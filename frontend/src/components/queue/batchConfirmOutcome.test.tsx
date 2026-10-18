import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { MantineProvider } from '@mantine/core';
import { ScanQueuePage } from './ScanQueuePage';
import { formatExpiryDate } from './queueUtils';
import type { ItemInstanceWithStatus, ScanEntry } from '../../types';

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
}

const product = (id: string, name: string) => ({
  id,
  name,
  category: 'Grocery',
  unitOfMeasure: 'carton',
});

const scanEntry = (overrides: Partial<ScanEntry>): ScanEntry => ({
  id: 'scan-milk',
  userId: 'user-1',
  barcode: '111',
  scannedAt: '2026-03-20T16:02:00Z',
  direction: 'stock_out',
  unitCount: 2,
  expiresAt: null,
  status: 'pending',
  productId: 'prod-milk',
  product: product('prod-milk', 'Milk'),
  committedAt: null,
  createdAt: '2026-03-20T16:02:00Z',
  ...overrides,
});

const milk = scanEntry({});
const oats = scanEntry({
  id: 'scan-oats',
  barcode: '222',
  scannedAt: '2026-03-20T16:03:00Z',
  productId: 'prod-oats',
  product: product('prod-oats', 'Oats'),
  createdAt: '2026-03-20T16:03:00Z',
});

const goneInstance: ItemInstanceWithStatus = {
  id: 'gone',
  itemId: 'item-milk',
  stockInAt: '2026-03-01T00:00:00Z',
  expiresAt: '2026-04-01T00:00:00Z',
  removedAt: null,
  removalReason: null,
  createdAt: '2026-03-01T00:00:00Z',
  expiryStatus: 'ok',
};
const keptInstance: ItemInstanceWithStatus = {
  ...goneInstance,
  id: 'kept',
  expiresAt: '2026-06-01T00:00:00Z',
  stockInAt: '2026-03-02T00:00:00Z',
  createdAt: '2026-03-02T00:00:00Z',
};

const goneLabel = `Expires ${formatExpiryDate(goneInstance.expiresAt!)}`;
const keptLabel = `Expires ${formatExpiryDate(keptInstance.expiresAt!)}`;

const jsonResponse = (body: unknown, status = 200) =>
  new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } });

const inventory = [
  {
    item: {
      id: 'item-milk',
      userId: 'user-1',
      productId: 'prod-milk',
      product: product('prod-milk', 'Milk'),
      targetQuantity: null,
      createdAt: '2026-03-20T10:00:00Z',
    },
    instanceCount: 2,
    nearExpiryCount: 0,
    expiredCount: 0,
    needsAttention: false,
  },
  {
    item: {
      id: 'item-oats',
      userId: 'user-1',
      productId: 'prod-oats',
      product: product('prod-oats', 'Oats'),
      targetQuantity: null,
      createdAt: '2026-03-20T10:00:00Z',
    },
    instanceCount: 1,
    nearExpiryCount: 0,
    expiredCount: 0,
    needsAttention: false,
  },
];

type PendingSet = 'both' | 'oats' | 'milk' | 'none';

const pendingEntries = (which: PendingSet): ScanEntry[] => {
  if (which === 'both') return [milk, oats];
  if (which === 'oats') return [oats];
  if (which === 'milk') return [milk];
  return [];
};

interface OutcomeCase {
  name: string;
  action: 'batch' | 'confirm-milk' | 'confirm-milk-instance' | 'confirm-oats-instance';
  pendingAfter: PendingSet;
  commit?: { status: number; body: unknown };
  batch?: unknown;
  instancesAfter?: ItemInstanceWithStatus[];
  visible: string[];
  absent: string[];
  pendingBarcodes: string[];
  goneBarcodes: string[];
  cardReason?: { barcode: string; text: string };
  instanceFetches?: number;
  dismiss?: boolean;
}

const committedMilk = { ...milk, status: 'committed' };

const cases: OutcomeCase[] = [
  {
    name: 'batch failure reason stays on the pending card',
    action: 'batch',
    pendingAfter: 'oats',
    batch: {
      updatedCount: 2,
      committedIds: ['scan-milk'],
      failedIds: ['scan-oats'],
      failedReasons: ['Nothing on hand to take out.'],
    },
    visible: ['Nothing on hand to take out.', 'Some scans could not be confirmed.'],
    absent: ['Confirmed 2', 'All scans confirmed'],
    pendingBarcodes: ['222'],
    goneBarcodes: ['111'],
    cardReason: { barcode: '222', text: 'Nothing on hand to take out.' },
  },
  {
    name: 'batch failure without a reason uses the shared fallback',
    action: 'batch',
    pendingAfter: 'oats',
    batch: {
      updatedCount: 2,
      committedIds: ['scan-milk'],
      failedIds: ['scan-oats'],
      failedReasons: [''],
    },
    visible: ['Some scans could not be confirmed.'],
    absent: ['Confirmed 2'],
    pendingBarcodes: ['222'],
    goneBarcodes: ['111'],
    cardReason: { barcode: '222', text: 'Some scans could not be confirmed.' },
  },
  {
    name: 'instance_gone shows the server message and drops the removed unit',
    action: 'confirm-milk-instance',
    pendingAfter: 'both',
    commit: {
      status: 409,
      body: { error: 'That one was already taken out. Pick another.', code: 'instance_gone' },
    },
    instancesAfter: [keptInstance],
    visible: ['That one was already taken out. Pick another.'],
    absent: [goneLabel],
    pendingBarcodes: ['111', '222'],
    goneBarcodes: [],
    cardReason: { barcode: '111', text: 'That one was already taken out. Pick another.' },
    instanceFetches: 2,
  },
  {
    name: 'none_on_hand shows the message and does not reload instances',
    action: 'confirm-milk-instance',
    pendingAfter: 'both',
    commit: {
      status: 409,
      body: { error: 'Nothing on hand to take out.', code: 'none_on_hand' },
    },
    instancesAfter: [goneInstance, keptInstance],
    visible: ['Nothing on hand to take out.'],
    absent: [],
    pendingBarcodes: ['111', '222'],
    goneBarcodes: [],
    cardReason: { barcode: '111', text: 'Nothing on hand to take out.' },
    instanceFetches: 1,
  },
  {
    name: 'a 409 without a code still shows the server message',
    action: 'confirm-milk',
    pendingAfter: 'both',
    commit: { status: 409, body: { error: 'That product is not in your pantry' } },
    visible: ['That product is not in your pantry'],
    absent: ['No close match. Search or add it as new.'],
    pendingBarcodes: ['111', '222'],
    goneBarcodes: [],
    cardReason: { barcode: '111', text: 'That product is not in your pantry' },
  },
  {
    name: 'no close match copy when nothing is suggested',
    action: 'confirm-milk',
    pendingAfter: 'both',
    commit: { status: 409, body: { error: 'Not in your pantry', code: 'no_item', suggestions: [] } },
    visible: ['No close match. Search or add it as new.'],
    absent: ['No stocked item matches this barcode.'],
    pendingBarcodes: ['111', '222'],
    goneBarcodes: [],
    dismiss: true,
  },
  {
    name: 'partial removal of one uses singular grammar',
    action: 'confirm-milk',
    pendingAfter: 'oats',
    commit: { status: 200, body: { ...committedMilk, requestedCount: 3, removedCount: 1 } },
    visible: ['Only 1 was on hand, so 1 was taken out.'],
    absent: ['Only 1 were on hand'],
    pendingBarcodes: ['222'],
    goneBarcodes: ['111'],
  },
  {
    name: 'partial removal of several uses plural grammar',
    action: 'confirm-milk',
    pendingAfter: 'oats',
    commit: { status: 200, body: { ...committedMilk, requestedCount: 4, removedCount: 2 } },
    visible: ['Only 2 were on hand, so 2 were taken out.'],
    absent: ['Only 2 was on hand'],
    pendingBarcodes: ['222'],
    goneBarcodes: ['111'],
  },
  {
    name: 'equal removal counts add no notice',
    action: 'confirm-milk',
    pendingAfter: 'oats',
    commit: { status: 200, body: { ...committedMilk, requestedCount: 2, removedCount: 2 } },
    visible: [],
    absent: ['was taken out', 'were taken out'],
    pendingBarcodes: ['222'],
    goneBarcodes: ['111'],
  },
  {
    name: 'missing removal counts behave as a normal confirm',
    action: 'confirm-milk',
    pendingAfter: 'oats',
    commit: { status: 200, body: committedMilk },
    visible: [],
    absent: ['was taken out', 'were taken out'],
    pendingBarcodes: ['222'],
    goneBarcodes: ['111'],
  },
  {
    name: 'batch result entry with a short removal shows the notice',
    action: 'batch',
    pendingAfter: 'none',
    batch: {
      updatedCount: 2,
      committedIds: ['scan-milk', 'scan-oats'],
      results: [{ id: 'scan-milk', requestedCount: 3, removedCount: 1 }],
    },
    visible: ['Only 1 was on hand, so 1 was taken out.'],
    absent: ['Some scans could not be confirmed.'],
    pendingBarcodes: [],
    goneBarcodes: ['111', '222'],
  },
  {
    name: 'batch reason carrying instance_gone refreshes that card',
    action: 'confirm-oats-instance',
    pendingAfter: 'oats',
    batch: {
      updatedCount: 2,
      committedIds: ['scan-milk'],
      failedIds: ['scan-oats'],
      failedReasons: [{ code: 'instance_gone', error: 'That one was already taken out. Pick another.' }],
    },
    instancesAfter: [keptInstance],
    visible: ['That one was already taken out. Pick another.', 'Some scans could not be confirmed.'],
    absent: [goneLabel],
    pendingBarcodes: ['222'],
    goneBarcodes: ['111'],
    cardReason: { barcode: '222', text: 'That one was already taken out. Pick another.' },
    instanceFetches: 2,
  },
];

describe('scan commit outcomes', () => {
  beforeEach(() => {
    FakeEventSource.instances = [];
    vi.stubGlobal('EventSource', FakeEventSource);
  });

  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it.each(cases)('$name', async (row) => {
    let pending: PendingSet = 'both';
    let instanceFetches = 0;
    const fetchMock = vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url.includes('batch-commit') && init?.method === 'POST') {
        pending = row.pendingAfter;
        return Promise.resolve(jsonResponse(row.batch ?? {}));
      }
      if (url.endsWith('/commit') && init?.method === 'POST') {
        pending = row.pendingAfter;
        const commit = row.commit ?? { status: 200, body: committedMilk };
        return Promise.resolve(jsonResponse(commit.body, commit.status));
      }
      if (url.includes('/instances')) {
        instanceFetches += 1;
        const first = [goneInstance, keptInstance];
        const body = instanceFetches === 1 ? first : (row.instancesAfter ?? first);
        return Promise.resolve(jsonResponse(body));
      }
      if (url.includes('status=pending')) return Promise.resolve(jsonResponse(pendingEntries(pending)));
      if (url.includes('status=flagged') || url.includes('status=qr')) return Promise.resolve(jsonResponse([]));
      if (url === '/api/inventory' || url === '/api/products') return Promise.resolve(jsonResponse(url === '/api/inventory' ? inventory : []));
      if (url.endsWith('/api/scanner/config')) {
        return Promise.resolve(jsonResponse({
          stockInBarcode: 'STOCK_IN',
          stockOutBarcode: 'STOCK_OUT',
          currentMode: 'stock_out',
          connected: false,
        }));
      }
      return Promise.reject(new Error(`Unexpected request: ${init?.method ?? 'GET'} ${url}`));
    });
    vi.stubGlobal('fetch', fetchMock);

    render(<MantineProvider><ScanQueuePage /></MantineProvider>);

    const milkCard = await screen.findByRole('article', { name: 'Scan 111' });
    expect(screen.getByRole('article', { name: 'Scan 222' })).toBeInTheDocument();

    if (row.action === 'confirm-milk-instance' || row.action === 'confirm-oats-instance') {
      const card = row.action === 'confirm-milk-instance'
        ? milkCard
        : screen.getByRole('article', { name: 'Scan 222' });
      fireEvent.click(within(card).getByRole('button', { name: 'More' }));
      fireEvent.click(await within(card).findByRole('radio', { name: goneLabel }));
    }

    if (row.action === 'batch' || row.action === 'confirm-oats-instance') {
      fireEvent.click(screen.getByRole('checkbox', { name: 'Select all eligible scans for batch approval' }));
      fireEvent.click(screen.getByRole('button', { name: 'Confirm 2 selected scans' }));
    } else {
      fireEvent.click(within(milkCard).getByRole('button', { name: 'Confirm' }));
    }

    await waitFor(() => {
      for (const barcode of row.goneBarcodes) {
        expect(screen.queryByRole('article', { name: `Scan ${barcode}` })).not.toBeInTheDocument();
      }
      for (const text of row.visible) {
        expect(screen.getAllByText(text).length).toBeGreaterThan(0);
      }
      for (const text of row.absent) {
        expect(screen.queryByText(text)).not.toBeInTheDocument();
      }
      if (row.instanceFetches !== undefined) {
        expect(instanceFetches).toBe(row.instanceFetches);
      }
    });

    for (const barcode of row.pendingBarcodes) {
      const card = screen.getByRole('article', { name: `Scan ${barcode}` });
      expect(within(card).getByRole('button', { name: 'Confirm' })).toBeInTheDocument();
    }
    if (row.cardReason) {
      const card = screen.getByRole('article', { name: `Scan ${row.cardReason.barcode}` });
      expect(within(card).getByRole('alert')).toHaveTextContent(row.cardReason.text);
    }
    for (const text of row.absent) {
      expect(screen.queryByText(text)).not.toBeInTheDocument();
    }
    if (row.dismiss) {
      expect(screen.getByRole('button', { name: 'Dismiss' })).toBeInTheDocument();
      expect(screen.queryByRole('button', { name: /^Link / })).not.toBeInTheDocument();
    }
    if (row.instanceFetches !== undefined) {
      expect(instanceFetches).toBe(row.instanceFetches);
    }
    if (row.action === 'confirm-milk-instance' && row.instancesAfter?.length === 1) {
      expect(screen.getByRole('radio', { name: keptLabel })).toBeInTheDocument();
    }
  });
});
