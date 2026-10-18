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

function householdItem(name: string, productId: string, instanceCount: number, category = 'Pantry'): InventoryItem {
  return {
    item: {
      id: `item-${productId}`,
      userId: 'user-1',
      productId,
      product: { id: productId, name, category, unitOfMeasure: 'bag' },
      targetQuantity: null,
      createdAt: '2026-10-16T12:00:00Z',
    },
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
    getInventoryList: jest.fn(async () => [householdItem('Oat milk', 'prod-1', 2)]),
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
    expect(screen.queryByTestId('confirm-toast')).toBeNull();
    expect(screen.queryByText('Only 1 was on hand, so 1 was taken out.')).toBeNull();
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
    { label: 'suggestions', suggestions: pears, missing: false },
    { label: 'no suggestions', suggestions: [] as PantryLinkSuggestion[], missing: false },
    { label: 'an in-stock suggestion while the product is absent', suggestions: pears, missing: true },
  ])('stock-out no_item with $label offers a link', async ({ suggestions, missing }) => {
    const api = client({
      listPending: jest.fn(async () => [entry({ direction: 'stock_out' })]),
      commitScan: jest.fn(async () => {
        throw new ApiError(409, 'Which item is this?', 'no_item', suggestions);
      }),
      ...(missing ? { getInventoryList: jest.fn(async () => []) } : {}),
    });
    const screen = await renderScreen(<QueueScreen client={api} warning="" />);

    await fireEvent.press(await screen.findByLabelText('Confirm Oat milk'));

    expect(await screen.findByText('Which item is this?')).toBeTruthy();
    expect(screen.getByLabelText('Search')).toBeTruthy();
    expect(screen.queryByText(/Not in your pantry/)).toBeNull();
    expect(screen.queryByText('Nothing on hand to take out.')).toBeNull();
    if (suggestions.length === 0) {
      expect(screen.getByText('No close match found.')).toBeTruthy();
      expect(screen.queryByLabelText(/Link /)).toBeNull();
    } else {
      expect(screen.getByText('Sliced Pears')).toBeTruthy();
      expect(screen.getByText('3 in stock')).toBeTruthy();
      expect(screen.getByLabelText('Link Sliced Pears')).toBeTruthy();
      expect(screen.queryByTestId('confirm-toast')).toBeNull();
      expect(screen.queryByText('No close match found.')).toBeNull();
    }
    if (missing) {
      await fireEvent.press(screen.getByLabelText('Dismiss'));
      expect(await screen.findByText('Nothing on hand to take out.')).toBeTruthy();
      expect(screen.queryByText('Which item is this?')).toBeNull();
    }
  });

  it('links the picked stock-out suggestion', async () => {
    const commitScan = jest.fn(async (_id: string, linkProductId?: string) => {
      if (linkProductId !== 'prod-pears') throw new ApiError(409, 'Which item is this?', 'no_item', pears);
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
        throw new ApiError(409, 'Which item is this?', 'no_item', pears);
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
      label: 'partial stock-out',
      direction: 'stock_out' as const,
      counts: { removedCount: 1, requestedCount: 3 },
      note: 'Only 1 was on hand, so 1 was taken out.',
    },
    {
      label: 'equal counts',
      direction: 'stock_out' as const,
      counts: { removedCount: 3, requestedCount: 3 },
      note: '',
    },
    {
      label: 'stock-in',
      direction: 'stock_in' as const,
      counts: {},
      note: '',
    },
  ])('$label shows the short-shelf note only when removedCount is below requestedCount', async ({ direction, counts, note }) => {
    const api = client({
      listPending: jest.fn(async () => [entry({ direction, unitCount: 3 })]),
      commitScan: jest.fn(async () => ({
        ...entry({
          id: 'scan-partial',
          barcode: 'scan-partial',
          direction,
          unitCount: 3,
          status: 'committed',
          productId: 'prod-partial',
        }),
        ...counts,
      })),
    });
    const screen = await renderScreen(<QueueScreen client={api} warning="" />);

    await fireEvent.press(await screen.findByLabelText('Confirm Oat milk'));

    await waitFor(() => expect(api.listPending).toHaveBeenCalledTimes(2));
    if (note === '') {
      expect(screen.queryByTestId('confirm-toast')).toBeNull();
      expect(screen.queryByText('Only 1 was on hand, so 1 was taken out.')).toBeNull();
    } else {
      expect(within(screen.getByTestId('confirm-toast')).getByText(note)).toBeTruthy();
    }
  });

  it('none on hand keeps the scan and shows the server sentence', async () => {
    const api = client({
      listPending: jest.fn(async () => [entry({ direction: 'stock_out' })]),
      commitScan: jest.fn(async () => {
        throw new ApiError(409, 'Nothing on hand to take out.', 'none_on_hand');
      }),
    });
    const screen = await renderScreen(<QueueScreen client={api} warning="" />);

    await fireEvent.press(await screen.findByLabelText('Confirm Oat milk'));

    expect(within(await screen.findByTestId('confirm-toast')).getByText('Nothing on hand to take out.')).toBeTruthy();
    expect(screen.getByText('Oat milk')).toBeTruthy();
    expect(screen.queryByText('Which item is this?')).toBeNull();
    expect(screen.queryByLabelText('Search')).toBeNull();
  });

  const slices: PantryLinkSuggestion = { itemId: 'item-slices', productId: 'prod-slices', name: 'Pear Slices', onHand: 1 };

  it.each([
    { label: 'the only suggestion', suggestions: pears, remaining: '', empty: true },
    { label: 'one of two suggestions', suggestions: [pears[0], slices], remaining: 'Pear Slices', empty: false },
  ])('link_target_gone drops $label and reloads the household list', async ({ suggestions, remaining, empty }) => {
    const commitScan = jest.fn(async (_id: string, linkProductId?: string) => {
      if (linkProductId === 'prod-pears') {
        throw new ApiError(409, "That item isn't available anymore. Pick another.", 'link_target_gone');
      }
      throw new ApiError(409, 'Which item is this?', 'no_item', suggestions);
    });
    const getInventoryList = jest.fn(async () => [
      householdItem('Sliced Pears', 'prod-pears', 3),
      householdItem('Brown Rice', 'prod-brown', 2),
    ]);
    const api = client({
      listPending: jest.fn(async () => [entry({ direction: 'stock_out' })]),
      commitScan,
      getInventoryList,
    });
    const screen = await renderScreen(<QueueScreen client={api} warning="" />);

    await fireEvent.press(await screen.findByLabelText('Confirm Oat milk'));
    await fireEvent.press(await screen.findByLabelText('Link Sliced Pears'));

    expect(within(await screen.findByTestId('confirm-toast')).getByText("That item isn't available anymore. Pick another.")).toBeTruthy();
    expect(screen.getByText('Oat milk')).toBeTruthy();
    expect(screen.getByLabelText('Search')).toBeTruthy();
    expect(commitScan).toHaveBeenCalledTimes(2);
    await waitFor(() => expect(getInventoryList).toHaveBeenCalledTimes(2));
    expect(getInventoryList).toHaveBeenCalledWith();
    if (empty) {
      expect(screen.getByText('No close match found.')).toBeTruthy();
      expect(screen.queryByText('Sliced Pears')).toBeNull();
    } else {
      expect(screen.getByText(remaining)).toBeTruthy();
      expect(screen.queryByText('No close match found.')).toBeNull();
      expect(screen.queryByText('Sliced Pears')).toBeNull();
    }

    await fireEvent(screen.getByLabelText('Search'), 'focus');
    expect(screen.getByText('Brown Rice')).toBeTruthy();
    expect(screen.queryByText('Sliced Pears')).toBeNull();
    expect(screen.queryByLabelText('Link Sliced Pears')).toBeNull();
  });

  it('keeps the link open when a zero-stock item has nothing on hand', async () => {
    const rice: PantryLinkSuggestion = { itemId: 'item-rice', productId: 'prod-rice', name: 'Rice', onHand: 0 };
    const commitScan = jest.fn(async (_id: string, linkProductId?: string) => {
      if (linkProductId === 'prod-rice') throw new ApiError(409, 'Nothing on hand to take out.', 'none_on_hand');
      throw new ApiError(409, 'Which item is this?', 'no_item', [rice]);
    });
    const api = client({
      listPending: jest.fn(async () => [entry({ direction: 'stock_out' })]),
      commitScan,
      getInventoryList: jest.fn(async () => [householdItem('Oat milk', 'prod-1', 2), householdItem('Rice', 'prod-rice', 0)]),
    });
    const screen = await renderScreen(<QueueScreen client={api} warning="" />);

    await fireEvent.press(await screen.findByLabelText('Confirm Oat milk'));
    await fireEvent.press(await screen.findByLabelText('Link Rice'));

    expect(await screen.findByText('Which item is this?')).toBeTruthy();
    expect(screen.getByLabelText('Link Rice')).toBeTruthy();
    expect(screen.getByText('Nothing on hand to take out.')).toBeTruthy();
    expect(screen.queryByText('Add it and take it out')).toBeNull();
    expect(screen.getByText('Oat milk')).toBeTruthy();
  });

  it.each([
    { label: 'an empty query', query: '', visible: ['Rice', 'Oats'], hidden: [] as string[], stock: ['0 in stock', '2 in stock'], unmatched: false },
    { label: 'a category', query: 'grain', visible: ['Oats'], hidden: ['Rice'], stock: ['2 in stock'], unmatched: false },
    { label: 'a name and category together', query: 'oats grain', visible: ['Oats'], hidden: ['Rice'], stock: ['2 in stock'], unmatched: false },
    { label: 'no match', query: 'quinoa', visible: [] as string[], hidden: ['Rice', 'Oats'], stock: [] as string[], unmatched: true },
  ])('search with $label lists household items', async ({ query, visible, hidden, stock, unmatched }) => {
    const api = client({
      listPending: jest.fn(async () => [entry({ direction: 'stock_out' })]),
      commitScan: jest.fn(async () => {
        throw new ApiError(409, 'Which item is this?', 'no_item', []);
      }),
      getInventoryList: jest.fn(async () => [
        householdItem('Rice', 'prod-rice', 0, 'Pantry'),
        householdItem('Oats', 'prod-oats', 2, 'Grain'),
      ]),
    });
    const screen = await renderScreen(<QueueScreen client={api} warning="" />);

    await fireEvent.press(await screen.findByLabelText('Confirm Oat milk'));
    const search = await screen.findByLabelText('Search');
    if (query === '') await fireEvent(search, 'focus');
    else await fireEvent.changeText(search, query);

    for (const name of visible) expect(screen.getByText(name)).toBeTruthy();
    for (const name of hidden) expect(screen.queryByText(name)).toBeNull();
    for (const line of stock) expect(screen.getByText(line)).toBeTruthy();
    if (unmatched) expect(screen.getByText('No matching products.')).toBeTruthy();
    else expect(screen.queryByText('No matching products.')).toBeNull();
  });

  it('links a household search result', async () => {
    const commitScan = jest.fn(async (_id: string, linkProductId?: string) => {
      if (linkProductId === 'prod-oats') return entry({ status: 'committed' });
      throw new ApiError(409, 'Which item is this?', 'no_item', []);
    });
    const api = client({
      listPending: jest.fn(async () => [entry({ direction: 'stock_out' })]),
      commitScan,
      getInventoryList: jest.fn(async () => [householdItem('Oats', 'prod-oats', 2, 'Grain')]),
    });
    const screen = await renderScreen(<QueueScreen client={api} warning="" />);

    await fireEvent.press(await screen.findByLabelText('Confirm Oat milk'));
    await fireEvent.changeText(await screen.findByLabelText('Search'), 'oats');
    await fireEvent.press(await screen.findByLabelText('Link Oats'));

    await waitFor(() => expect(commitScan).toHaveBeenLastCalledWith('scan-1', 'prod-oats'));
  });

  it('batch product unresolved opens search without another commit', async () => {
    const api = client({
      listPending: jest.fn(async () => [
        entry({ direction: 'stock_out' }),
        entry({ id: 'scan-2', barcode: '999', product: null, productId: null, direction: 'stock_out' }),
      ]),
      batchCommit: jest.fn(async () => ({
        updatedCount: 1,
        failedIds: ['scan-1'],
        failedReasons: ['Choose which item this is first.'],
        failedCodes: ['product_unresolved'],
      })),
    });
    const screen = await renderScreen(<QueueScreen client={api} warning="" />);

    await fireEvent.press(await screen.findByLabelText('Confirm all'));

    expect(await screen.findByText('Choose which item this is first.')).toBeTruthy();
    expect(screen.getByText('Which item is this?')).toBeTruthy();
    expect(screen.getByLabelText('Search')).toBeTruthy();
    expect(screen.queryByText('No close match found.')).toBeNull();
    expect(screen.getByText('Oat milk')).toBeTruthy();
    expect(api.commitScan).not.toHaveBeenCalled();
    expect(screen.queryByLabelText(/Link /)).toBeNull();
  });

  it.each([
    { label: 'never stocked', direction: 'stock_out' as const, productId: 'prod-new' as string | null, name: 'Quinoa', count: null as number | null, sentence: true },
    { label: 'wiped', direction: 'stock_out' as const, productId: 'prod-wiped', name: 'Rice cakes', count: null, sentence: true },
    { label: 'stocked', direction: 'stock_out' as const, productId: 'prod-oats', name: 'Oats', count: 2, sentence: false },
    { label: 'zero on hand', direction: 'stock_out' as const, productId: 'prod-rice', name: 'Rice', count: 0, sentence: false },
    { label: 'stock-in', direction: 'stock_in' as const, productId: 'prod-new', name: 'Quinoa', count: null, sentence: false },
    { label: 'no product yet', direction: 'stock_out' as const, productId: null, name: '999', count: null, sentence: false },
  ])('a $label card says nothing is on hand only when that stock-out product is absent', async ({ direction, productId, name, count, sentence }) => {
    const api = client({
      listPending: jest.fn(async () => [entry({
        direction,
        productId,
        barcode: '999',
        product: productId === null ? null : { id: productId, name, category: 'Pantry', unitOfMeasure: 'bag' },
      })]),
      getInventoryList: jest.fn(async () => (
        count === null || productId === null ? [] : [householdItem(name, productId, count)]
      )),
    });
    const screen = await renderScreen(<QueueScreen client={api} warning="" />);
    const card = within(await screen.findByTestId('scan-scan-1'));

    if (sentence) expect(card.getByText('Nothing on hand to take out.')).toBeTruthy();
    else expect(card.queryByText('Nothing on hand to take out.')).toBeNull();
    expect(screen.queryByText('No inventory item is available for this product.')).toBeNull();
  });

  it('hides More while the link card is open', async () => {
    const api = client({
      listPending: jest.fn(async () => [entry({ direction: 'stock_out' })]),
      commitScan: jest.fn(async () => {
        throw new ApiError(409, 'Which item is this?', 'no_item', pears);
      }),
    });
    const screen = await renderScreen(<QueueScreen client={api} warning="" />);

    await fireEvent.press(await screen.findByLabelText('Confirm Oat milk'));

    expect(await screen.findByText('Which item is this?')).toBeTruthy();
    expect(screen.queryByText('More')).toBeNull();
    expect(screen.queryByLabelText('More')).toBeNull();
    expect(screen.queryByText('No inventory item is available for this product.')).toBeNull();
  });

  it('shows an empty queue as an invitation to scan', async () => {
    const api = client({ listPending: jest.fn(async () => []) });
    const screen = await renderScreen(<QueueScreen client={api} warning="" />);

    expect(await screen.findByText(/Nothing is waiting/)).toBeTruthy();
    expect(screen.queryByLabelText('Confirm all')).toBeNull();
  });
});
