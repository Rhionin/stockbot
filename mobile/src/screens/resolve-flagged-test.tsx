import { fireEvent, waitFor, within } from '@testing-library/react-native';
import { CODE_LINK_TARGET_GONE, LINK_EXISTING_SCAN_FAILURE } from '@stockbot/shared/contract';
import {
  NET_SIZE_PAIR_REQUIRED,
  PACK_COUNT_MINIMUM,
  PRODUCT_NAME_REQUIRED,
} from '@stockbot/shared/productForm';
import type { ProductSearchHit, ScanEntry, ScannerConfig } from '@stockbot/shared/types';
import { ApiError, type StockbotClient } from '../api/client';
import { renderScreen } from '../test/renderScreen';
import { resolverClientStubs } from '../test/resolverClient';
import { QueueScreen } from './QueueScreen';

jest.mock('../live/foreground', () => jest.requireActual('../test/fakeForeground'));

const flagged = (overrides: Partial<ScanEntry> = {}): ScanEntry => ({
  id: 'scan-1',
  userId: 'user-1',
  barcode: '0049000012345',
  scannedAt: '2026-10-16T12:00:00Z',
  direction: 'stock_in',
  unitCount: 1,
  expiresAt: null,
  status: 'flagged',
  productId: null,
  committedAt: null,
  createdAt: '2026-10-16T12:00:00Z',
  ...overrides,
});

const named = (row: ScanEntry, name: string, productId: string): ScanEntry => ({
  ...row,
  status: 'pending',
  productId,
  product: { id: productId, name, category: '', unitOfMeasure: 'bag' },
});

const config = (): ScannerConfig => ({ stockInBarcode: 'IN', stockOutBarcode: 'OUT', currentMode: 'stock_in', connected: true });

const hit = (productId: string, name: string, extra: Partial<ProductSearchHit> = {}): ProductSearchHit => ({
  productId,
  itemId: `item-${productId}`,
  name,
  onHand: 2,
  ...extra,
});

interface World {
  rows: ScanEntry[];
}

function setup(rows: ScanEntry[], overrides: Partial<StockbotClient> = {}) {
  const world: World = { rows: [...rows] };
  const api = {
    events: { url: 'https://stockbot.example/api/events', headers: {}, rejected: jest.fn() },
    getScannerConfig: jest.fn(async () => config()),
    setScannerMode: jest.fn(async (mode) => ({ mode })),
    listPending: jest.fn(async () => [...world.rows]),
    getInventoryList: jest.fn(async () => []),
    listInstances: jest.fn(async () => []),
    undoMove: jest.fn(async () => undefined),
    commitScan: jest.fn(async (id: string) => {
      world.rows = world.rows.filter((row) => row.id !== id);
      return { ...rows[0], id, status: 'committed' as const };
    }),
    cancelScan: jest.fn(async (id: string) => {
      world.rows = world.rows.filter((row) => row.id !== id);
      return { ...rows[0], id, status: 'cancelled' as const };
    }),
    batchCommit: jest.fn(async (ids: string[]) => ({ updatedCount: ids.length, committedIds: ids })),
    updateScan: jest.fn(async (id: string, input: { productId?: string }) => {
      const current = world.rows.find((row) => row.id === id) as ScanEntry;
      const next = named(current, 'Resolved product', input.productId ?? '');
      world.rows = world.rows.map((row) => (row.id === id ? next : row));
      return next;
    }),
    ...resolverClientStubs(),
    ...overrides,
  } as unknown as StockbotClient;
  return { api, world };
}

async function open(api: StockbotClient) {
  const screen = await renderScreen(<QueueScreen client={api} warning="" />);
  await screen.findByLabelText(/^What is it\? /);
  return screen;
}

type Screen = Awaited<ReturnType<typeof open>>;

async function openSheet(screen: Screen) {
  await fireEvent.press(await screen.findByLabelText(/^What is it\? /));
  return screen.findByText('What is it?', { selector: undefined } as never).catch(() => screen.getAllByText('What is it?')[0]);
}

const typeInto = (screen: Screen, label: string, text: string) => fireEvent.changeText(screen.getByLabelText(label), text);

describe('a flagged scan in the queue', () => {
  it('reads as needing a name, offers the resolver and Remove, and has no Confirm or checkbox', async () => {
    const { api } = setup([flagged({ unitCount: 3 })]);
    const screen = await open(api);

    expect(screen.getByText('0049000012345')).toBeTruthy();
    expect(screen.getByText('Needs a name  Putting away  \u00d73')).toBeTruthy();
    expect(screen.getByLabelText('What is it? 0049000012345')).toBeTruthy();
    expect(screen.getByLabelText('Remove 0049000012345')).toBeTruthy();
    expect(screen.queryByLabelText('Confirm 0049000012345')).toBeNull();
    expect(screen.queryByLabelText(/^Select 0049/)).toBeNull();
  });

  it('is left alone by Confirm all, which says why', async () => {
    const ready = named(flagged({ id: 'scan-2', barcode: '111', scannedAt: '2026-10-16T12:01:00Z' }), 'Oat milk', 'prod-2');
    const readyToo = named(flagged({ id: 'scan-3', barcode: '222', scannedAt: '2026-10-16T12:02:00Z' }), 'Rice', 'prod-3');
    const { api } = setup([flagged(), ready, readyToo]);
    const screen = await open(api);

    expect(screen.getByText('Confirm skips the 1 that needs a name.')).toBeTruthy();
    await fireEvent.press(screen.getByLabelText('Confirm all'));

    await waitFor(() => expect(api.batchCommit).toHaveBeenCalledTimes(1));
    expect(api.batchCommit).toHaveBeenCalledWith(['scan-2', 'scan-3']);
  });

  it('removes a flagged scan', async () => {
    const { api } = setup([flagged()]);
    const screen = await open(api);

    await fireEvent.press(screen.getByLabelText('Remove 0049000012345'));

    await waitFor(() => expect(api.cancelScan).toHaveBeenCalledWith('scan-1'));
    await waitFor(() => expect(screen.queryByLabelText('Remove 0049000012345')).toBeNull());
  });
});

describe('naming a flagged scan', () => {
  it('opens a sheet with a heading, the barcode and the looked-up name', async () => {
    const lookupProduct = jest.fn(async () => ({
      product: { id: 'off-1', name: 'Looked up oats', category: '', unitOfMeasure: '', externalSource: 'openfoodfacts' as const },
      source: 'openfoodfacts',
    }));
    const { api } = setup([flagged()], { lookupProduct });
    const screen = await open(api);
    await openSheet(screen);

    expect(lookupProduct).toHaveBeenCalledWith('0049000012345');
    expect(screen.getAllByRole('header').some((node) => node.props.children === 'What is it?')).toBe(true);
    expect(screen.getByText('Barcode 0049000012345')).toBeTruthy();
    await waitFor(() => expect(screen.getByLabelText('Product name').props.value).toBe('Looked up oats'));
    expect(screen.getByLabelText('Product data from Open Food Facts')).toBeTruthy();
  });

  it('keeps what was typed when the lookup answers late', async () => {
    let answer!: (value: { product: { id: string; name: string; category: string; unitOfMeasure: string }; source: string }) => void;
    const lookupProduct = jest.fn(() => new Promise<never>((resolve) => { answer = resolve as never; }));
    const { api } = setup([flagged()], { lookupProduct: lookupProduct as never });
    const screen = await open(api);
    await openSheet(screen);

    await typeInto(screen, 'Product name', 'My oats');
    answer({ product: { id: 'x', name: 'Looked up', category: '', unitOfMeasure: '' }, source: 'x' });

    await waitFor(() => expect(lookupProduct).toHaveBeenCalled());
    expect(screen.getByLabelText('Product name').props.value).toBe('My oats');
  });

  it('creates the product, resolves the scan and then confirms it with the new product', async () => {
    const createProduct = jest.fn(async () => ({
      id: 'prod-new',
      name: 'Oat flour',
      category: '',
      unitOfMeasure: 'bag',
      createdAt: '2026-10-16T12:00:00Z',
    }));
    const { api, world } = setup([flagged()], { createProduct });
    const calls: string[] = [];
    (api.updateScan as jest.Mock).mockImplementationOnce(async (id: string, input: { productId?: string }) => {
      calls.push('patch');
      const next = named(world.rows[0], 'Oat flour', input.productId ?? '');
      world.rows = [next];
      return next;
    });
    (api.createProductOverride as jest.Mock).mockImplementation(async () => { calls.push('override'); });
    createProduct.mockImplementation(async () => {
      calls.push('create');
      return { id: 'prod-new', name: 'Oat flour', category: '', unitOfMeasure: 'bag', createdAt: '' };
    });
    const screen = await open(api);
    await openSheet(screen);

    await typeInto(screen, 'Product name', '  Oat flour ');
    await typeInto(screen, 'Brand', 'Mill');
    await typeInto(screen, 'Size', '16');
    await fireEvent.press(screen.getByLabelText('oz'));
    await typeInto(screen, 'Package', 'bag');
    await typeInto(screen, 'Pack count', '2');
    await fireEvent.press(screen.getByLabelText('Create and use product'));

    await waitFor(() => expect(api.createProductOverride).toHaveBeenCalled());
    expect(createProduct).toHaveBeenCalledWith({
      name: 'Oat flour',
      brand: 'Mill',
      category: '',
      unitOfMeasure: 'bag',
      netAmount: 16,
      netUnit: 'oz',
      packCount: 2,
      barcode: '0049000012345',
    });
    expect(api.updateScan).toHaveBeenCalledWith('scan-1', { productId: 'prod-new', status: 'pending' });
    expect(api.createProductOverride).toHaveBeenCalledWith('0049000012345', 'prod-new');
    expect(calls).toEqual(['create', 'patch', 'override']);

    expect(await screen.findByLabelText('Confirm Oat flour')).toBeTruthy();
    expect(screen.queryByLabelText(/^What is it\?/)).toBeNull();
    await fireEvent.press(screen.getByLabelText('Confirm Oat flour'));
    await waitFor(() => expect(api.commitScan).toHaveBeenCalledWith('scan-1'));
    const listed = await (api.listPending as jest.Mock).mock.results.at(-1)?.value;
    expect(listed).toEqual([]);
  });

  it('does not post an invalid net size and says which field is wrong', async () => {
    const { api } = setup([flagged()]);
    const screen = await open(api);
    await openSheet(screen);

    await typeInto(screen, 'Product name', 'Oat flour');
    await typeInto(screen, 'Size', '16');
    await fireEvent.press(screen.getByLabelText('Create and use product'));

    expect(screen.getByText(NET_SIZE_PAIR_REQUIRED)).toBeTruthy();
    expect(screen.getByLabelText('Size').props['aria-invalid']).toBe(true);
    await typeInto(screen, 'Size', '0');
    await fireEvent.press(screen.getByLabelText('oz'));
    await typeInto(screen, 'Pack count', '0');
    expect(screen.getByText(PACK_COUNT_MINIMUM)).toBeTruthy();
    await fireEvent.press(screen.getByLabelText('Create and use product'));

    expect(api.createProduct).not.toHaveBeenCalled();
    expect(api.updateScan).not.toHaveBeenCalled();
    expect(screen.getByLabelText('Product name').props.value).toBe('Oat flour');
  });

  it('does not post without a name', async () => {
    const { api } = setup([flagged()]);
    const screen = await open(api);
    await openSheet(screen);

    await fireEvent.press(screen.getByLabelText('Create and use product'));

    expect(screen.getByText(PRODUCT_NAME_REQUIRED)).toBeTruthy();
    expect(screen.getByLabelText('Product name').props['aria-invalid']).toBe(true);
    expect(api.createProduct).not.toHaveBeenCalled();
  });

  it('leaves the scan flagged and the draft intact when the create fails', async () => {
    const createProduct = jest.fn(async () => { throw new ApiError(500, 'boom'); });
    const { api } = setup([flagged()], { createProduct });
    const screen = await open(api);
    await openSheet(screen);

    await typeInto(screen, 'Product name', 'Oat flour');
    await typeInto(screen, 'Brand', 'Mill');
    await typeInto(screen, 'Size', '16');
    await fireEvent.press(screen.getByLabelText('oz'));
    await fireEvent.press(screen.getByLabelText('Create and use product'));

    expect(await screen.findByText('boom')).toBeTruthy();
    expect(api.updateScan).not.toHaveBeenCalled();
    expect(api.createProductOverride).not.toHaveBeenCalled();
    expect(screen.getByLabelText('Product name').props.value).toBe('Oat flour');
    expect(screen.getByLabelText('Brand').props.value).toBe('Mill');
    expect(screen.getByLabelText('Size').props.value).toBe('16');
    expect(screen.getByLabelText('oz').props.accessibilityState.checked).toBe(true);
    await fireEvent.press(screen.getByLabelText('Close'));
    expect(screen.getByLabelText('What is it? 0049000012345')).toBeTruthy();
  });

  it('does not create a second product when the scan update fails and the person tries again', async () => {
    const createProduct = jest.fn(async () => ({ id: 'prod-new', name: 'Oat flour', category: '', unitOfMeasure: '', createdAt: '' }));
    const updateScan = jest.fn()
      .mockRejectedValueOnce(new ApiError(500, 'boom'))
      .mockImplementation(async (_id: string, input: { productId?: string }) => named(flagged(), 'Oat flour', input.productId ?? ''));
    const { api } = setup([flagged()], { createProduct, updateScan });
    const screen = await open(api);
    await openSheet(screen);
    await typeInto(screen, 'Product name', 'Oat flour');

    await fireEvent.press(screen.getByLabelText('Create and use product'));
    await screen.findByText('boom');
    await fireEvent.press(screen.getByLabelText('Create and use product'));

    await waitFor(() => expect(updateScan).toHaveBeenCalledTimes(2));
    expect(createProduct).toHaveBeenCalledTimes(1);
    expect(updateScan).toHaveBeenLastCalledWith('scan-1', { productId: 'prod-new', status: 'pending' });
  });

  it('sends the contribute choice and shows the receipt after saving', async () => {
    const createProduct = jest.fn(async () => ({
      id: 'prod-new',
      name: 'Oat flour',
      category: '',
      unitOfMeasure: '',
      createdAt: '',
      contribution: { status: 'submitted' as const, detail: 'Sent to Open Food Facts.' },
    }));
    const updateContributionSettings = jest.fn(async (enabled: boolean) => ({ enabled, configured: true }));
    const { api } = setup([flagged()], { createProduct, updateContributionSettings });
    const screen = await open(api);
    await openSheet(screen);

    await fireEvent(screen.getByLabelText('Let me contribute products I type in'), 'valueChange', true);
    await fireEvent.press(await screen.findByLabelText('Contribute this product'));
    await fireEvent.press(screen.getByLabelText('Open Products Facts'));
    await typeInto(screen, 'Product name', 'Oat flour');
    await fireEvent.press(screen.getByLabelText('Create and use product'));

    await waitFor(() => expect(createProduct).toHaveBeenCalled());
    expect(updateContributionSettings).toHaveBeenCalledWith(true);
    expect(createProduct).toHaveBeenCalledWith(expect.objectContaining({ contribute: true, contributeTo: 'openproductsfacts' }));
    expect(await screen.findByText('Sent to Open Food Facts.')).toBeTruthy();
  });

  it('switches the household sharing setting back with a sentence when the save fails', async () => {
    const updateContributionSettings = jest.fn(async () => { throw new ApiError(500, 'nope'); });
    const { api } = setup([flagged()], { updateContributionSettings });
    const screen = await open(api);
    await openSheet(screen);

    await fireEvent(screen.getByLabelText('Let me contribute products I type in'), 'valueChange', true);

    expect(await screen.findByText(/nope|Unable to save sharing setting/)).toBeTruthy();
    expect(screen.getByLabelText('Let me contribute products I type in').props.value).toBe(false);
    expect(screen.queryByLabelText('Contribute this product')).toBeNull();
  });
});

describe('linking a flagged scan to something we already have', () => {
  const oats = hit('prod-oats', 'Rolled oats', { brand: 'Mill', size: '42 oz', onHand: 2 });
  const stray = hit('prod-stray', 'Oat bran', { itemId: null, onHand: 0 });

  it('searches, picks a row and resolves the scan with its product', async () => {
    const searchProducts = jest.fn(async () => [oats, stray]);
    const { api } = setup([flagged()], { searchProducts });
    const screen = await open(api);
    await openSheet(screen);

    expect(screen.getByLabelText(/Use selected product/).props.accessibilityState.disabled).toBe(true);
    await typeInto(screen, 'Search products', 'oat');
    expect(await screen.findByLabelText('Rolled oats (Mill)')).toBeTruthy();
    expect(searchProducts).toHaveBeenCalledWith('oat', 20);
    expect(screen.getByText('42 oz  2 on hand')).toBeTruthy();
    expect(screen.getByText('Not on your shelf yet')).toBeTruthy();
    await fireEvent.press(screen.getByLabelText('Rolled oats (Mill)'));
    expect(screen.getByLabelText('Rolled oats (Mill)').props.accessibilityState.checked).toBe(true);
    await fireEvent.press(screen.getByLabelText('Use selected product'));

    await waitFor(() => expect(api.createProductOverride).toHaveBeenCalledWith('0049000012345', 'prod-oats'));
    expect(api.updateScan).toHaveBeenCalledWith('scan-1', { productId: 'prod-oats', status: 'pending' });
    expect(api.createProduct).not.toHaveBeenCalled();
    expect(await screen.findByLabelText('Confirm Resolved product')).toBeTruthy();
  });

  it('asks for two letters before it searches and says when nothing matches', async () => {
    const searchProducts = jest.fn(async () => []);
    const { api } = setup([flagged()], { searchProducts });
    const screen = await open(api);
    await openSheet(screen);

    await typeInto(screen, 'Search products', 'o');
    expect(screen.getByText('Type at least 2 letters.')).toBeTruthy();
    expect(searchProducts).not.toHaveBeenCalled();
    await typeInto(screen, 'Search products', 'zz');
    expect(await screen.findByText('No matching products.')).toBeTruthy();
  });

  it('drops a product that is gone and keeps the sheet open with a sentence', async () => {
    const searchProducts = jest.fn(async () => [oats]);
    const updateScan = jest.fn(async () => { throw new ApiError(409, 'That item is gone.', CODE_LINK_TARGET_GONE); });
    const { api } = setup([flagged()], { searchProducts, updateScan });
    const screen = await open(api);
    await openSheet(screen);
    await typeInto(screen, 'Search products', 'oat');
    await fireEvent.press(await screen.findByLabelText('Rolled oats (Mill)'));

    await fireEvent.press(screen.getByLabelText('Use selected product'));

    expect(await screen.findByText(/gone|Couldn't add it/)).toBeTruthy();
    expect(screen.queryByLabelText('Rolled oats (Mill)')).toBeNull();
    expect(api.createProductOverride).not.toHaveBeenCalled();
  });

  it('shows the plain failure sentence for any other server error and keeps the scan flagged', async () => {
    const searchProducts = jest.fn(async () => [oats]);
    const updateScan = jest.fn(async () => { throw new ApiError(500, 'internal detail'); });
    const { api } = setup([flagged()], { searchProducts, updateScan });
    const screen = await open(api);
    await openSheet(screen);
    await typeInto(screen, 'Search products', 'oat');
    await fireEvent.press(await screen.findByLabelText('Rolled oats (Mill)'));

    await fireEvent.press(screen.getByLabelText('Use selected product'));

    expect(await screen.findByText(LINK_EXISTING_SCAN_FAILURE)).toBeTruthy();
    expect(screen.getByLabelText('Rolled oats (Mill)').props.accessibilityState.checked).toBe(true);
    await fireEvent.press(screen.getByLabelText('Close'));
    expect(screen.getByLabelText('What is it? 0049000012345')).toBeTruthy();
  });
});

describe('what a resolve keeps (#327)', () => {
  it('sends only the product and the status, and the scan keeps its count, direction and expiry', async () => {
    const original = flagged({ unitCount: 3, direction: 'stock_in', expiresAt: '2027-03-01T00:00:00Z' });
    const searchProducts = jest.fn(async () => [hit('prod-oats', 'Rolled oats')]);
    const { api, world } = setup([original], { searchProducts });
    (api.updateScan as jest.Mock).mockImplementation(async (_id: string, input: { productId?: string }) => {
      // The server applies the product and the status and leaves the other columns as they were.
      const next = { ...named(original, 'Rolled oats', input.productId ?? '') };
      world.rows = [next];
      return next;
    });
    const screen = await open(api);
    await openSheet(screen);
    await typeInto(screen, 'Search products', 'oat');
    await fireEvent.press(await screen.findByLabelText('Rolled oats'));
    await fireEvent.press(screen.getByLabelText('Use selected product'));

    expect(await screen.findByLabelText('Confirm Rolled oats')).toBeTruthy();
    const body = (api.updateScan as jest.Mock).mock.calls[0]?.[1];
    expect(body).toEqual({ productId: 'prod-oats', status: 'pending' });
    expect(Object.keys(body)).not.toContain('unitCount');
    expect(Object.keys(body)).not.toContain('expiresAt');
    expect(Object.keys(body)).not.toContain('direction');
    const card = within(screen.getByTestId('scan-scan-1'));
    expect(card.getByLabelText('Unit count').props.value).toBe('3');
    expect(card.getByText(/Putting away/)).toBeTruthy();
    expect(world.rows[0]).toMatchObject({ unitCount: 3, direction: 'stock_in', expiresAt: '2027-03-01T00:00:00Z' });
  });
});
