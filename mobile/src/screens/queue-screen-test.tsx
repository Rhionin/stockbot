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

  const pears: PantryLinkSuggestion[] = [
    { itemId: 'item-pears', productId: 'prod-pears', name: 'Sliced Pears', onHand: 3 },
  ];

  it.each([
    { direction: 'stock_out' as const, suggestions: pears },
    { direction: 'stock_out' as const, suggestions: [] as PantryLinkSuggestion[] },
    { direction: 'stock_in' as const, suggestions: pears },
    { direction: 'stock_in' as const, suggestions: [] as PantryLinkSuggestion[] },
  ])('$direction no_item with $suggestions.length suggestions uses the same card', async ({ direction, suggestions }) => {
    const api = client({
      listPending: jest.fn(async () => [entry({ direction })]),
      commitScan: jest.fn(async () => {
        throw new ApiError(409, 'unlinked barcode', 'no_item', suggestions);
      }),
    });
    const screen = await renderScreen(<QueueScreen client={api} warning="" />);

    await fireEvent.press(await screen.findByLabelText('Confirm Oat milk'));

    expect(await screen.findByText('Which item is this?')).toBeTruthy();
    expect(screen.getByLabelText('Search')).toBeTruthy();
    expect(screen.queryByText(/Not in your pantry/)).toBeNull();
    expect(screen.queryByText(/Nothing on hand/)).toBeNull();
    if (suggestions.length === 0) {
      expect(screen.getByText('No close match found.')).toBeTruthy();
      expect(screen.queryByLabelText(/Link /)).toBeNull();
    } else {
      expect(screen.getByText('Sliced Pears')).toBeTruthy();
      expect(screen.getByLabelText('Link Sliced Pears and scan it out')).toBeTruthy();
      expect(screen.queryByText('No close match found.')).toBeNull();
    }
  });

  it('links the picked stock-out suggestion', async () => {
    const commitScan = jest.fn(async (_id: string, linkProductId?: string) => {
      if (linkProductId !== 'prod-pears') throw new ApiError(409, 'unlinked barcode', 'no_item', pears);
      return entry({ status: 'committed' });
    });
    const api = client({
      listPending: jest.fn(async () => [entry({ direction: 'stock_out' })]),
      commitScan,
    });
    const screen = await renderScreen(<QueueScreen client={api} warning="" />);

    await fireEvent.press(await screen.findByLabelText('Confirm Oat milk'));
    await fireEvent.press(await screen.findByLabelText('Link Sliced Pears and scan it out'));

    await waitFor(() => expect(commitScan).toHaveBeenLastCalledWith('scan-1', 'prod-pears'));
    await waitFor(() => expect(screen.queryByText('Which item is this?')).toBeNull());
  });

  it('dismisses the unlinked card and leaves the scan waiting', async () => {
    const api = client({
      listPending: jest.fn(async () => [entry({ direction: 'stock_out' })]),
      commitScan: jest.fn(async () => {
        throw new ApiError(409, 'unlinked barcode', 'no_item', pears);
      }),
    });
    const screen = await renderScreen(<QueueScreen client={api} warning="" />);

    await fireEvent.press(await screen.findByLabelText('Confirm Oat milk'));
    await fireEvent.press(await screen.findByLabelText('Dismiss'));

    await waitFor(() => expect(screen.queryByText('Which item is this?')).toBeNull());
    expect(screen.getByText('Oat milk')).toBeTruthy();
    expect(api.commitScan).toHaveBeenCalledTimes(1);
  });

  it('shows an empty queue as an invitation to scan', async () => {
    const api = client({ listPending: jest.fn(async () => []) });
    const screen = await renderScreen(<QueueScreen client={api} warning="" />);

    expect(await screen.findByText(/Nothing is waiting/)).toBeTruthy();
    expect(screen.queryByLabelText('Confirm all')).toBeNull();
  });
});
