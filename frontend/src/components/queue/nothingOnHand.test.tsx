import { beforeEach, describe, expect, it, vi } from 'vitest';
import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { MantineProvider } from '@mantine/core';
import { notifications, Notifications } from '@mantine/notifications';
import { BATCH_FAILURE_FALLBACK, BUSY_SENTENCE, NOTHING_ON_HAND_BODY, NOTHING_ON_HAND_TITLE, NO_SPACE_SENTENCE, OFFLINE_SENTENCE } from '../../api/contract';
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
  batchResult?: Record<string, unknown>;
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
  let offlineCommits = 0;
  const batches: string[][] = [];
  const patches: { id: string; body: unknown }[] = [];
  const fetchMock = vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
    const url = String(input);
    const method = init?.method ?? 'GET';
    if (url.includes('batch-commit') && method === 'POST') {
      const body = JSON.parse(String(init?.body ?? '{}')) as { scanEntryIds?: string[] };
      batches.push(body.scanEntryIds ?? []);
      const result = harness.batchResult ?? {
        updatedCount: body.scanEntryIds?.length ?? 0,
        committedIds: body.scanEntryIds ?? [],
      };
      const committed = Array.isArray(result.committedIds)
        ? result.committedIds.filter((id): id is string => typeof id === 'string')
        : [];
      if (harness.batchResult !== undefined && committed.length > 0) {
        pending = pending.filter((entry) => !committed.includes(entry.id));
      }
      return Promise.resolve(json(result));
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
      if (id === 'scan-busy-fail' || id === 'scan-busy-first') {
        return Promise.resolve(json({ error: BUSY_SENTENCE, code: 'busy' }, 503));
      }
      if (id === 'scan-nospace-fail' || id === 'scan-nospace-first') {
        return Promise.resolve(json({ error: NO_SPACE_SENTENCE, code: 'no_space' }, 507));
      }
      if (id === 'scan-link-ok' || id === 'scan-link-fail') {
        const body = JSON.parse(String(init?.body ?? '{}')) as { linkProductId?: string };
        if (typeof body.linkProductId === 'string' && body.linkProductId !== '') {
          if (id === 'scan-link-fail') {
            return Promise.resolve(json({ error: NO_SPACE_SENTENCE, code: 'no_space' }, 507));
          }
          pending = pending.filter((entry) => entry.id !== id);
          return Promise.resolve(json({ status: 'committed' }));
        }
        return Promise.resolve(json({
          error: 'Which item is this?',
          code: 'no_item',
          suggestions: [{ itemId: 'item-pears', productId: 'prod-pears', name: 'Sliced Pears', onHand: 3 }],
        }, 409));
      }
      if (id === 'scan-offline') {
        offlineCommits += 1;
        if (offlineCommits === 1) return Promise.reject(new TypeError('Failed to fetch'));
        pending = pending.filter((entry) => entry.id !== id);
        return Promise.resolve(json({ status: 'committed' }));
      }
      pending = pending.filter((entry) => entry.id !== id);
      return Promise.resolve(json({ status: 'committed' }));
    }
    if (method === 'PATCH' && /\/api\/scans\/[^/]+$/.test(url)) {
      const id = url.split('/').at(-1) ?? '';
      const body = JSON.parse(String(init?.body ?? '{}')) as { status?: string; unitCount?: number };
      patches.push({ id, body });
      if (id === 'scan-busy-remove') {
        return Promise.resolve(json({ error: BUSY_SENTENCE, code: 'busy' }, 503));
      }
      if (id === 'scan-nospace-remove') {
        return Promise.resolve(json({ error: NO_SPACE_SENTENCE, code: 'no_space' }, 507));
      }
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
    notifications.clean();
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

  it('merges a live inventory update into the list that was already loading', async () => {
    const rice = scan({
      id: 'scan-rice',
      barcode: '666',
      productId: 'prod-rice',
      product: product('prod-rice', 'Rice'),
    });
    const { releaseInventory } = renderQueue({ scans: [milk, rice], holdInventory: true });
    expect(within(await screen.findByRole('article', { name: 'Scan 111' })).getByRole('button', { name: 'Confirm' })).toBeInTheDocument();
    expect(within(screen.getByRole('article', { name: 'Scan 666' })).getByRole('button', { name: 'Confirm' })).toBeInTheDocument();
    FakeEventSource.instances[0]?.dispatch('inventory', shelf('milk', 'Milk', 4));
    releaseInventory([shelf('milk', 'Milk', 0), shelf('rice', 'Rice', 0)]);
    const riceCard = await screen.findByRole('article', { name: `Scan 666, ${NOTHING_ON_HAND_TITLE}` });
    expect(within(riceCard).getByText(NOTHING_ON_HAND_TITLE)).toBeInTheDocument();
    expect(within(screen.getByRole('article', { name: 'Scan 111' })).getByRole('button', { name: 'Confirm' })).toBeInTheDocument();
    expect(screen.queryByRole('article', { name: `Scan 111, ${NOTHING_ON_HAND_TITLE}` })).not.toBeInTheDocument();
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

  const expectNoBatchBanner = () => {
    expect(document.querySelector('.scan-batch-banner')).toBeNull();
    expect(screen.queryByText('Some scans could not be confirmed.')).not.toBeInTheDocument();
  };

  const confirmBatch = async (count: number) => {
    fireEvent.click(screen.getByRole('checkbox', { name: 'Select all eligible scans for batch approval' }));
    fireEvent.click(screen.getByRole('button', { name: `Confirm ${count} selected scans` }));
  };

  it('An amber-only batch with no partial line shows no banner.', async () => {
    const oats = scan({
      id: 'scan-oats',
      barcode: '333',
      productId: 'prod-oats',
      product: product('prod-oats', 'Oats'),
    });
    const rice = scan({
      id: 'scan-rice',
      barcode: '666',
      productId: 'prod-rice',
      product: product('prod-rice', 'Rice'),
    });
    renderQueue({
      scans: [oats, rice],
      inventory: [shelf('oats', 'Oats', 2), shelf('rice', 'Rice', 2)],
      batchResult: {
        updatedCount: 1,
        committedIds: ['scan-oats'],
        failedIds: ['scan-rice'],
        failedReasons: ['Nothing on hand to take out.'],
        failedCodes: ['none_on_hand'],
      },
    });
    expect(await screen.findByRole('article', { name: 'Scan 333' })).toBeInTheDocument();
    await confirmBatch(2);
    const riceCard = await screen.findByRole('article', { name: `Scan 666, ${NOTHING_ON_HAND_TITLE}` });
    expect(within(riceCard).getByText(NOTHING_ON_HAND_TITLE)).toBeInTheDocument();
    expect(screen.queryByRole('article', { name: 'Scan 333' })).not.toBeInTheDocument();
    expectNoBatchBanner();
  });

  it('After a stock-in clears amber from the last none_on_hand card, the banner is gone.', async () => {
    const rice = scan({
      id: 'scan-rice',
      barcode: '666',
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
      scans: [rice, inbound],
      inventory: [shelf('milk', 'Milk', 2)],
      batchResult: {
        updatedCount: 0,
        committedIds: [],
        failedIds: ['scan-rice'],
        failedReasons: ['Nothing on hand to take out.'],
        failedCodes: ['none_on_hand'],
      },
    });
    expect(await screen.findByRole('article', { name: 'Scan 666' })).toBeInTheDocument();
    await confirmBatch(1);
    expect(await screen.findByRole('article', { name: `Scan 666, ${NOTHING_ON_HAND_TITLE}` })).toBeInTheDocument();
    fireEvent.click(screen.getByRole('tab', { name: /Stock in/ }));
    fireEvent.click(within(await screen.findByRole('article', { name: 'Scan 777' })).getByRole('button', { name: 'Confirm' }));
    fireEvent.click(await screen.findByRole('tab', { name: /Stock out/ }));
    await waitFor(() => expect(within(screen.getByRole('article', { name: 'Scan 666' })).getByRole('button', { name: 'Confirm' })).toBeInTheDocument());
    expect(screen.queryByText(NOTHING_ON_HAND_TITLE)).not.toBeInTheDocument();
    expectNoBatchBanner();
  });

  it('A mixed batch stays red until its non-amber failure is resolved.', async () => {
    const rice = scan({
      id: 'scan-rice',
      barcode: '666',
      productId: 'prod-rice',
      product: product('prod-rice', 'Rice'),
    });
    const open = scan({ id: 'scan-open', barcode: '444', productId: null, product: null });
    renderQueue({
      scans: [rice, open],
      inventory: [shelf('rice', 'Rice', 2)],
      batchResult: {
        updatedCount: 0,
        committedIds: [],
        failedIds: ['scan-rice', 'scan-open'],
        failedReasons: ['Nothing on hand to take out.', 'Choose which item this is first.'],
        failedCodes: ['none_on_hand', 'product_unresolved'],
      },
    });
    expect(await screen.findByRole('article', { name: 'Scan 666' })).toBeInTheDocument();
    await confirmBatch(2);
    await waitFor(() => {
      const failure = document.querySelector('.scan-batch-banner');
      expect(failure?.textContent?.replace(/\s+/g, ' ').trim()).toBe('Some scans could not be confirmed.');
      expect(failure?.className).not.toContain('scan-batch-banner--success');
    });
    expect(screen.getAllByText('Some scans could not be confirmed.')).toHaveLength(1);
    expect(screen.getByRole('article', { name: `Scan 666, ${NOTHING_ON_HAND_TITLE}` })).toBeInTheDocument();
    expect(within(screen.getByRole('article', { name: 'Scan 444' })).getByText('Choose which item this is first.')).toBeInTheDocument();
    fireEvent.click(within(screen.getByRole('article', { name: 'Scan 444' })).getByRole('button', { name: 'Confirm' }));
    await waitFor(() => expect(screen.queryByRole('article', { name: 'Scan 444' })).not.toBeInTheDocument());
    expect(screen.getByRole('article', { name: `Scan 666, ${NOTHING_ON_HAND_TITLE}` })).toBeInTheDocument();
    expectNoBatchBanner();
  });

  it('reconciles a pin against the list merged with live inventory', async () => {
    const pinned = scan({ id: 'scan-pin', barcode: '666' });
    const { releaseInventory } = renderQueue({ scans: [pinned], holdInventory: true });
    expect(within(await screen.findByRole('article', { name: 'Scan 666' })).getByRole('button', { name: 'Confirm' })).toBeInTheDocument();
    await act(async () => {
      FakeEventSource.instances[0]?.dispatch('inventory', shelf('milk', 'Milk', 4));
    });
    fireEvent.click(screen.getByRole('button', { name: 'Confirm' }));
    expect(await screen.findByRole('article', { name: `Scan 666, ${NOTHING_ON_HAND_TITLE}` })).toBeInTheDocument();
    await act(async () => {
      releaseInventory([shelf('milk', 'Milk', 0)]);
    });
    expect(screen.getByRole('article', { name: `Scan 666, ${NOTHING_ON_HAND_TITLE}` })).toBeInTheDocument();
    expect(within(screen.getByRole('article', { name: `Scan 666, ${NOTHING_ON_HAND_TITLE}` })).queryByRole('button', { name: 'Confirm' })).not.toBeInTheDocument();
  });

  it('clears live inventory updates when the next load starts', async () => {
    const rice = scan({
      id: 'scan-rice',
      barcode: '666',
      productId: 'prod-rice',
      product: product('prod-rice', 'Rice'),
    });
    const { patches } = renderQueue({
      scans: [milk, rice],
      holdInventory: true,
      nextInventory: [shelf('milk', 'Milk', 4)],
    });
    expect(within(await screen.findByRole('article', { name: 'Scan 111' })).getByRole('button', { name: 'Confirm' })).toBeInTheDocument();
    await act(async () => {
      FakeEventSource.instances[0]?.dispatch('inventory', shelf('rice', 'Rice', 0));
    });
    expect(await screen.findByRole('article', { name: `Scan 666, ${NOTHING_ON_HAND_TITLE}` })).toBeInTheDocument();
    fireEvent.click(within(screen.getByRole('article', { name: 'Scan 111' })).getByRole('button', { name: 'Increase unit count' }));
    await waitFor(() => expect(patches.some((patch) => patch.id === 'scan-milk')).toBe(true));
    await waitFor(() => expect(screen.queryByRole('article', { name: `Scan 666, ${NOTHING_ON_HAND_TITLE}` })).not.toBeInTheDocument());
    expect(within(screen.getByRole('article', { name: 'Scan 666' })).getByRole('button', { name: 'Confirm' })).toBeInTheDocument();
  });

  it('discards an inventory list that lost the race with a newer load', async () => {
    const { releaseInventory, patches, fetchMock } = renderQueue({
      scans: [milk],
      holdInventory: true,
      nextInventory: [shelf('milk', 'Milk', 4)],
    });
    expect(within(await screen.findByRole('article', { name: 'Scan 111' })).getByRole('button', { name: 'Confirm' })).toBeInTheDocument();
    const inventoryCalls = () => fetchMock.mock.calls.filter((call) => String(call[0]).includes('/api/inventory')).length;
    const before = inventoryCalls();
    fireEvent.click(within(screen.getByRole('article', { name: 'Scan 111' })).getByRole('button', { name: 'Increase unit count' }));
    await waitFor(() => expect(patches.some((patch) => patch.id === 'scan-milk')).toBe(true));
    await waitFor(() => expect(inventoryCalls()).toBeGreaterThan(before));
    await act(async () => {
      releaseInventory([shelf('milk', 'Milk', 0)]);
    });
    expect(within(screen.getByRole('article', { name: 'Scan 111' })).getByRole('button', { name: 'Confirm' })).toBeInTheDocument();
    expect(screen.queryByText(NOTHING_ON_HAND_TITLE)).not.toBeInTheDocument();
  });

  it.each([
    { name: 'busy and nothing committed', code: 'busy', message: BUSY_SENTENCE, committed: 0, banner: BUSY_SENTENCE, onCard: false },
    { name: 'busy and one committed', code: 'busy', message: BUSY_SENTENCE, committed: 1, banner: `1 confirmed. ${BUSY_SENTENCE}`, onCard: false },
    { name: 'busy and many committed', code: 'busy', message: BUSY_SENTENCE, committed: 2, banner: `2 confirmed. ${BUSY_SENTENCE}`, onCard: false },
    { name: 'no_space and nothing committed', code: 'no_space', message: NO_SPACE_SENTENCE, committed: 0, banner: NO_SPACE_SENTENCE, onCard: false },
    { name: 'no_space and one committed', code: 'no_space', message: NO_SPACE_SENTENCE, committed: 1, banner: `1 confirmed. ${NO_SPACE_SENTENCE}`, onCard: false },
    { name: 'no_space and many committed', code: 'no_space', message: NO_SPACE_SENTENCE, committed: 3, banner: `3 confirmed. ${NO_SPACE_SENTENCE}`, onCard: false },
    { name: 'instance_gone keeps the card sentence', code: 'instance_gone', message: 'That one was already taken out. Pick another.', committed: 1, banner: BATCH_FAILURE_FALLBACK, onCard: true },
  ])('$name', async ({ code, message, committed, banner, onCard }) => {
    const failed = scan({
      id: 'scan-fail',
      barcode: '444',
      productId: 'prod-fail',
      product: product('prod-fail', 'Short loaf'),
    });
    const committedScans = Array.from({ length: committed }, (_, index) => scan({
      id: `scan-ok-${index}`,
      barcode: `80${index}`,
      productId: `prod-ok-${index}`,
      product: product(`prod-ok-${index}`, `Kept ${index}`),
    }));
    const scans = [...committedScans, failed];
    renderQueue({
      scans,
      inventory: scans.map((entry, index) => shelf(`row-${index}`, entry.product?.name ?? 'Item', 2)),
      batchResult: {
        updatedCount: scans.length,
        committedIds: committedScans.map((entry) => entry.id),
        failedIds: [failed.id],
        failedReasons: [message],
        failedCodes: [code],
      },
    });
    expect(await screen.findByRole('article', { name: 'Scan 444' })).toBeInTheDocument();
    fireEvent.click(screen.getByRole('checkbox', { name: 'Select all eligible scans for batch approval' }));
    fireEvent.click(screen.getByRole('button', { name: `Confirm ${scans.length} selected scans` }));
    await waitFor(() => {
      expect(document.querySelector('.scan-batch-banner')?.textContent?.replace(/\s+/g, ' ').trim()).toBe(banner);
    });
    const card = screen.getByRole('article', { name: 'Scan 444' });
    if (onCard) {
      expect(within(card).getByText(message)).toBeInTheDocument();
    } else {
      expect(within(card).queryByText(message)).not.toBeInTheDocument();
    }
  });

  it.each([
    { name: 'busy empty message', code: 'busy', message: '', committed: 0, banner: BUSY_SENTENCE },
    { name: 'busy blank message', code: 'busy', message: '   ', committed: 0, banner: BUSY_SENTENCE },
    { name: 'busy empty message and one committed', code: 'busy', message: '', committed: 1, banner: `1 confirmed. ${BUSY_SENTENCE}` },
    { name: 'no_space empty message', code: 'no_space', message: '', committed: 0, banner: NO_SPACE_SENTENCE },
    { name: 'no_space blank message', code: 'no_space', message: ' \n ', committed: 0, banner: NO_SPACE_SENTENCE },
    { name: 'no_space blank message and many committed', code: 'no_space', message: '  ', committed: 3, banner: `3 confirmed. ${NO_SPACE_SENTENCE}` },
  ])('$name fills the banner from the storage sentence', async ({ code, message, committed, banner }) => {
    const failed = scan({
      id: 'scan-fail',
      barcode: '444',
      productId: 'prod-fail',
      product: product('prod-fail', 'Short loaf'),
    });
    const committedScans = Array.from({ length: committed }, (_, index) => scan({
      id: `scan-ok-${index}`,
      barcode: `80${index}`,
      productId: `prod-ok-${index}`,
      product: product(`prod-ok-${index}`, `Kept ${index}`),
    }));
    const scans = [...committedScans, failed];
    renderQueue({
      scans,
      inventory: scans.map((entry, index) => shelf(`row-${index}`, entry.product?.name ?? 'Item', 2)),
      batchResult: {
        updatedCount: scans.length,
        committedIds: committedScans.map((entry) => entry.id),
        failedIds: [failed.id],
        failedReasons: [message],
        failedCodes: [code],
      },
    });
    expect(await screen.findByRole('article', { name: 'Scan 444' })).toBeInTheDocument();
    fireEvent.click(screen.getByRole('checkbox', { name: 'Select all eligible scans for batch approval' }));
    fireEvent.click(screen.getByRole('button', { name: `Confirm ${scans.length} selected scans` }));
    const bannerNode = await screen.findByText(banner);
    expect(bannerNode.closest('.scan-batch-banner')).not.toBeNull();
    expect(within(screen.getByRole('article', { name: 'Scan 444' })).queryByText(banner)).not.toBeInTheDocument();
  });

  it('a busy card beside amber keeps one red sentence', async () => {
    const amber = scan({
      id: 'scan-amber',
      barcode: '555',
      productId: 'prod-amber',
      product: product('prod-amber', 'Rice'),
    });
    const busy = scan({
      id: 'scan-busy',
      barcode: '666',
      productId: 'prod-busy',
      product: product('prod-busy', 'Oats'),
    });
    renderQueue({
      scans: [amber, busy],
      inventory: [shelf('amber', 'Rice', 2), shelf('busy', 'Oats', 2)],
      batchResult: {
        updatedCount: 0,
        committedIds: [],
        failedIds: [amber.id, busy.id],
        failedReasons: [NOTHING_ON_HAND_TITLE, BUSY_SENTENCE],
        failedCodes: ['none_on_hand', 'busy'],
      },
    });
    expect(await screen.findByRole('article', { name: 'Scan 555' })).toBeInTheDocument();
    fireEvent.click(screen.getByRole('checkbox', { name: 'Select all eligible scans for batch approval' }));
    fireEvent.click(screen.getByRole('button', { name: 'Confirm 2 selected scans' }));
    const banner = await screen.findByText(BUSY_SENTENCE);
    expect(banner.closest('.scan-batch-banner')).not.toBeNull();
    expect(banner.closest('.scan-batch-banner')?.className).not.toContain('scan-batch-banner--success');
    expect(screen.getByRole('article', { name: `Scan 555, ${NOTHING_ON_HAND_TITLE}` })).toBeInTheDocument();
    expect(within(screen.getByRole('article', { name: 'Scan 666' })).queryByText(BUSY_SENTENCE)).not.toBeInTheDocument();
    expect(screen.queryByText(BATCH_FAILURE_FALLBACK)).not.toBeInTheDocument();
  });

  it('a nothing-on-hand confirm keeps the busy banner', async () => {
    const oats = scan({
      id: 'scan-pin',
      barcode: '555',
      productId: 'prod-oats',
      product: product('prod-oats', 'Oats'),
    });
    const rice = scan({
      id: 'scan-rice',
      barcode: '666',
      productId: 'prod-rice',
      product: product('prod-rice', 'Rice'),
    });
    renderQueue({
      scans: [oats, rice],
      inventory: [shelf('oats', 'Oats', 2), shelf('rice', 'Rice', 2)],
      batchResult: {
        updatedCount: 0,
        committedIds: [],
        failedIds: [oats.id, rice.id],
        failedReasons: [BUSY_SENTENCE, BUSY_SENTENCE],
        failedCodes: ['busy', 'busy'],
      },
    });
    expect(await screen.findByRole('article', { name: 'Scan 555' })).toBeInTheDocument();
    fireEvent.click(screen.getByRole('checkbox', { name: 'Select all eligible scans for batch approval' }));
    fireEvent.click(screen.getByRole('button', { name: 'Confirm 2 selected scans' }));
    const banner = await screen.findByText('Stockbot is busy right now. Try again in a moment.');
    expect(banner.closest('.scan-batch-banner')).not.toBeNull();
    expect(within(screen.getByRole('article', { name: 'Scan 555' })).queryByText('Stockbot is busy right now. Try again in a moment.')).not.toBeInTheDocument();
    fireEvent.click(within(screen.getByRole('article', { name: 'Scan 555' })).getByRole('button', { name: 'Confirm' }));
    const card = await screen.findByRole('article', { name: 'Scan 555, Nothing on hand to take out.' });
    expect(within(card).getByText('Nothing on hand to take out.')).toBeInTheDocument();
    expect(within(card).getByText("Stockbot thinks you have none, so this can't be confirmed yet. Stock it in first, or remove this scan.")).toBeInTheDocument();
    expect(document.querySelector('.scan-batch-banner')?.textContent).toContain('Stockbot is busy right now. Try again in a moment.');
    expect(screen.getByRole('article', { name: 'Scan 666' })).toBeInTheDocument();
  });

  it('a single confirm clears a no_space notice', async () => {
    const oats = scan({
      id: 'scan-oats',
      barcode: '555',
      productId: 'prod-oats',
      product: product('prod-oats', 'Oats'),
    });
    const rice = scan({
      id: 'scan-rice',
      barcode: '666',
      productId: 'prod-rice',
      product: product('prod-rice', 'Rice'),
    });
    renderQueue({
      scans: [oats, rice],
      inventory: [shelf('oats', 'Oats', 2), shelf('rice', 'Rice', 2)],
      batchResult: {
        updatedCount: 0,
        committedIds: [],
        failedIds: [oats.id, rice.id],
        failedReasons: [NO_SPACE_SENTENCE, NO_SPACE_SENTENCE],
        failedCodes: ['no_space', 'no_space'],
      },
    });
    expect(await screen.findByRole('article', { name: 'Scan 555' })).toBeInTheDocument();
    fireEvent.click(screen.getByRole('checkbox', { name: 'Select all eligible scans for batch approval' }));
    fireEvent.click(screen.getByRole('button', { name: 'Confirm 2 selected scans' }));
    const banner = await screen.findByText("Stockbot is out of storage space, so this wasn't saved.");
    expect(banner.closest('.scan-batch-banner')).not.toBeNull();
    fireEvent.click(within(screen.getByRole('article', { name: 'Scan 555' })).getByRole('button', { name: 'Confirm' }));
    await waitFor(() => expect(screen.queryByRole('article', { name: 'Scan 555' })).not.toBeInTheDocument());
    expect(document.querySelector('.scan-batch-banner')).toBeNull();
    expect(screen.queryByText("Stockbot is out of storage space, so this wasn't saved.")).not.toBeInTheDocument();
    expect(screen.getByRole('article', { name: 'Scan 666' })).toBeInTheDocument();
  });

  it('a busy confirm keeps the banner', async () => {
    const oats = scan({
      id: 'scan-busy-fail',
      barcode: '555',
      productId: 'prod-oats',
      product: product('prod-oats', 'Oats'),
    });
    const rice = scan({
      id: 'scan-rice',
      barcode: '666',
      productId: 'prod-rice',
      product: product('prod-rice', 'Rice'),
    });
    const { commits } = renderQueue({
      scans: [oats, rice],
      inventory: [shelf('oats', 'Oats', 2), shelf('rice', 'Rice', 2)],
      batchResult: {
        updatedCount: 0,
        committedIds: [],
        failedIds: [oats.id, rice.id],
        failedReasons: [BUSY_SENTENCE, BUSY_SENTENCE],
        failedCodes: ['busy', 'busy'],
      },
    });
    expect(await screen.findByRole('article', { name: 'Scan 555' })).toBeInTheDocument();
    fireEvent.click(screen.getByRole('checkbox', { name: 'Select all eligible scans for batch approval' }));
    fireEvent.click(screen.getByRole('button', { name: 'Confirm 2 selected scans' }));
    expect((await screen.findByText(BUSY_SENTENCE)).closest('.scan-batch-banner')).not.toBeNull();
    fireEvent.click(within(screen.getByRole('article', { name: 'Scan 555' })).getByRole('button', { name: 'Confirm' }));
    await waitFor(() => expect(commits).toContain('scan-busy-fail'));
    expect(document.querySelector('.restock-toast')).toBeNull();
    expect(screen.getAllByText(BUSY_SENTENCE)).toHaveLength(1);
    expect(document.querySelector('.scan-batch-banner')?.textContent).toContain(BUSY_SENTENCE);
    expect(screen.getByRole('article', { name: 'Scan 555' })).toBeInTheDocument();
    expect(screen.getByRole('article', { name: 'Scan 666' })).toBeInTheDocument();
  });

  it('a no_space confirm keeps the banner', async () => {
    const oats = scan({
      id: 'scan-nospace-fail',
      barcode: '555',
      productId: 'prod-oats',
      product: product('prod-oats', 'Oats'),
    });
    const rice = scan({
      id: 'scan-rice',
      barcode: '666',
      productId: 'prod-rice',
      product: product('prod-rice', 'Rice'),
    });
    const { commits } = renderQueue({
      scans: [oats, rice],
      inventory: [shelf('oats', 'Oats', 2), shelf('rice', 'Rice', 2)],
      batchResult: {
        updatedCount: 0,
        committedIds: [],
        failedIds: [oats.id, rice.id],
        failedReasons: [NO_SPACE_SENTENCE, NO_SPACE_SENTENCE],
        failedCodes: ['no_space', 'no_space'],
      },
    });
    expect(await screen.findByRole('article', { name: 'Scan 555' })).toBeInTheDocument();
    fireEvent.click(screen.getByRole('checkbox', { name: 'Select all eligible scans for batch approval' }));
    fireEvent.click(screen.getByRole('button', { name: 'Confirm 2 selected scans' }));
    expect((await screen.findByText(NO_SPACE_SENTENCE)).closest('.scan-batch-banner')).not.toBeNull();
    fireEvent.click(within(screen.getByRole('article', { name: 'Scan 555' })).getByRole('button', { name: 'Confirm' }));
    await waitFor(() => expect(commits).toContain('scan-nospace-fail'));
    expect(document.querySelector('.restock-toast')).toBeNull();
    expect(screen.getAllByText(NO_SPACE_SENTENCE)).toHaveLength(1);
    expect(document.querySelector('.scan-batch-banner')?.textContent).toContain(NO_SPACE_SENTENCE);
    expect(screen.getByRole('article', { name: 'Scan 555' })).toBeInTheDocument();
    expect(screen.getByRole('article', { name: 'Scan 666' })).toBeInTheDocument();
  });

  it('a network error keeps the banner', async () => {
    const oats = scan({
      id: 'scan-offline',
      barcode: '555',
      productId: 'prod-oats',
      product: product('prod-oats', 'Oats'),
    });
    const rice = scan({
      id: 'scan-rice',
      barcode: '666',
      productId: 'prod-rice',
      product: product('prod-rice', 'Rice'),
    });
    renderQueue({
      scans: [oats, rice],
      inventory: [shelf('oats', 'Oats', 2), shelf('rice', 'Rice', 2)],
      batchResult: {
        updatedCount: 0,
        committedIds: [],
        failedIds: [oats.id, rice.id],
        failedReasons: [BUSY_SENTENCE, BUSY_SENTENCE],
        failedCodes: ['busy', 'busy'],
      },
    });
    expect(await screen.findByRole('article', { name: 'Scan 555' })).toBeInTheDocument();
    fireEvent.click(screen.getByRole('checkbox', { name: 'Select all eligible scans for batch approval' }));
    fireEvent.click(screen.getByRole('button', { name: 'Confirm 2 selected scans' }));
    const busy = await screen.findByText(BUSY_SENTENCE);
    expect(busy.closest('.scan-batch-banner')).not.toBeNull();
    expect(busy.closest('.scan-page-queue')).not.toBeNull();
    fireEvent.click(within(screen.getByRole('article', { name: 'Scan 555' })).getByRole('button', { name: 'Confirm' }));
    const offline = await screen.findByText(OFFLINE_SENTENCE);
    expect(offline.closest('.scan-page-queue')).not.toBeNull();
    expect(screen.queryByText('Failed to fetch')).not.toBeInTheDocument();
    expect(screen.getAllByText(OFFLINE_SENTENCE)).toHaveLength(1);
    expect(screen.queryByText(BUSY_SENTENCE)).not.toBeInTheDocument();
    expect(screen.getByRole('article', { name: 'Scan 555' })).toBeInTheDocument();
    expect(screen.getByRole('article', { name: 'Scan 666' })).toBeInTheDocument();
    fireEvent.click(within(screen.getByRole('article', { name: 'Scan 555' })).getByRole('button', { name: 'Confirm' }));
    await waitFor(() => expect(screen.queryByRole('article', { name: 'Scan 555' })).not.toBeInTheDocument());
    expect(screen.queryByText(OFFLINE_SENTENCE)).not.toBeInTheDocument();
    expect(screen.queryByText(BUSY_SENTENCE)).not.toBeInTheDocument();
    expect(screen.getByRole('article', { name: 'Scan 666' })).toBeInTheDocument();
  });

  it.each([
    { name: 'a successful link clears a busy notice', id: 'scan-link-ok', code: 'busy', banner: BUSY_SENTENCE, linked: true },
    { name: 'a successful link clears a no_space notice', id: 'scan-link-ok', code: 'no_space', banner: NO_SPACE_SENTENCE, linked: true },
    { name: 'a failed link keeps a busy notice', id: 'scan-link-fail', code: 'busy', banner: BUSY_SENTENCE, linked: false },
    { name: 'a failed link keeps a no_space notice', id: 'scan-link-fail', code: 'no_space', banner: NO_SPACE_SENTENCE, linked: false },
  ])('$name', async ({ id, code, banner, linked }) => {
    const oats = scan({
      id,
      barcode: '555',
      productId: 'prod-oats',
      product: product('prod-oats', 'Oats'),
    });
    const rice = scan({
      id: 'scan-rice',
      barcode: '666',
      productId: 'prod-rice',
      product: product('prod-rice', 'Rice'),
    });
    const { commits } = renderQueue({
      scans: [oats, rice],
      inventory: [shelf('oats', 'Oats', 2), shelf('rice', 'Rice', 2)],
      batchResult: {
        updatedCount: 0,
        committedIds: [],
        failedIds: [oats.id, rice.id],
        failedReasons: [banner, banner],
        failedCodes: [code, code],
      },
    });
    expect(await screen.findByRole('article', { name: 'Scan 555' })).toBeInTheDocument();
    fireEvent.click(screen.getByRole('checkbox', { name: 'Select all eligible scans for batch approval' }));
    fireEvent.click(screen.getByRole('button', { name: 'Confirm 2 selected scans' }));
    const card = screen.getByRole('article', { name: 'Scan 555' });
    expect((await screen.findByText(banner)).closest('.scan-batch-banner')).not.toBeNull();
    fireEvent.click(within(card).getByRole('button', { name: 'Confirm' }));
    fireEvent.click(await within(card).findByRole('button', { name: 'Link Sliced Pears' }));
    if (linked) {
      await waitFor(() => expect(screen.queryByRole('article', { name: 'Scan 555' })).not.toBeInTheDocument());
      expect(document.querySelector('.scan-batch-banner')).toBeNull();
      expect(screen.queryByText(banner)).not.toBeInTheDocument();
      expect(screen.getByRole('article', { name: 'Scan 666' })).toBeInTheDocument();
      return;
    }
    await waitFor(() => expect(commits.filter((commit) => commit === id)).toHaveLength(2));
    expect(document.querySelector('.restock-toast')).toBeNull();
    expect(screen.getAllByText(banner)).toHaveLength(1);
    expect(document.querySelector('.scan-batch-banner')?.textContent).toContain(banner);
    expect(within(screen.getByRole('article', { name: 'Scan 555' })).queryByText(banner)).not.toBeInTheDocument();
    expect(screen.getByRole('article', { name: 'Scan 555' })).toBeInTheDocument();
    expect(screen.getByRole('article', { name: 'Scan 666' })).toBeInTheDocument();
  });

  it.each([
    { name: 'a first busy confirm is only the banner', id: 'scan-busy-first', sentence: BUSY_SENTENCE },
    { name: 'a first no_space confirm is only the banner', id: 'scan-nospace-first', sentence: NO_SPACE_SENTENCE },
  ])('$name', async ({ id, sentence }) => {
    const oats = scan({
      id,
      barcode: '555',
      productId: 'prod-oats',
      product: product('prod-oats', 'Oats'),
    });
    const { commits } = renderQueue({
      scans: [oats],
      inventory: [shelf('oats', 'Oats', 2)],
    });
    const card = await screen.findByRole('article', { name: 'Scan 555' });
    fireEvent.click(within(card).getByRole('button', { name: 'Confirm' }));
    await waitFor(() => expect(commits).toContain(id));
    expect(document.querySelector('.restock-toast')).toBeNull();
    expect(screen.getAllByText(sentence)).toHaveLength(1);
    expect(document.querySelector('.scan-batch-banner')?.textContent).toContain(sentence);
    expect(within(screen.getByRole('article', { name: 'Scan 555' })).queryByText(sentence)).not.toBeInTheDocument();
  });

  it.each([
    { name: 'a busy remove is only the banner', id: 'scan-busy-remove', sentence: BUSY_SENTENCE },
    { name: 'a no_space remove is only the banner', id: 'scan-nospace-remove', sentence: NO_SPACE_SENTENCE },
  ])('$name', async ({ id, sentence }) => {
    const oats = scan({
      id,
      barcode: '555',
      productId: 'prod-oats',
      product: product('prod-oats', 'Oats'),
    });
    const { patches } = renderQueue({
      scans: [oats],
      inventory: [shelf('oats', 'Oats', 2)],
    });
    const card = await screen.findByRole('article', { name: 'Scan 555' });
    fireEvent.click(within(card).getByRole('button', { name: 'Remove' }));
    await waitFor(() => expect(patches.map((patch) => patch.id)).toContain(id));
    expect(document.querySelector('.restock-toast')).toBeNull();
    expect(screen.getAllByText(sentence)).toHaveLength(1);
    expect(document.querySelector('.scan-batch-banner')?.textContent).toContain(sentence);
    expect(within(screen.getByRole('article', { name: 'Scan 555' })).queryByRole('alert')).not.toBeInTheDocument();
    expect(within(screen.getByRole('article', { name: 'Scan 555' })).queryByText(sentence)).not.toBeInTheDocument();
  });

  it.each([
    { name: 'a busy confirm after a saved batch has no confirmed prefix', id: 'scan-busy-first', sentence: BUSY_SENTENCE },
    { name: 'a no_space confirm after a saved batch has no confirmed prefix', id: 'scan-nospace-first', sentence: NO_SPACE_SENTENCE },
  ])('$name', async ({ id, sentence }) => {
    const oats = scan({
      id: 'scan-oats',
      barcode: '555',
      productId: 'prod-oats',
      product: product('prod-oats', 'Oats'),
    });
    const rice = scan({
      id: 'scan-rice',
      barcode: '666',
      productId: 'prod-rice',
      product: product('prod-rice', 'Rice'),
    });
    const later = scan({
      id,
      barcode: '777',
      productId: 'prod-later',
      product: product('prod-later', 'Later loaf'),
    });
    const { commits } = renderQueue({
      scans: [oats, rice, later],
      inventory: [shelf('oats', 'Oats', 2), shelf('rice', 'Rice', 2), shelf('later', 'Later loaf', 2)],
      batchResult: {
        updatedCount: 2,
        committedIds: [oats.id, rice.id],
      },
    });
    expect(await screen.findByRole('article', { name: 'Scan 777' })).toBeInTheDocument();
    fireEvent.click(within(screen.getByRole('article', { name: 'Scan 555' })).getByRole('checkbox', { name: 'Select scan for batch approval' }));
    fireEvent.click(within(screen.getByRole('article', { name: 'Scan 666' })).getByRole('checkbox', { name: 'Select scan for batch approval' }));
    fireEvent.click(screen.getByRole('button', { name: 'Confirm 2 selected scans' }));
    await waitFor(() => expect(screen.queryByRole('article', { name: 'Scan 555' })).not.toBeInTheDocument());
    expect(screen.queryByRole('article', { name: 'Scan 666' })).not.toBeInTheDocument();
    expect(document.querySelector('.scan-batch-banner')).toBeNull();
    fireEvent.click(within(screen.getByRole('article', { name: 'Scan 777' })).getByRole('button', { name: 'Confirm' }));
    await waitFor(() => expect(commits).toContain(id));
    expect(document.querySelector('.restock-toast')).toBeNull();
    expect(screen.queryByText('confirmed.', { exact: false })).not.toBeInTheDocument();
    expect(screen.getAllByText(sentence)).toHaveLength(1);
    expect(document.querySelector('.scan-batch-banner')?.textContent?.replace(/\s+/g, ' ').trim()).toBe(sentence);
    expect(within(screen.getByRole('article', { name: 'Scan 777' })).queryByText(sentence)).not.toBeInTheDocument();
  });

  it('a busy retry leaves the other card and the banner alone', async () => {
    const oats = scan({
      id: 'scan-oats',
      barcode: '555',
      productId: 'prod-oats',
      product: product('prod-oats', 'Oats'),
    });
    const rice = scan({
      id: 'scan-busy-fail',
      barcode: '666',
      productId: 'prod-rice',
      product: product('prod-rice', 'Rice'),
    });
    const { commits } = renderQueue({
      scans: [oats, rice],
      inventory: [shelf('oats', 'Oats', 2), shelf('rice', 'Rice', 2)],
      batchResult: {
        updatedCount: 0,
        committedIds: [],
        failedIds: [oats.id, rice.id],
        failedReasons: [BUSY_SENTENCE, 'Which item is this?'],
        failedCodes: ['busy', 'no_item'],
      },
    });
    expect(await screen.findByRole('article', { name: 'Scan 555' })).toBeInTheDocument();
    fireEvent.click(screen.getByRole('checkbox', { name: 'Select all eligible scans for batch approval' }));
    fireEvent.click(screen.getByRole('button', { name: 'Confirm 2 selected scans' }));
    const banner = 'Some scans could not be confirmed.';
    expect((await screen.findByText(banner)).closest('.scan-batch-banner')).not.toBeNull();
    expect(within(screen.getByRole('article', { name: 'Scan 666' })).getByText('Which item is this?')).toBeInTheDocument();
    fireEvent.click(within(screen.getByRole('article', { name: 'Scan 666' })).getByRole('button', { name: 'Confirm' }));
    await waitFor(() => expect(commits).toContain('scan-busy-fail'));
    expect(document.querySelector('.restock-toast')).toBeNull();
    expect(screen.queryByText(BUSY_SENTENCE)).not.toBeInTheDocument();
    expect(screen.getAllByText(banner)).toHaveLength(1);
    expect(document.querySelector('.scan-batch-banner')?.textContent?.replace(/\s+/g, ' ').trim()).toBe(banner);
    expect(within(screen.getByRole('article', { name: 'Scan 666' })).getByText('Which item is this?')).toBeInTheDocument();
    expect(screen.getByRole('article', { name: 'Scan 555' })).toBeInTheDocument();
    expect(within(screen.getByRole('article', { name: 'Scan 555' })).queryByText('Which item is this?')).not.toBeInTheDocument();
  });

  it('a no_space retry leaves the other card and the banner alone', async () => {
    const oats = scan({
      id: 'scan-oats',
      barcode: '555',
      productId: 'prod-oats',
      product: product('prod-oats', 'Oats'),
    });
    const rice = scan({
      id: 'scan-nospace-fail',
      barcode: '666',
      productId: 'prod-rice',
      product: product('prod-rice', 'Rice'),
    });
    const { commits } = renderQueue({
      scans: [oats, rice],
      inventory: [shelf('oats', 'Oats', 2), shelf('rice', 'Rice', 2)],
      batchResult: {
        updatedCount: 0,
        committedIds: [],
        failedIds: [oats.id, rice.id],
        failedReasons: [BUSY_SENTENCE, 'Which item is this?'],
        failedCodes: ['busy', 'no_item'],
      },
    });
    expect(await screen.findByRole('article', { name: 'Scan 555' })).toBeInTheDocument();
    fireEvent.click(screen.getByRole('checkbox', { name: 'Select all eligible scans for batch approval' }));
    fireEvent.click(screen.getByRole('button', { name: 'Confirm 2 selected scans' }));
    const banner = 'Some scans could not be confirmed.';
    expect((await screen.findByText(banner)).closest('.scan-batch-banner')).not.toBeNull();
    expect(within(screen.getByRole('article', { name: 'Scan 666' })).getByText('Which item is this?')).toBeInTheDocument();
    fireEvent.click(within(screen.getByRole('article', { name: 'Scan 666' })).getByRole('button', { name: 'Confirm' }));
    await waitFor(() => expect(commits).toContain('scan-nospace-fail'));
    expect(document.querySelector('.restock-toast')).toBeNull();
    expect(screen.queryByText(NO_SPACE_SENTENCE)).not.toBeInTheDocument();
    expect(screen.getAllByText(banner)).toHaveLength(1);
    expect(document.querySelector('.scan-batch-banner')?.textContent?.replace(/\s+/g, ' ').trim()).toBe(banner);
    expect(within(screen.getByRole('article', { name: 'Scan 666' })).getByText('Which item is this?')).toBeInTheDocument();
    expect(screen.getByRole('article', { name: 'Scan 555' })).toBeInTheDocument();
    expect(within(screen.getByRole('article', { name: 'Scan 555' })).queryByText('Which item is this?')).not.toBeInTheDocument();
  });

  it('a batch response with no committedIds key has no confirmed prefix', async () => {
    const oats = scan({
      id: 'scan-oats',
      barcode: '555',
      productId: 'prod-oats',
      product: product('prod-oats', 'Oats'),
    });
    const rice = scan({
      id: 'scan-rice',
      barcode: '666',
      productId: 'prod-rice',
      product: product('prod-rice', 'Rice'),
    });
    renderQueue({
      scans: [oats, rice],
      inventory: [shelf('oats', 'Oats', 2), shelf('rice', 'Rice', 2)],
      batchResult: {
        updatedCount: 0,
        failedIds: [oats.id, rice.id],
        failedReasons: [
          'Stockbot is busy right now. Try again in a moment.',
          'Stockbot is busy right now. Try again in a moment.',
        ],
        failedCodes: ['busy', 'busy'],
      },
    });
    expect(await screen.findByRole('article', { name: 'Scan 555' })).toBeInTheDocument();
    fireEvent.click(screen.getByRole('checkbox', { name: 'Select all eligible scans for batch approval' }));
    fireEvent.click(screen.getByRole('button', { name: 'Confirm 2 selected scans' }));
    const banner = await screen.findByText('Stockbot is busy right now. Try again in a moment.');
    expect(banner.closest('.scan-batch-banner')?.textContent).toBe('Stockbot is busy right now. Try again in a moment.');
    expect(screen.queryByText('confirmed.', { exact: false })).not.toBeInTheDocument();
  });

  const bannerText = () => document.querySelector('.scan-batch-banner')?.textContent?.replace(/\s+/g, ' ').trim() ?? '';

  it.each([
    {
      name: 'all busy then a single confirm',
      failures: [
        { id: 'scan-a', barcode: '701', name: 'Oats', code: 'busy', message: BUSY_SENTENCE },
        { id: 'scan-b', barcode: '702', name: 'Rice', code: 'busy', message: BUSY_SENTENCE },
      ],
      confirmBarcode: '701',
      banner: '',
    },
    {
      name: 'busy plus no_item then a single confirm of another card',
      failures: [
        { id: 'scan-a', barcode: '701', name: 'Oats', code: 'busy', message: BUSY_SENTENCE },
        { id: 'scan-b', barcode: '702', name: 'Rice', code: 'no_item', message: 'Which item is this?' },
      ],
      confirmBarcode: '701',
      banner: 'Some scans could not be confirmed.',
    },
    {
      name: 'busy plus no_item plus not_pending',
      failures: [
        { id: 'scan-a', barcode: '701', name: 'Oats', code: 'busy', message: BUSY_SENTENCE },
        { id: 'scan-b', barcode: '702', name: 'Rice', code: 'no_item', message: 'Which item is this?' },
        { id: 'scan-c', barcode: '703', name: 'Beans', code: 'not_pending', message: 'This scan was already handled.' },
      ],
      confirmBarcode: '701',
      banner: 'Some scans could not be confirmed.',
    },
    {
      name: 'resolving the last failure',
      failures: [
        { id: 'scan-a', barcode: '701', name: 'Oats', code: 'busy', message: BUSY_SENTENCE },
        { id: 'scan-b', barcode: '702', name: 'Rice', code: 'no_item', message: 'Which item is this?' },
      ],
      confirmBarcode: '702',
      banner: '',
    },
  ])('$name', async ({ failures, confirmBarcode, banner }) => {
    const scans = failures.map((failure) => scan({
      id: failure.id,
      barcode: failure.barcode,
      direction: 'stock_out',
      productId: `prod-${failure.id}`,
      product: product(`prod-${failure.id}`, failure.name),
    }));
    renderQueue({
      scans,
      inventory: scans.map((entry) => shelf(entry.id, entry.product?.name ?? 'Item', 2)),
      batchResult: {
        updatedCount: scans.length,
        committedIds: [],
        failedIds: failures.map((failure) => failure.id),
        failedReasons: failures.map((failure) => failure.message),
        failedCodes: failures.map((failure) => failure.code),
      },
    });
    expect(await screen.findByRole('article', { name: `Scan ${failures[0]?.barcode}` })).toBeInTheDocument();
    fireEvent.click(screen.getByRole('checkbox', { name: 'Select all eligible scans for batch approval' }));
    fireEvent.click(screen.getByRole('button', { name: `Confirm ${scans.length} selected scans` }));
    await waitFor(() => expect(document.querySelector('.scan-batch-banner')).not.toBeNull());
    fireEvent.click(within(screen.getByRole('article', { name: `Scan ${confirmBarcode}` })).getByRole('button', { name: 'Confirm' }));
    await waitFor(() => {
      if (banner === '') {
        expect(document.querySelector('.scan-batch-banner')).toBeNull();
      } else {
        expect(bannerText()).toBe(banner);
      }
    });
    if (banner === '') {
      expect(screen.queryByText(BUSY_SENTENCE)).not.toBeInTheDocument();
      expect(screen.queryByText(NO_SPACE_SENTENCE)).not.toBeInTheDocument();
      return;
    }
    const card = screen.getByRole('article', { name: 'Scan 702' });
    expect(within(card).getAllByText('Which item is this?').length).toBeGreaterThan(0);
    expect(bannerText()).not.toContain('Which item is this?');
  });
});
