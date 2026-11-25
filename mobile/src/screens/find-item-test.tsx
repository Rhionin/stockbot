import { act, fireEvent, waitFor } from '@testing-library/react-native';
import { CODE_NONE_ON_HAND } from '@stockbot/shared/contract';
import type { ProductSearchHit, ProductWriteResult, ScanDirection, ScannerConfig } from '@stockbot/shared/types';
import { ApiError, type StockbotClient } from '../api/client';
import { renderScreen } from '../test/renderScreen';
import { resolverClientStubs } from '../test/resolverClient';
import { QueueScreen } from './QueueScreen';

jest.mock('../live/foreground', () => jest.requireActual('../test/fakeForeground'));

const hit = (productId: string, name: string, extra: Partial<ProductSearchHit> = {}): ProductSearchHit => ({
  productId,
  itemId: `item-${productId}`,
  name,
  onHand: 2,
  ...extra,
});

const deferred = <T,>() => {
  let resolve!: (value: T) => void;
  let reject!: (reason: unknown) => void;
  const promise = new Promise<T>((done, fail) => {
    resolve = done;
    reject = fail;
  });
  return { promise, resolve, reject };
};

function setup(mode: ScanDirection, overrides: Partial<StockbotClient> = {}) {
  const config: ScannerConfig = { stockInBarcode: 'IN', stockOutBarcode: 'OUT', currentMode: mode, connected: true };
  return {
    events: { url: 'https://stockbot.example/api/events', headers: {}, rejected: jest.fn() },
    getScannerConfig: jest.fn(async () => config),
    setScannerMode: jest.fn(async (next: ScanDirection) => ({ mode: next })),
    listPending: jest.fn(async () => []),
    getInventoryList: jest.fn(async () => []),
    listInstances: jest.fn(async () => []),
    undoMove: jest.fn(async () => undefined),
    ...resolverClientStubs(),
    ...overrides,
  } as unknown as StockbotClient;
}

async function open(client: StockbotClient) {
  const screen = await renderScreen(<QueueScreen client={client} warning="" />);
  await fireEvent.press(await screen.findByLabelText('Find an item'));
  await screen.findByRole('header', { name: 'Find an item' });
  return screen;
}

type Screen = Awaited<ReturnType<typeof open>>;

const searchFor = (screen: Screen, text: string) => fireEvent.changeText(screen.getByTestId('field-find-item-search'), text);

const sheetOpen = (screen: Screen) => screen.queryByRole('header', { name: 'Find an item' }) !== null;

describe('the Find an item button', () => {
  it('opens the sheet with the direction sentence and the search field', async () => {
    const screen = await open(setup('stock_in'));

    expect(screen.getAllByText('Scans add to stock')).toHaveLength(2);
    expect(screen.getByPlaceholderText('Type a name')).toBeTruthy();
    expect(screen.getByLabelText('Close')).toBeTruthy();
  });

  it('says scans remove from stock on the stock out tab', async () => {
    const screen = await open(setup('stock_out'));

    expect(screen.getAllByText('Scans remove from stock')).toHaveLength(2);
  });

  it('closes with Close and sends nothing', async () => {
    const client = setup('stock_in');
    const screen = await open(client);

    await fireEvent.press(screen.getByLabelText('Close'));

    await waitFor(() => expect(sheetOpen(screen)).toBe(false));
    expect(client.searchProducts).not.toHaveBeenCalled();
  });
});

describe('searching', () => {
  it('sends no request for a search shorter than two letters and says so', async () => {
    const client = setup('stock_in', { searchProducts: jest.fn(async () => [hit('p1', 'Jam')]) });
    const screen = await open(client);

    await searchFor(screen, 'j');

    expect(await screen.findByText('Type at least 2 letters.')).toBeTruthy();
    await act(async () => { await new Promise((done) => setTimeout(done, 450)); });
    expect(client.searchProducts).not.toHaveBeenCalled();
    expect(screen.queryByLabelText('Search results')).toBeNull();
    expect(screen.queryByLabelText(/^Add "/)).toBeNull();
  });

  it('lists matches in a named list, each with a spoken name, and offers to add what was typed', async () => {
    const client = setup('stock_in', {
      searchProducts: jest.fn(async () => [
        hit('p1', 'Jam jar', { brand: 'Pantry', size: '16 oz', onHand: 0 }),
        hit('p2', 'Jam tart', { onHand: 1, itemId: null }),
      ]),
    });
    const screen = await open(client);

    await searchFor(screen, 'jam');

    const list = await screen.findByLabelText('Search results');
    expect(client.searchProducts).toHaveBeenCalledTimes(1);
    expect(client.searchProducts).toHaveBeenCalledWith('jam');
    expect(list.props.role).toBe('list');
    expect(screen.getByRole('button', { name: 'Jam jar Pantry · 16 oz None on hand' })).toBeTruthy();
    expect(screen.getByRole('button', { name: 'Jam tart 1 on hand' })).toBeTruthy();
    expect(screen.getByLabelText('Add "jam"')).toBeTruthy();
  });

  it('does not offer to add a name a result already has', async () => {
    const client = setup('stock_in', { searchProducts: jest.fn(async () => [hit('p1', 'Jam')]) });
    const screen = await open(client);

    await searchFor(screen, 'jam');

    await screen.findByLabelText('Search results');
    expect(screen.queryByLabelText(/^Add "/)).toBeNull();
  });

  it('says nothing is called that when there are no matches', async () => {
    const screen = await open(setup('stock_in'));

    await searchFor(screen, 'zzz');

    expect(await screen.findByText('Nothing called "zzz" yet.')).toBeTruthy();
    expect(screen.getByLabelText('Add "zzz"')).toBeTruthy();
  });

  it('keeps the newer results when an older search answers last', async () => {
    const first = deferred<ProductSearchHit[]>();
    const second = deferred<ProductSearchHit[]>();
    const searchProducts = jest.fn((query: string) => (query === 'ja' ? first.promise : second.promise));
    const screen = await open(setup('stock_in', { searchProducts }));

    await searchFor(screen, 'ja');
    await waitFor(() => expect(searchProducts).toHaveBeenCalledWith('ja'));
    await searchFor(screen, 'jam');
    await waitFor(() => expect(searchProducts).toHaveBeenCalledWith('jam'));
    await act(async () => { second.resolve([hit('p2', 'Jam new')]); });
    expect(await screen.findByRole('button', { name: 'Jam new 2 on hand' })).toBeTruthy();
    await act(async () => { first.resolve([hit('p1', 'Jam stale')]); });

    expect(screen.queryByRole('button', { name: 'Jam stale 2 on hand' })).toBeNull();
    expect(screen.getByRole('button', { name: 'Jam new 2 on hand' })).toBeTruthy();
  });

  it('shows the failure and no results when the search fails', async () => {
    const client = setup('stock_in', { searchProducts: jest.fn(async () => { throw new ApiError(500, 'Search is down.'); }) });
    const screen = await open(client);

    await searchFor(screen, 'jam');

    expect(await screen.findByText('Search is down.')).toBeTruthy();
    expect(screen.queryByLabelText('Search results')).toBeNull();
    expect(screen.queryByText(/^Nothing called/)).toBeNull();
  });
});

describe('stocking in', () => {
  async function pick(client: StockbotClient, label: string) {
    const screen = await open(client);
    await searchFor(screen, 'jam');
    await fireEvent.press(await screen.findByRole('button', { name: label }));
    return screen;
  }

  it('adds one unit to the item, closes the sheet and offers Undo that removes that unit', async () => {
    const client = setup('stock_in', { searchProducts: jest.fn(async () => [hit('p1', 'Jam')]) });
    const screen = await pick(client, 'Jam 2 on hand');

    expect(await screen.findByText('Added 1 Jam.')).toBeTruthy();
    expect(client.addItemInstance).toHaveBeenCalledWith('item-p1');
    expect(client.ensureProductItem).not.toHaveBeenCalled();
    expect(sheetOpen(screen)).toBe(false);
    await waitFor(() => expect((client.listPending as jest.Mock).mock.calls.length).toBeGreaterThan(1));

    await fireEvent.press(screen.getByLabelText('Undo'));

    await waitFor(() => expect(client.removeItemInstance).toHaveBeenCalledWith('instance-1'));
    expect(client.undoMove).not.toHaveBeenCalled();
  });

  it('makes the item first for a product that is not on the shelf yet', async () => {
    const client = setup('stock_in', { searchProducts: jest.fn(async () => [hit('p9', 'Jam', { itemId: null, onHand: 0 })]) });
    const screen = await pick(client, 'Jam None on hand');

    expect(await screen.findByText('Added 1 Jam.')).toBeTruthy();
    expect(client.ensureProductItem).toHaveBeenCalledWith('p9');
    expect(client.addItemInstance).toHaveBeenCalledWith('item-p9');
  });

  it('leaves the sheet open with the error when recording fails', async () => {
    const client = setup('stock_in', {
      searchProducts: jest.fn(async () => [hit('p1', 'Jam')]),
      addItemInstance: jest.fn(async () => { throw new ApiError(500, 'The shelf is locked.'); }),
    });
    const screen = await pick(client, 'Jam 2 on hand');

    expect(await screen.findByText('The shelf is locked.')).toBeTruthy();
    expect(sheetOpen(screen)).toBe(true);
    expect(screen.queryByText('Added 1 Jam.')).toBeNull();
    expect(screen.queryByLabelText('Undo')).toBeNull();
    expect(screen.getByRole('button', { name: 'Jam 2 on hand' })).toBeTruthy();
    expect((client.listPending as jest.Mock).mock.calls.length).toBe(1);
  });

  it('says so when the Undo fails', async () => {
    const client = setup('stock_in', {
      searchProducts: jest.fn(async () => [hit('p1', 'Jam')]),
      removeItemInstance: jest.fn(async () => { throw new ApiError(404, 'That unit is already gone.'); }),
    });
    const screen = await pick(client, 'Jam 2 on hand');

    await fireEvent.press(await screen.findByLabelText('Undo'));

    expect(await screen.findByText('That unit is already gone.')).toBeTruthy();
  });
});

describe('stocking out', () => {
  async function pick(client: StockbotClient, label: string) {
    const screen = await open(client);
    await searchFor(screen, 'jam');
    await fireEvent.press(await screen.findByRole('button', { name: label }));
    return screen;
  }

  it('takes out one unit, closes the sheet and offers Undo for that move', async () => {
    const client = setup('stock_out', { searchProducts: jest.fn(async () => [hit('p1', 'Jam')]) });
    const screen = await pick(client, 'Jam 2 on hand');

    expect(await screen.findByText('Took out 1 Jam.')).toBeTruthy();
    expect(client.stockOutItem).toHaveBeenCalledWith('item-p1', undefined);
    expect(sheetOpen(screen)).toBe(false);

    await fireEvent.press(screen.getByLabelText('Undo'));

    await waitFor(() => expect(client.undoMove).toHaveBeenCalledWith('move-out-1'));
  });

  it('offers no Undo when the server sent no move id', async () => {
    const client = setup('stock_out', {
      searchProducts: jest.fn(async () => [hit('p1', 'Jam')]),
      stockOutItem: jest.fn(async () => ({})),
    });
    const screen = await pick(client, 'Jam 2 on hand');

    expect(await screen.findByText('Took out 1 Jam.')).toBeTruthy();
    expect(screen.queryByLabelText('Undo')).toBeNull();
  });

  it('asks before adding and taking out an item with none on hand, and takes out nothing yet', async () => {
    const client = setup('stock_out', { searchProducts: jest.fn(async () => [hit('p1', 'Jam', { onHand: 0 })]) });
    const screen = await pick(client, 'Jam None on hand');

    expect(await screen.findByText('You have 0 of Jam on hand. If you had one anyway, this adds it and takes it out in one step.')).toBeTruthy();
    expect(screen.getByLabelText('None on hand', { exact: true })).toBeTruthy();
    expect(client.stockOutItem).not.toHaveBeenCalled();
    expect(screen.queryByLabelText('Search results')).toBeNull();

    await fireEvent.press(screen.getByLabelText('Back'));

    expect(await screen.findByLabelText('Search results')).toBeTruthy();
    expect(screen.queryByLabelText('Add it and take it out')).toBeNull();
    expect(client.stockOutItem).not.toHaveBeenCalled();
  });

  it('adds it and takes it out, then Undo removes the pair through the stock in move', async () => {
    const client = setup('stock_out', {
      searchProducts: jest.fn(async () => [hit('p1', 'Jam', { onHand: 0 })]),
      stockOutItem: jest.fn(async () => ({ backfill: { stockInMoveId: 'move-in', stockOutMoveId: 'move-out' } })),
    });
    const screen = await pick(client, 'Jam None on hand');

    await fireEvent.press(await screen.findByLabelText('Add it and take it out'));

    expect(await screen.findByText('Added and took out 1 Jam.')).toBeTruthy();
    expect(client.stockOutItem).toHaveBeenCalledWith('item-p1', { backfill: true });
    expect(sheetOpen(screen)).toBe(false);

    await fireEvent.press(screen.getByLabelText('Undo'));

    await waitFor(() => expect(client.undoMove).toHaveBeenCalledWith('move-in'));
    expect(client.undoMove).toHaveBeenCalledTimes(1);
  });

  it('asks the same question when the server says none are on hand for a row that showed some', async () => {
    const stockOutItem = jest.fn(async () => { throw new ApiError(409, 'None on hand.', CODE_NONE_ON_HAND); });
    const client = setup('stock_out', { searchProducts: jest.fn(async () => [hit('p1', 'Jam')]), stockOutItem });
    const screen = await pick(client, 'Jam 2 on hand');

    expect(await screen.findByText(/^You have 0 of Jam on hand/)).toBeTruthy();
    expect(screen.queryByText('None on hand.')).toBeNull();
    expect(sheetOpen(screen)).toBe(true);
  });

  it('keeps the question open with the error when adding and taking out fails', async () => {
    const client = setup('stock_out', {
      searchProducts: jest.fn(async () => [hit('p1', 'Jam', { onHand: 0 })]),
      stockOutItem: jest.fn(async () => { throw new ApiError(500, 'The shelf is locked.'); }),
    });
    const screen = await pick(client, 'Jam None on hand');

    await fireEvent.press(await screen.findByLabelText('Add it and take it out'));

    expect(await screen.findByText('The shelf is locked.')).toBeTruthy();
    expect(sheetOpen(screen)).toBe(true);
    expect(screen.getByLabelText('Add it and take it out')).toBeTruthy();
    expect(screen.queryByText('Added and took out 1 Jam.')).toBeNull();
  });

  it('leaves the sheet open with the error when taking out fails', async () => {
    const client = setup('stock_out', {
      searchProducts: jest.fn(async () => [hit('p1', 'Jam')]),
      stockOutItem: jest.fn(async () => { throw new ApiError(500, 'The shelf is locked.'); }),
    });
    const screen = await pick(client, 'Jam 2 on hand');

    expect(await screen.findByText('The shelf is locked.')).toBeTruthy();
    expect(sheetOpen(screen)).toBe(true);
    expect(screen.queryByText('Took out 1 Jam.')).toBeNull();
  });
});

describe('adding an item', () => {
  async function openAdd(client: StockbotClient, typed = 'Peach jam') {
    const screen = await open(client);
    await searchFor(screen, typed);
    await fireEvent.press(await screen.findByLabelText(`Add "${typed}"`));
    await screen.findByRole('header', { name: 'Add an item' });
    return screen;
  }

  const created = (extra: Partial<ProductWriteResult> = {}): ProductWriteResult => ({
    id: 'prod-new',
    name: 'Peach jam',
    category: '',
    unitOfMeasure: '',
    createdAt: '2026-10-16T12:00:00Z',
    ...extra,
  });

  it('opens with the typed name filled in, and the buttons say what saving does in this direction', async () => {
    const screen = await openAdd(setup('stock_in'));

    expect(screen.getByTestId('field-add-item-name').props.value).toBe('Peach jam');
    expect(screen.getByLabelText('Brand')).toBeTruthy();
    expect(screen.getByPlaceholderText('e.g. 16 oz')).toBeTruthy();
    expect(screen.getByLabelText('Category')).toBeTruthy();
    expect(screen.getByLabelText('Add and stock in')).toBeTruthy();
    expect(screen.getByLabelText('Cancel')).toBeTruthy();
  });

  it('says Add it and take it out on the stock out tab', async () => {
    const screen = await openAdd(setup('stock_out'));

    expect(screen.getByLabelText('Add it and take it out')).toBeTruthy();
  });

  it('asks for a name and sends nothing when it is empty', async () => {
    const client = setup('stock_in');
    const screen = await openAdd(client);
    await fireEvent.changeText(screen.getByTestId('field-add-item-name'), '   ');

    await fireEvent.press(screen.getByLabelText('Add and stock in'));

    expect(await screen.findByText('Enter a name.')).toBeTruthy();
    expect(screen.getByTestId('field-add-item-name').props['aria-invalid']).toBe(true);
    expect(client.createProduct).not.toHaveBeenCalled();
  });

  it('creates the product with the web payload, stocks one in and closes both sheets', async () => {
    const client = setup('stock_in', {
      createProduct: jest.fn(async () => created({ itemId: 'item-new' })),
    });
    const screen = await openAdd(client);
    await fireEvent.changeText(screen.getByLabelText('Brand'), ' Millhouse ');
    await fireEvent.changeText(screen.getByLabelText('Size'), '16 oz');
    await fireEvent.changeText(screen.getByLabelText('Category'), 'Preserves');

    await fireEvent.press(screen.getByLabelText('Add and stock in'));

    expect(await screen.findByText('Added 1 Peach jam.')).toBeTruthy();
    expect(client.createProduct).toHaveBeenCalledWith({
      name: 'Peach jam',
      brand: 'Millhouse',
      size: '16 oz',
      category: 'Preserves',
      unitOfMeasure: '',
    });
    expect(client.ensureProductItem).not.toHaveBeenCalled();
    expect(client.addItemInstance).toHaveBeenCalledWith('item-new');
    expect(screen.queryByRole('header', { name: 'Add an item' })).toBeNull();
    expect(sheetOpen(screen)).toBe(false);
  });

  it('makes the item when the new product came back without one', async () => {
    const client = setup('stock_in', { createProduct: jest.fn(async () => created()) });
    const screen = await openAdd(client);

    await fireEvent.press(screen.getByLabelText('Add and stock in'));

    await screen.findByText('Added 1 Peach jam.');
    expect(client.ensureProductItem).toHaveBeenCalledWith('prod-new');
    expect(client.addItemInstance).toHaveBeenCalledWith('item-prod-new');
  });

  it('adds and takes out in one step on the stock out tab, with Undo for the pair', async () => {
    const client = setup('stock_out', {
      createProduct: jest.fn(async () => created({ itemId: 'item-new' })),
      stockOutItem: jest.fn(async () => ({ backfill: { stockInMoveId: 'move-in', stockOutMoveId: 'move-out' } })),
    });
    const screen = await openAdd(client);

    await fireEvent.press(screen.getByLabelText('Add it and take it out'));

    expect(await screen.findByText('Added and took out 1 Peach jam.')).toBeTruthy();
    expect(client.stockOutItem).toHaveBeenCalledWith('item-new', { backfill: true });
    await fireEvent.press(screen.getByLabelText('Undo'));
    await waitFor(() => expect(client.undoMove).toHaveBeenCalledWith('move-in'));
  });

  it('keeps the form open with the error when the product cannot be saved', async () => {
    const client = setup('stock_in', {
      createProduct: jest.fn(async () => { throw new ApiError(500, 'Could not save.'); }),
    });
    const screen = await openAdd(client);

    await fireEvent.press(screen.getByLabelText('Add and stock in'));

    expect(await screen.findByText('Could not save.')).toBeTruthy();
    expect(screen.getByRole('header', { name: 'Add an item' })).toBeTruthy();
    expect(client.addItemInstance).not.toHaveBeenCalled();
    expect(screen.queryByText('Added 1 Peach jam.')).toBeNull();
  });

  it('returns to the search with the error when the product saved but stocking it failed', async () => {
    const client = setup('stock_in', {
      createProduct: jest.fn(async () => created({ itemId: 'item-new' })),
      addItemInstance: jest.fn(async () => { throw new ApiError(500, 'The shelf is locked.'); }),
    });
    const screen = await openAdd(client);

    await fireEvent.press(screen.getByLabelText('Add and stock in'));

    await waitFor(() => expect(screen.queryByRole('header', { name: 'Add an item' })).toBeNull());
    expect(await screen.findByText('The shelf is locked.')).toBeTruthy();
    expect(sheetOpen(screen)).toBe(true);
    expect(screen.queryByText('Added 1 Peach jam.')).toBeNull();
  });

  it('swaps the search for the form and keeps what was typed', async () => {
    const screen = await openAdd(setup('stock_in'));

    expect(screen.queryByRole('header', { name: 'Find an item' })).toBeNull();
    await fireEvent.press(screen.getByLabelText('Cancel'));

    expect((await screen.findByTestId('field-find-item-search')).props.value).toBe('Peach jam');
  });

  it('Cancel goes back to the search without saving', async () => {
    const client = setup('stock_in');
    const screen = await openAdd(client);

    await fireEvent.press(screen.getByLabelText('Cancel'));

    await waitFor(() => expect(screen.queryByRole('header', { name: 'Add an item' })).toBeNull());
    expect(sheetOpen(screen)).toBe(true);
    expect(client.createProduct).not.toHaveBeenCalled();
  });
});
