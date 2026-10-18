import { fireEvent, waitFor } from '@testing-library/react-native';
import type { PantryLinkSuggestion, ScanEntry } from '../../../frontend/src/types';
import { ApiError, type StockbotClient } from '../api/client';
import { renderScreen } from '../test/renderScreen';
import { QueueScreen } from './QueueScreen';

const entry = (overrides: Partial<ScanEntry> = {}): ScanEntry => ({
  id: 'scan-1',
  userId: 'user-1',
  barcode: '111222333444',
  scannedAt: '2026-10-16T12:00:00Z',
  direction: 'stock_in',
  unitCount: 1,
  expiresAt: null,
  status: 'pending',
  productId: 'prod-1',
  product: {
    id: 'prod-1',
    name: 'Oat milk',
    category: 'Dairy',
    unitOfMeasure: 'carton',
  },
  committedAt: null,
  createdAt: '2026-10-16T12:00:00Z',
  ...overrides,
});

function client(overrides: Partial<StockbotClient> = {}): StockbotClient {
  return {
    getSession: jest.fn(),
    getScannerConfig: jest.fn(),
    setScannerMode: jest.fn(),
    createScan: jest.fn(),
    listPending: jest.fn(async () => [entry(), entry({ id: 'scan-2', barcode: '999', product: null, productId: null, direction: 'stock_out' })]),
    commitScan: jest.fn(async () => entry({ status: 'committed' })),
    batchCommit: jest.fn(async () => ({ updatedCount: 2, committedIds: ['scan-1', 'scan-2'] })),
    ...overrides,
  };
}

describe('QueueScreen', () => {
  it('lists pending scans and confirms one', async () => {
    const api = client();
    const screen = await renderScreen(<QueueScreen client={api} warning="" />);

    expect(await screen.findByText('Oat milk')).toBeTruthy();
    expect(screen.getByText('2 scans are waiting')).toBeTruthy();
    expect(screen.getByText('999')).toBeTruthy();

    await fireEvent.press(screen.getByLabelText('Confirm Oat milk'));

    await waitFor(() => expect(api.commitScan).toHaveBeenCalledWith('scan-1'));
  });

  it('confirms every waiting scan', async () => {
    const api = client();
    const screen = await renderScreen(<QueueScreen client={api} warning="" />);
    await fireEvent.press(await screen.findByLabelText('Confirm all'));

    await waitFor(() => expect(api.batchCommit).toHaveBeenCalledWith(['scan-1', 'scan-2']));
  });

  it('keeps the reason when a batch leaves a scan unconfirmed', async () => {
    const api = client({
      batchCommit: jest.fn(async () => ({
        updatedCount: 2,
        committedIds: ['scan-1'],
        failedIds: ['scan-2'],
        failedReasons: ['direction not set'],
      })),
    });
    const screen = await renderScreen(<QueueScreen client={api} warning="" />);

    await fireEvent.press(await screen.findByLabelText('Confirm all'));

    expect(await screen.findByText('direction not set')).toBeTruthy();
  });

  it('offers pantry items when a stock-out is not stocked, then links the one that is picked', async () => {
    const suggestions: PantryLinkSuggestion[] = [
      { itemId: 'item-pears', productId: 'prod-pears', name: 'Sliced Pears', onHand: 3 },
      { itemId: 'item-slices', productId: 'prod-slices', name: 'Pear Slices', onHand: 1 },
    ];
    const pears = entry({
      direction: 'stock_out',
      product: { id: 'prod-1', name: 'Lite Pear Slices', category: 'Fruit', unitOfMeasure: 'can' },
    });
    const commitScan = jest.fn(async (_id: string, linkProductId?: string) => {
      if (linkProductId !== 'prod-pears') {
        throw new ApiError(409, 'Not in your pantry', 'no_item', suggestions);
      }
      return entry({ status: 'committed' });
    });
    const api = client({
      listPending: jest.fn(async () => [pears]),
      commitScan,
    });
    const screen = await renderScreen(<QueueScreen client={api} warning="" />);

    await fireEvent.press(await screen.findByLabelText('Confirm Lite Pear Slices'));

    expect(await screen.findByText('Not in your pantry')).toBeTruthy();
    expect(screen.getByText('Link the barcode to an item you stock. That scan out uses the item you pick.')).toBeTruthy();
    expect(screen.getByText('Sliced Pears')).toBeTruthy();
    expect(screen.getByText('3 in stock')).toBeTruthy();
    expect(screen.getByText('Pear Slices')).toBeTruthy();
    expect(commitScan).toHaveBeenCalledTimes(1);
    expect(commitScan).toHaveBeenCalledWith('scan-1');

    await fireEvent.press(screen.getByLabelText('Link Sliced Pears and scan it out'));

    await waitFor(() => expect(commitScan).toHaveBeenLastCalledWith('scan-1', 'prod-pears'));
    await waitFor(() => expect(screen.queryByText('Not in your pantry')).toBeNull());
  });

  it('dismisses pantry suggestions and leaves the scan waiting', async () => {
    const api = client({
      listPending: jest.fn(async () => [entry({ direction: 'stock_out' })]),
      commitScan: jest.fn(async () => {
        throw new ApiError(409, 'Not in your pantry', 'no_item', [
          { itemId: 'item-pears', productId: 'prod-pears', name: 'Sliced Pears', onHand: 3 },
        ]);
      }),
    });
    const screen = await renderScreen(<QueueScreen client={api} warning="" />);

    await fireEvent.press(await screen.findByLabelText('Confirm Oat milk'));
    expect(await screen.findByText('Sliced Pears')).toBeTruthy();
    await fireEvent.press(screen.getByLabelText('Dismiss'));

    await waitFor(() => expect(screen.queryByText('Not in your pantry')).toBeNull());
    expect(screen.getByText('Oat milk')).toBeTruthy();
    expect(api.commitScan).toHaveBeenCalledTimes(1);
  });

  it('says when no stocked item matches', async () => {
    const api = client({
      listPending: jest.fn(async () => [entry({ direction: 'stock_out' })]),
      commitScan: jest.fn(async () => {
        throw new ApiError(409, 'Not in your pantry', 'no_item', []);
      }),
    });
    const screen = await renderScreen(<QueueScreen client={api} warning="" />);

    await fireEvent.press(await screen.findByLabelText('Confirm Oat milk'));

    expect(await screen.findByText('No close match found.')).toBeTruthy();
    expect(screen.queryByLabelText(/Link /)).toBeNull();
  });

  it('shows an empty queue as an invitation to scan', async () => {
    const api = client({ listPending: jest.fn(async () => []) });
    const screen = await renderScreen(<QueueScreen client={api} warning="" />);

    expect(await screen.findByText(/Nothing is waiting/)).toBeTruthy();
    expect(screen.queryByLabelText('Confirm all')).toBeNull();
  });
});
