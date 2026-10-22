import { beforeEach, describe, expect, it, vi } from 'vitest';
import { fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { MantineProvider } from '@mantine/core';
import { Notifications } from '@mantine/notifications';
import { NOTHING_ON_HAND_BODY, NOTHING_ON_HAND_TITLE } from '../../api/contract';
import { ScanQueuePage } from './ScanQueuePage';
import type { ScanEntry } from '../../types';

class FakeEventSource {
  static instances: FakeEventSource[] = [];
  readonly url: string;
  private readonly listeners = new Map<string, Array<(event: MessageEvent) => void>>();

  constructor(url: string) {
    this.url = url;
    FakeEventSource.instances.push(this);
  }

  addEventListener(type: string, listener: (event: MessageEvent) => void) {
    const current = this.listeners.get(type) ?? [];
    current.push(listener);
    this.listeners.set(type, current);
  }

  close() {}

  dispatch(type: string, payload: unknown) {
    const data = JSON.stringify(payload);
    for (const listener of this.listeners.get(type) ?? []) {
      listener({ data } as MessageEvent);
    }
  }
}

const product = (id: string, name: string) => ({
  id,
  name,
  category: 'Grocery',
  unitOfMeasure: 'bag',
});

const scan = (overrides: Partial<ScanEntry>): ScanEntry => ({
  id: 'scan-1',
  userId: 'user-1',
  barcode: '111',
  scannedAt: '2026-03-20T16:02:00Z',
  direction: 'stock_out',
  unitCount: 1,
  expiresAt: null,
  status: 'pending',
  productId: 'prod-milk',
  product: product('prod-milk', 'Milk'),
  committedAt: null,
  createdAt: '2026-03-20T16:02:00Z',
  ...overrides,
});

const shelf = (id: string, name: string, instanceCount: number) => ({
  item: {
    id: `item-${id}`,
    userId: 'user-1',
    productId: `prod-${id}`,
    product: product(`prod-${id}`, name),
    targetQuantity: null,
    createdAt: '2026-03-20T10:00:00Z',
  },
  instanceCount,
  nearExpiryCount: 0,
  expiredCount: 0,
  needsAttention: false,
});

const json = (body: unknown, status = 200) =>
  new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } });

const cardName = (barcode: string) => new RegExp(`^Scan ${barcode}(,|$)`);

interface QueueHarness {
  scans: ScanEntry[];
  inventory?: unknown;
  nextInventory?: unknown;
  mode?: 'stock_in' | 'stock_out';
  failInventory?: boolean;
  holdInventory?: boolean;
}

const renderQueue = (harness: QueueHarness) => {
  let inventory = harness.inventory ?? [];
  let inventoryLoads = 0;
  let pending = harness.scans;
  let releaseInventory: (items: unknown) => void = () => {};
  const heldInventory = harness.holdInventory
    ? new Promise<unknown>((resolve) => {
      releaseInventory = resolve;
    })
    : null;
  const commits: string[] = [];
  const batches: string[][] = [];
  const patches: { id: string; body: unknown }[] = [];
  const fetchMock = vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
    const url = String(input);
    const method = init?.method ?? 'GET';
    if (url.includes('batch-commit') && method === 'POST') {
      const body = JSON.parse(String(init?.body ?? '{}')) as { scanEntryIds?: string[] };
      batches.push(body.scanEntryIds ?? []);
      return Promise.resolve(json({ updatedCount: body.scanEntryIds?.length ?? 0, committedIds: body.scanEntryIds ?? [] }));
    }
    if (url.endsWith('/commit') && method === 'POST') {
      const id = url.split('/').at(-2) ?? '';
      commits.push(id);
      if (id === 'scan-stale' || id === 'scan-pin') {
        return Promise.resolve(json({ error: NOTHING_ON_HAND_TITLE, code: 'none_on_hand' }, 409));
      }
      if (id === 'scan-missing') {
        return Promise.resolve(json({ error: 'Which item is this?', code: 'no_item', suggestions: [] }, 409));
      }
      if (id === 'scan-in') {
        inventory = [shelf('milk', 'Milk', 1)];
        pending = pending.filter((entry) => entry.id !== id);
        return Promise.resolve(json({ ...scan({ id, direction: 'stock_in', status: 'committed' }) }));
      }
      pending = pending.filter((entry) => entry.id !== id);
      return Promise.resolve(json({ status: 'committed' }));
    }
    if (method === 'PATCH' && /\/api\/scans\/[^/]+$/.test(url)) {
      const id = url.split('/').at(-1) ?? '';
      const body = JSON.parse(String(init?.body ?? '{}')) as { status?: string; unitCount?: number };
      patches.push({ id, body });
      if (body.status === 'cancelled') pending = pending.filter((entry) => entry.id !== id);
      return Promise.resolve(json({ status: body.status ?? 'pending' }));
    }
    if (url.includes('status=pending')) return Promise.resolve(json(pending));
    if (url.includes('status=flagged') || url.includes('status=qr')) return Promise.resolve(json([]));
    if (url === '/api/inventory' || url.startsWith('/api/inventory?')) {
      inventoryLoads += 1;
      if (!url.includes('userId=user-1')) return Promise.reject(new Error(`inventory request omitted the household: ${url}`));
      if (harness.failInventory) return Promise.reject(new Error('inventory down'));
      if (heldInventory !== null && inventoryLoads === 1) return heldInventory.then((items) => json(items));
      if (inventoryLoads > 1 && harness.nextInventory !== undefined) return Promise.resolve(json(harness.nextInventory));
      return Promise.resolve(json(inventory));
    }
    if (url === '/api/products' || url.startsWith('/api/products?')) return Promise.resolve(json([]));
    if (url.endsWith('/api/scanner/config')) {
      return Promise.resolve(json({
        stockInBarcode: 'STOCK_IN',
        stockOutBarcode: 'STOCK_OUT',
        currentMode: harness.mode ?? 'stock_out',
        connected: false,
      }));
    }
    if (url.endsWith('/api/scanner/mode') && method === 'POST') {
      return Promise.resolve(json({ mode: 'stock_in' }));
    }
    return Promise.reject(new Error(`Unexpected request: ${method} ${url}`));
  });
  vi.stubGlobal('fetch', fetchMock);
  render(
    <MantineProvider>
      <Notifications />
      <ScanQueuePage />
    </MantineProvider>,
  );
  return { commits, batches, patches, releaseInventory, fetchMock };
};

const milk = scan({ id: 'scan-milk', barcode: '111', productId: 'prod-milk', product: product('prod-milk', 'Milk') });
const missing = scan({
  id: 'scan-missing',
  barcode: '222',
  productId: 'prod-missing',
  product: product('prod-missing', 'Missing tea'),
});
const zero = scan({
  id: 'scan-zero',
  barcode: '333',
  productId: 'prod-zero',
  product: product('prod-zero', 'Empty oats'),
});
const unresolved = scan({ id: 'scan-open', barcode: '444', productId: null, product: null });

describe('nothing on hand queue', () => {
  beforeEach(() => {
    FakeEventSource.instances = [];
    vi.stubGlobal('EventSource', FakeEventSource);
  });

  const cases: {
    name: string;
    scans: ScanEntry[];
    inventory: unknown[];
    amber: string[];
    normal: string[];
    pill: string | null;
    skip: string | null;
  }[] = [
    {
      name: 'an absent product stays confirmable and a listed zero is amber',
      scans: [milk, missing, zero, unresolved],
      inventory: [shelf('milk', 'Milk', 2), shelf('zero', 'Empty oats', 0)],
      amber: ['333'],
      normal: ['111', '222', '444'],
      pill: '1 with nothing on hand',
      skip: 'Confirm skips the 1 with nothing on hand.',
    },
    {
      name: 'one empty card uses the digit in the skip line',
      scans: [milk, zero],
      inventory: [shelf('milk', 'Milk', 2), shelf('zero', 'Empty oats', 0)],
      amber: ['333'],
      normal: ['111'],
      pill: '1 with nothing on hand',
      skip: 'Confirm skips the 1 with nothing on hand.',
    },
    {
      name: 'a stocked queue has no pill and no skip line',
      scans: [milk, unresolved],
      inventory: [shelf('milk', 'Milk', 2)],
      amber: [],
      normal: ['111', '444'],
      pill: null,
      skip: null,
    },
  ];

  it.each(cases)('$name', async (row) => {
    renderQueue({ scans: row.scans, inventory: row.inventory });
    for (const barcode of row.amber) {
      const card = await screen.findByRole('article', { name: `Scan ${barcode}, ${NOTHING_ON_HAND_TITLE}` });
      expect(card).toHaveClass('scan-entry-card--nothing');
      expect(card).not.toHaveClass('scan-entry-card--changed-animated');
      expect(within(card).getByText(NOTHING_ON_HAND_TITLE).tagName).toBe('P');
      expect(within(card).getByText(NOTHING_ON_HAND_BODY)).toBeInTheDocument();
      expect(card.querySelector('.scan-nothing-mark')?.textContent).toBe('!');
      expect(within(card).queryByRole('button', { name: 'Confirm' })).not.toBeInTheDocument();
      expect(within(card).queryByRole('button', { name: 'More' })).not.toBeInTheDocument();
      expect(within(card).getByRole('button', { name: 'Remove' })).toBeInTheDocument();
      expect(within(card).queryByRole('alert')).not.toBeInTheDocument();
      const checkbox = within(card).getByRole('checkbox');
      expect(checkbox).toBeDisabled();
      expect(checkbox).not.toBeChecked();
      expect(checkbox.getAttribute('aria-describedby')).toBe(within(card).getByText(NOTHING_ON_HAND_BODY).id);
    }
    for (const barcode of row.normal) {
      const card = await screen.findByRole('article', { name: `Scan ${barcode}` });
      expect(card).not.toHaveClass('scan-entry-card--nothing');
      expect(within(card).queryByText(NOTHING_ON_HAND_TITLE)).not.toBeInTheDocument();
      expect(within(card).getByRole('button', { name: 'Confirm' })).toBeInTheDocument();
    }
    if (row.pill === null) expect(screen.queryByText(/\d+ with nothing on hand/)).not.toBeInTheDocument();
    else expect(screen.getByText(row.pill)).toBeInTheDocument();
    expect(screen.queryByText(/Confirm skips the /)).not.toBeInTheDocument();
    if (row.skip !== null && row.normal.length > 0) {
      const chosen = screen.getByRole('article', { name: `Scan ${row.normal[0]}` });
      fireEvent.click(within(chosen).getByRole('checkbox'));
      expect(screen.getByText(row.skip)).toBeInTheDocument();
    }
  });

  it('select all checks only the eligible cards and can confirm them', async () => {
    const { batches } = renderQueue({
      scans: [milk, missing, zero],
      inventory: [shelf('milk', 'Milk', 2), shelf('zero', 'Empty oats', 0)],
    });
    await screen.findByRole('article', { name: `Scan 333, ${NOTHING_ON_HAND_TITLE}` });
    const selectAll = screen.getByRole('checkbox', { name: 'Select all eligible scans for batch approval' });
    expect(selectAll).not.toBeChecked();
    expect(selectAll).toBeEnabled();
    fireEvent.click(selectAll);
    expect(selectAll).toBeChecked();
    expect(within(screen.getByRole('article', { name: 'Scan 111' })).getByRole('checkbox')).toBeChecked();
    expect(within(screen.getByRole('article', { name: 'Scan 222' })).getByRole('checkbox')).toBeChecked();
    expect(within(screen.getByRole('article', { name: cardName('333') })).getByRole('checkbox')).not.toBeChecked();
    expect(within(screen.getByRole('article', { name: cardName('333') })).getByRole('checkbox')).toBeDisabled();
    expect(screen.getByText('Confirm skips the 1 with nothing on hand.')).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: 'Confirm 2 selected scans' }));
    await waitFor(() => expect(batches).toEqual([['scan-milk', 'scan-missing']]));
  });

  it('disables confirm all when every card has nothing on hand', async () => {
    const second = scan({
      id: 'scan-zero-2',
      barcode: '334',
      productId: 'prod-zero-2',
      product: product('prod-zero-2', 'Empty rice'),
    });
    renderQueue({
      scans: [zero, second],
      inventory: [shelf('zero', 'Empty oats', 0), shelf('zero-2', 'Empty rice', 0)],
    });
    await screen.findByRole('article', { name: `Scan 333, ${NOTHING_ON_HAND_TITLE}` });
    expect(screen.getByRole('checkbox', { name: 'Select all eligible scans for batch approval' })).toBeDisabled();
    expect(screen.getByRole('button', { name: 'Confirm 0 selected scans' })).toBeDisabled();
  });

  it('an absent product shows the link offer, not an amber card', async () => {
    renderQueue({ scans: [missing], inventory: [] });
    const card = await screen.findByRole('article', { name: 'Scan 222' });
    expect(within(card).queryByText(NOTHING_ON_HAND_TITLE)).not.toBeInTheDocument();
    fireEvent.click(within(card).getByRole('button', { name: 'Confirm' }));
    expect(await within(card).findByRole('region', { name: 'Which item is this?' })).toBeInTheDocument();
    expect(within(card).queryByText(NOTHING_ON_HAND_TITLE)).not.toBeInTheDocument();
  });

  it('removes an empty card', async () => {
    const { patches } = renderQueue({
      scans: [zero],
      inventory: [shelf('zero', 'Empty oats', 0)],
    });
    const card = await screen.findByRole('article', { name: `Scan 333, ${NOTHING_ON_HAND_TITLE}` });
    fireEvent.click(within(card).getByRole('button', { name: 'Remove' }));
    await waitFor(() => expect(patches).toEqual([{ id: 'scan-zero', body: { status: 'cancelled' } }]));
    await waitFor(() => expect(screen.queryByRole('article', { name: cardName('333') })).not.toBeInTheDocument());
  });

  it('waits for the inventory list before turning a card amber', async () => {
    const { releaseInventory } = renderQueue({
      scans: [zero],
      holdInventory: true,
    });
    const card = await screen.findByRole('article', { name: 'Scan 333' });
    expect(within(card).getByRole('button', { name: 'Confirm' })).toBeInTheDocument();
    expect(screen.queryByText(NOTHING_ON_HAND_TITLE)).not.toBeInTheDocument();
    releaseInventory([shelf('zero', 'Empty oats', 0)]);
    expect(await screen.findByRole('article', { name: `Scan 333, ${NOTHING_ON_HAND_TITLE}` })).toBeInTheDocument();
  });

  it('leaves cards alone when the inventory list fails', async () => {
    renderQueue({ scans: [missing], inventory: [], failInventory: true });
    const card = await screen.findByRole('article', { name: 'Scan 222' });
    expect(within(card).getByRole('button', { name: 'Confirm' })).toBeInTheDocument();
    expect(screen.queryByText(NOTHING_ON_HAND_TITLE)).not.toBeInTheDocument();
  });

  it('a stale none_on_hand result stays amber until the count changes', async () => {
    const stale = scan({ id: 'scan-stale', barcode: '555' });
    renderQueue({ scans: [stale], inventory: [shelf('milk', 'Milk', 2)] });
    const card = await screen.findByRole('article', { name: 'Scan 555' });
    fireEvent.click(within(card).getByRole('button', { name: 'Confirm' }));
    const amber = await screen.findByRole('article', { name: `Scan 555, ${NOTHING_ON_HAND_TITLE}` });
    expect(amber).toHaveFocus();
    expect(within(amber).getByText(NOTHING_ON_HAND_TITLE)).toHaveAttribute('role', 'status');
    expect(within(amber).queryByRole('alert')).not.toBeInTheDocument();
    expect(document.querySelector('.restock-toast')).toBeNull();
    expect(within(amber).getByRole('button', { name: 'Increase unit count' })).toBeDisabled();
    expect(within(amber).getByRole('button', { name: 'Decrease unit count' })).toBeDisabled();
    expect(within(amber).getByRole('textbox', { name: 'Unit count' })).toBeDisabled();
    FakeEventSource.instances[0]?.dispatch('inventory', shelf('milk', 'Milk', 4));
    await waitFor(() => expect(within(screen.getByRole('article', { name: 'Scan 555' })).getByRole('button', { name: 'Confirm' })).toBeInTheDocument());
    expect(screen.queryByText(NOTHING_ON_HAND_TITLE)).not.toBeInTheDocument();
  });

  it('a live stock-in clears a pinned card while the list still shows the old count', async () => {
    const pinned = scan({ id: 'scan-pin', barcode: '666' });
    const inbound = scan({
      id: 'scan-add',
      barcode: '777',
      direction: 'stock_in',
      productId: 'prod-milk',
      product: product('prod-milk', 'Milk'),
    });
    renderQueue({ scans: [pinned, inbound], inventory: [shelf('milk', 'Milk', 2)] });
    const card = await screen.findByRole('article', { name: 'Scan 666' });
    fireEvent.click(within(card).getByRole('button', { name: 'Confirm' }));
    await screen.findByRole('article', { name: `Scan 666, ${NOTHING_ON_HAND_TITLE}` });
    fireEvent.click(screen.getByRole('tab', { name: /Stock in/ }));
    const stockIn = await screen.findByRole('article', { name: 'Scan 777' });
    fireEvent.click(within(stockIn).getByRole('button', { name: 'Confirm' }));
    fireEvent.click(screen.getByRole('tab', { name: /Stock out/ }));
    await waitFor(() => expect(within(screen.getByRole('article', { name: 'Scan 666' })).getByRole('button', { name: 'Confirm' })).toBeInTheDocument());
  });

  it('stocking in clears a zero card once the list shows some on hand', async () => {
    const empty = scan({
      id: 'scan-zero',
      barcode: '333',
      productId: 'prod-milk',
      product: product('prod-milk', 'Milk'),
    });
    const inbound = scan({
      id: 'scan-in',
      barcode: '777',
      direction: 'stock_in',
      productId: 'prod-milk',
      product: product('prod-milk', 'Milk'),
    });
    renderQueue({
      scans: [empty, inbound],
      inventory: [shelf('milk', 'Milk', 0)],
      mode: 'stock_in',
    });
    const stockIn = await screen.findByRole('article', { name: 'Scan 777' });
    fireEvent.click(within(stockIn).getByRole('button', { name: 'Confirm' }));
    fireEvent.click(await screen.findByRole('tab', { name: /Stock out/ }));
    await waitFor(() => expect(within(screen.getByRole('article', { name: 'Scan 333' })).getByRole('button', { name: 'Confirm' })).toBeInTheDocument());
    expect(screen.queryByText(NOTHING_ON_HAND_TITLE)).not.toBeInTheDocument();
  });

  it('drops a pin when a later list reports a different count', async () => {
    const stale = scan({ id: 'scan-stale', barcode: '555' });
    const beans = scan({
      id: 'scan-beans',
      barcode: '888',
      productId: 'prod-beans',
      product: product('prod-beans', 'Beans'),
    });
    renderQueue({
      scans: [stale, beans],
      inventory: [shelf('milk', 'Milk', 2), shelf('beans', 'Beans', 3)],
      nextInventory: [shelf('milk', 'Milk', 5), shelf('beans', 'Beans', 3)],
    });
    fireEvent.click(within(await screen.findByRole('article', { name: 'Scan 555' })).getByRole('button', { name: 'Confirm' }));
    await screen.findByRole('article', { name: `Scan 555, ${NOTHING_ON_HAND_TITLE}` });
    fireEvent.click(within(screen.getByRole('article', { name: 'Scan 888' })).getByRole('button', { name: 'Increase unit count' }));
    await waitFor(() => expect(within(screen.getByRole('article', { name: 'Scan 555' })).getByRole('button', { name: 'Confirm' })).toBeInTheDocument());
    expect(screen.queryByText(NOTHING_ON_HAND_TITLE)).not.toBeInTheDocument();
  });

  it('keeps a live inventory update when an older list arrives later', async () => {
    const empty = scan({
      id: 'scan-zero',
      barcode: '333',
      productId: 'prod-milk',
      product: product('prod-milk', 'Milk'),
    });
    const { releaseInventory } = renderQueue({ scans: [empty], holdInventory: true });
    const card = await screen.findByRole('article', { name: 'Scan 333' });
    expect(within(card).getByRole('button', { name: 'Confirm' })).toBeInTheDocument();
    FakeEventSource.instances[0]?.dispatch('inventory', shelf('milk', 'Milk', 4));
    releaseInventory([shelf('milk', 'Milk', 0)]);
    await waitFor(() => expect(within(screen.getByRole('article', { name: 'Scan 333' })).getByRole('button', { name: 'Confirm' })).toBeInTheDocument());
    expect(screen.queryByText(NOTHING_ON_HAND_TITLE)).not.toBeInTheDocument();
  });
});
