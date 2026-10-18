import { fireEvent, waitFor, within } from '@testing-library/react-native';
import type { InventoryItem, PantryLinkSuggestion, ScanEntry } from '../../../frontend/src/types';
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

function householdItem(name: string, productId: string, instanceCount: number, shelved = false): InventoryItem {
  return {
    item: {
      id: `item-${productId}`,
      userId: 'user-1',
      productId,
      product: { id: productId, name, category: 'Pantry', unitOfMeasure: 'bag' },
      targetQuantity: null,
      createdAt: '2026-10-16T12:00:00Z',
      ...(shelved ? { shelved: true } : {}),
    } as InventoryItem['item'],
    instanceCount,
    nearExpiryCount: 0,
    expiredCount: 0,
    needsAttention: false,
  };
}

function client(overrides: Partial<StockbotClient> = {}): StockbotClient {
  return {
    getSession: jest.fn(),
    getScannerConfig: jest.fn(),
    setScannerMode: jest.fn(),
    createScan: jest.fn(),
    listPending: jest.fn(async () => [entry(), entry({ id: 'scan-2', barcode: '999', product: null, productId: null, direction: 'stock_out' })]),
    commitScan: jest.fn(async () => entry({ status: 'committed' })),
    batchCommit: jest.fn(async () => ({ updatedCount: 2, committedIds: ['scan-1', 'scan-2'] })),
    getInventoryList: jest.fn(async () => []),
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

  it.each([
    {
      label: 'one failure',
      failures: [{ id: 'scan-2', reason: 'direction not set', code: '' }],
    },
    {
      label: 'several failures',
      failures: [
        { id: 'scan-1', reason: 'Nothing on hand to take out.', code: 'none_on_hand' },
        { id: 'scan-2', reason: 'Choose which item this is first.', code: 'product_unresolved' },
      ],
    },
  ])('batch $label puts each reason on its card', async ({ failures }) => {
    const api = client({
      listPending: jest.fn(async () => [
        entry({ direction: 'stock_out' }),
        entry({ id: 'scan-2', barcode: '999', product: null, productId: null, direction: 'stock_out' }),
      ]),
      batchCommit: jest.fn(async () => ({
        updatedCount: 0,
        failedIds: failures.map((failure) => failure.id),
        failedReasons: failures.map((failure) => failure.reason),
        failedCodes: failures.map((failure) => failure.code ?? ''),
      })),
    });
    const screen = await renderScreen(<QueueScreen client={api} warning="" />);

    await fireEvent.press(await screen.findByLabelText('Confirm all'));

    expect(await screen.findByText('Some scans could not be confirmed.')).toBeTruthy();
    expect(screen.getAllByText('Some scans could not be confirmed.')).toHaveLength(1);
    expect(screen.queryByTestId('confirm-toast')).toBeNull();
    for (const failure of failures) {
      const card = within(screen.getByTestId(`scan-${failure.id}`));
      expect(card.getByText(failure.reason)).toBeTruthy();
      expect(card.queryByText('Some scans could not be confirmed.')).toBeNull();
    }
    const unresolved = failures.find((failure) => failure.code === 'product_unresolved');
    if (unresolved) {
      const card = within(screen.getByTestId(`scan-${unresolved.id}`));
      expect(card.getByText('Which item is this?')).toBeTruthy();
      expect(card.getByLabelText('Search')).toBeTruthy();
      expect(card.queryByText('No close match found.')).toBeNull();
    }
    const stockedOut = failures.find((failure) => failure.code === 'none_on_hand');
    if (stockedOut) {
      expect(within(screen.getByTestId(`scan-${stockedOut.id}`)).queryByLabelText('Search')).toBeNull();
    }
  });

  const pears: PantryLinkSuggestion[] = [
    { itemId: 'item-pears', productId: 'prod-pears', name: 'Sliced Pears', onHand: 3 },
  ];

  it.each([
    { suggestions: pears },
    { suggestions: [] as PantryLinkSuggestion[] },
  ])('stock-out no_item with $suggestions.length suggestions offers a link', async ({ suggestions }) => {
    const api = client({
      listPending: jest.fn(async () => [entry({ direction: 'stock_out' })]),
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
      expect(screen.getByLabelText('Link Sliced Pears')).toBeTruthy();
      expect(screen.queryByTestId('confirm-toast')).toBeNull();
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
    await fireEvent.press(await screen.findByLabelText('Link Sliced Pears'));

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

  it.each([
    {
      label: 'product unresolved',
      status: 400,
      code: 'product_unresolved',
      message: 'Choose which item this is first.',
      heading: true,
    },
    {
      label: 'none on hand',
      status: 409,
      code: 'none_on_hand',
      message: 'Nothing on hand to take out.',
      heading: false,
    },
    {
      label: 'instance gone',
      status: 409,
      code: 'instance_gone',
      message: 'That one was already taken out. Pick another.',
      heading: false,
    },
  ])('$label keeps the scan and shows the server sentence', async ({ status, code, message, heading }) => {
    const api = client({
      listPending: jest.fn(async () => [entry({ direction: 'stock_out' })]),
      commitScan: jest.fn(async () => {
        throw new ApiError(status, message, code, pears);
      }),
    });
    const screen = await renderScreen(<QueueScreen client={api} warning="" />);

    await fireEvent.press(await screen.findByLabelText('Confirm Oat milk'));

    expect(within(await screen.findByTestId('confirm-toast')).getByText(message)).toBeTruthy();
    expect(screen.getByText('Oat milk')).toBeTruthy();
    if (heading) {
      expect(screen.getByText('Which item is this?')).toBeTruthy();
      expect(screen.getByLabelText('Search')).toBeTruthy();
      expect(screen.queryByText('No close match found.')).toBeNull();
      expect(screen.queryByText('Sliced Pears')).toBeNull();
      expect(screen.queryByLabelText(/Link /)).toBeNull();
    } else {
      expect(screen.queryByText('Which item is this?')).toBeNull();
      expect(screen.queryByLabelText('Search')).toBeNull();
    }
  });

  const slices: PantryLinkSuggestion = { itemId: 'item-slices', productId: 'prod-slices', name: 'Pear Slices', onHand: 1 };
  const riceSuggestion: PantryLinkSuggestion = { itemId: 'item-rice', productId: 'prod-brown', name: 'Brown Rice', onHand: 2 };

  it.each([
    { label: 'uses the list on the response', gone: [slices], again: null as PantryLinkSuggestion[] | null, shown: 'Pear Slices' },
    { label: 'refetches when the response lists nothing', gone: [] as PantryLinkSuggestion[], again: [riceSuggestion], shown: 'Brown Rice' },
  ])('link_target_gone $label', async ({ gone, again, shown }) => {
    const commitScan = jest.fn(async (_id: string, linkProductId?: string) => {
      if (linkProductId === 'prod-pears') {
        throw new ApiError(409, "That item isn't available anymore. Pick another.", 'link_target_gone', gone);
      }
      if (again && linkProductId === undefined && commitScan.mock.calls.length > 1) {
        throw new ApiError(409, 'unlinked barcode', 'no_item', again);
      }
      throw new ApiError(409, 'unlinked barcode', 'no_item', [pears[0]]);
    });
    const api = client({
      listPending: jest.fn(async () => [entry({ direction: 'stock_out' })]),
      commitScan,
    });
    const screen = await renderScreen(<QueueScreen client={api} warning="" />);

    await fireEvent.press(await screen.findByLabelText('Confirm Oat milk'));
    await fireEvent.press(await screen.findByLabelText('Link Sliced Pears'));

    expect(await screen.findByText(shown)).toBeTruthy();
    expect(within(screen.getByTestId('confirm-toast')).getByText("That item isn't available anymore. Pick another.")).toBeTruthy();
    expect(screen.queryByText('Sliced Pears')).toBeNull();
    expect(screen.getByText('Oat milk')).toBeTruthy();
    expect(screen.getByLabelText('Search')).toBeTruthy();
    expect(screen.queryByText('No close match found.')).toBeNull();
    expect(commitScan).toHaveBeenCalledTimes(again ? 3 : 2);
  });

  it('finds a zero-stock household item and leaves a shelved item out', async () => {
    const rice = householdItem('Rice', 'prod-rice', 0);
    const cakes = householdItem('Rice cakes', 'prod-cakes', 2, true);
    const getInventoryList = jest.fn(async () => [rice, cakes]);
    const commitScan = jest.fn(async (_id: string, linkProductId?: string) => {
      if (linkProductId === 'prod-rice') return entry({ status: 'committed' });
      throw new ApiError(409, 'unlinked barcode', 'no_item', []);
    });
    const api = client({
      listPending: jest.fn(async () => [entry({ direction: 'stock_out' })]),
      commitScan,
      getInventoryList,
    });
    const screen = await renderScreen(<QueueScreen client={api} warning="" />);

    await fireEvent.press(await screen.findByLabelText('Confirm Oat milk'));
    await fireEvent.changeText(await screen.findByLabelText('Search'), 'rice');

    expect(await screen.findByText('Rice')).toBeTruthy();
    expect(screen.getByText('0 in stock')).toBeTruthy();
    expect(screen.queryByText('Rice cakes')).toBeNull();
    expect(getInventoryList).toHaveBeenCalledWith();
    await fireEvent.press(screen.getByLabelText('Link Rice'));
    await waitFor(() => expect(commitScan).toHaveBeenLastCalledWith('scan-1', 'prod-rice'));
  });

  it('shows a short confirm result in the toast', async () => {
    const api = client({
      listPending: jest.fn(async () => [entry({ unitCount: 3 })]),
      commitScan: jest.fn(async () => Object.assign(entry({ unitCount: 3, status: 'committed' }), { removedCount: 1 })),
    });
    const screen = await renderScreen(<QueueScreen client={api} warning="" />);

    await fireEvent.press(await screen.findByLabelText('Confirm Oat milk'));

    expect(within(await screen.findByTestId('confirm-toast')).getByText('Only 1 was on hand, so 1 was taken out.')).toBeTruthy();
  });

  it.each([
    {
      label: 'product unresolved',
      code: 'product_unresolved',
      message: 'Choose which item this is first.',
      refetch: false,
    },
    {
      label: 'link target gone',
      code: 'link_target_gone',
      message: "That item isn't available anymore. Pick another.",
      refetch: true,
    },
  ])('batch $label follows the code', async ({ code, message, refetch }) => {
    const slices: PantryLinkSuggestion = { itemId: 'item-slices', productId: 'prod-slices', name: 'Pear Slices', onHand: 1 };
    const api = client({
      listPending: jest.fn(async () => [
        entry({ direction: 'stock_out' }),
        entry({ id: 'scan-2', barcode: '999', product: null, productId: null, direction: 'stock_out' }),
      ]),
      commitScan: jest.fn(async () => {
        throw new ApiError(409, 'unlinked barcode', 'no_item', [slices]);
      }),
      batchCommit: jest.fn(async () => ({
        updatedCount: 1,
        failedIds: ['scan-1'],
        failedReasons: [message],
        failedCodes: [code],
      })),
    });
    const screen = await renderScreen(<QueueScreen client={api} warning="" />);

    await fireEvent.press(await screen.findByLabelText('Confirm all'));

    expect(await screen.findByText(message)).toBeTruthy();
    expect(screen.getByText('Which item is this?')).toBeTruthy();
    expect(screen.getByLabelText('Search')).toBeTruthy();
    expect(screen.queryByText('No close match found.')).toBeNull();
    expect(screen.getByText('Oat milk')).toBeTruthy();
    if (refetch) {
      expect(screen.getByText('Pear Slices')).toBeTruthy();
      expect(api.commitScan).toHaveBeenCalledWith('scan-1');
    } else {
      expect(api.commitScan).not.toHaveBeenCalled();
      expect(screen.queryByLabelText(/Link /)).toBeNull();
    }
  });

  it('shows an empty queue as an invitation to scan', async () => {
    const api = client({ listPending: jest.fn(async () => []) });
    const screen = await renderScreen(<QueueScreen client={api} warning="" />);

    expect(await screen.findByText(/Nothing is waiting/)).toBeTruthy();
    expect(screen.queryByLabelText('Confirm all')).toBeNull();
  });
});
