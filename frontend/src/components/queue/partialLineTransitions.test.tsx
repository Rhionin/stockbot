import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import { useEffect, useState } from 'react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { MantineProvider } from '@mantine/core';
import { ScanQueuePage } from './ScanQueuePage';
import type { ScanEntry } from '../../types';

// The unmount clear schedules a state update as the page goes away, so the
// sentence is already gone with or without that call. This is the cleanup that
// owns it: deleting the call fails the leave row.
const unmountClearsPartialLine = readFileSync(join(process.cwd(), 'src/components/queue/ScanQueuePage.tsx'), 'utf8')
  .includes('window.clearTimeout(captureHighlightTimer.current);\n    setPartialLine(null);');

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

const wasLine = 'Only 1 of 3 Tortilla chips was on hand, so 1 was taken out.';
const fallback = 'Some scans could not be confirmed.';

const product = (id: string, name: string) => ({
  id,
  name,
  category: 'Grocery',
  unitOfMeasure: 'bag',
});

const stockedShelf = (productId: string, name: string) => ({
  item: {
    id: `item-${productId}`,
    userId: 'user-1',
    productId,
    product: product(productId, name),
    targetQuantity: null,
    createdAt: '2026-03-20T10:00:00Z',
  },
  instanceCount: 2,
  nearExpiryCount: 0,
  expiredCount: 0,
  needsAttention: false,
});

const scanEntry = (overrides: Partial<ScanEntry>): ScanEntry => ({
  id: 'scan-batch-partial',
  userId: 'user-1',
  barcode: 'scan-batch-partial',
  scannedAt: '2026-03-20T10:00:00Z',
  direction: 'stock_out',
  unitCount: 3,
  expiresAt: null,
  status: 'pending',
  productId: 'prod-chips',
  product: product('prod-chips', 'Tortilla chips'),
  committedAt: null,
  createdAt: '2026-03-20T10:00:00Z',
  ...overrides,
});

const chips = scanEntry({});
const kept = scanEntry({
  id: 'scan-keep',
  barcode: 'scan-keep',
  productId: 'prod-keep',
  product: product('prod-keep', 'Kept oil'),
  unitCount: 1,
});
const later = scanEntry({
  id: 'scan-later',
  barcode: 'scan-later',
  productId: 'prod-later',
  product: product('prod-later', 'Rolled oats'),
  unitCount: 1,
});
const arrived = scanEntry({
  id: 'scan-new',
  barcode: '555666777888',
  productId: 'prod-new',
  product: product('prod-new', 'New rice'),
  unitCount: 1,
});

const partialCount = { removedCount: 1, requestedCount: 3 };

const json = (body: unknown) => new Response(JSON.stringify(body), {
  status: 200,
  headers: { 'Content-Type': 'application/json' },
});

type Trigger = 'keep' | 'pending' | 'blocking' | 'confirm' | 'batch' | 'remove' | 'typed' | 'arrived' | 'remote' | 'leave';

const QueueOrLeft = ({ leaveRef }: { leaveRef: { current: () => void } }) => {
  const [here, setHere] = useState(true);
  useEffect(() => {
    leaveRef.current = () => setHere(false);
  }, [leaveRef]);
  if (!here) return <h1>Left the queue</h1>;
  return <ScanQueuePage />;
};

const rows: { name: string; trigger: Trigger }[] = [
  { name: 'the same cards keep the partial line', trigger: 'keep' },
  { name: 'amber cards left after a batch show the teal banner and the partial line', trigger: 'pending' },
  { name: 'a non-amber failure stays red until that card is resolved', trigger: 'blocking' },
  { name: 'a confirm start clears the partial line', trigger: 'confirm' },
  { name: 'a new batch clears the partial line when it starts', trigger: 'batch' },
  { name: 'removing a card clears the partial line', trigger: 'remove' },
  { name: 'typing a barcode clears the partial line', trigger: 'typed' },
  { name: 'a scan event clears the partial line', trigger: 'arrived' },
  { name: 'a remote mode change clears the partial line', trigger: 'remote' },
  { name: 'leaving the queue clears the partial line', trigger: 'leave' },
];

describe('partial line transitions', () => {
  beforeEach(() => {
    FakeEventSource.instances = [];
    vi.stubGlobal('EventSource', FakeEventSource);
  });

  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it.each(rows)('$name', async ({ trigger }) => {
    const mixed = trigger === 'pending' || trigger === 'blocking' || trigger === 'confirm' || trigger === 'remove';
    const secondBatch = trigger === 'batch';
    let pending = secondBatch ? [chips, later] : mixed ? [chips, kept] : [chips];
    let mode = 'stock_out';
    let pendingFetches = 0;
    let batchRound = 0;
    let releaseHold = () => {};
    const fetchMock = vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      const method = init?.method ?? 'GET';
      if (url.includes('batch-commit') && method === 'POST') {
        batchRound += 1;
        if (secondBatch && batchRound === 1) {
          pending = [later];
          return Promise.resolve(json({
            updatedCount: 1,
            committedIds: [chips.id],
            stockOutCounts: { [chips.id]: partialCount },
          }));
        }
        if (secondBatch && batchRound === 2) {
          return new Promise((resolve) => {
            releaseHold = () => resolve(json({ updatedCount: 1, committedIds: [later.id] }));
          });
        }
        if (mixed) {
          pending = [kept];
          const amberOnly = trigger === 'pending' || trigger === 'remove';
          return Promise.resolve(json({
            updatedCount: 2,
            committedIds: [chips.id],
            failedIds: [kept.id],
            failedReasons: [amberOnly ? 'Nothing on hand to take out.' : 'Choose which item this is first.'],
            failedCodes: [amberOnly ? 'none_on_hand' : 'product_unresolved'],
            stockOutCounts: { [chips.id]: partialCount },
          }));
        }
        if (trigger !== 'keep') pending = [];
        return Promise.resolve(json({
          updatedCount: 1,
          committedIds: [chips.id],
          stockOutCounts: { [chips.id]: partialCount },
        }));
      }
      if (url === '/api/scans' && method === 'POST') {
        return new Promise((resolve) => {
          releaseHold = () => resolve(json({ id: 'scan-typed', status: 'pending' }));
        });
      }
      if (method === 'PATCH' && /\/api\/scans\/[^/]+$/.test(url)) {
        pending = [];
        return Promise.resolve(json({ status: 'cancelled' }));
      }
      if (url.includes('/commit') && method === 'POST') {
        return new Promise((resolve) => {
          releaseHold = () => resolve(json({ id: kept.id, status: 'committed' }));
        });
      }
      if (url.endsWith('/api/scanner/mode') && method === 'POST') {
        return Promise.resolve(json({ mode }));
      }
      if (url.includes('status=pending')) {
        pendingFetches += 1;
        return Promise.resolve(json(pending));
      }
      if (url.includes('status=flagged') || url.includes('status=qr')) return Promise.resolve(json([]));
      if (url === '/api/inventory' || url.startsWith('/api/inventory?')) {
        return Promise.resolve(json([
          stockedShelf('prod-chips', 'Tortilla chips'),
          stockedShelf('prod-keep', 'Kept oil'),
          stockedShelf('prod-later', 'Rolled oats'),
          stockedShelf('prod-new', 'New rice'),
        ]));
      }
      if (url === '/api/products' || url.startsWith('/api/products?')) return Promise.resolve(json([]));
      if (url.endsWith('/api/scanner/config')) {
        return Promise.resolve(json({
          stockInBarcode: 'STOCK_IN',
          stockOutBarcode: 'STOCK_OUT',
          currentMode: mode,
          connected: false,
        }));
      }
      return Promise.reject(new Error(`Unexpected request: ${method} ${url}`));
    });
    vi.stubGlobal('fetch', fetchMock);

    const leaveRef = { current: () => {} };
    const view = render(
      <MantineProvider>
        {trigger === 'leave' ? <QueueOrLeft leaveRef={leaveRef} /> : <ScanQueuePage />}
      </MantineProvider>,
    );

    const starting = secondBatch ? 2 : mixed ? 2 : 1;
    expect(await screen.findAllByRole('article')).toHaveLength(starting);
    fireEvent.click(screen.getByRole('checkbox', { name: 'Select all eligible scans for batch approval' }));
    fireEvent.click(screen.getByRole('button', { name: `Confirm ${starting} selected scans` }));

    await screen.findByText(wasLine);
    await waitFor(() => expect(pendingFetches).toBeGreaterThanOrEqual(2));
    expect(screen.getByText(wasLine)).toBeInTheDocument();

    if (trigger === 'keep') {
      expect(screen.getByText('Tortilla chips')).toBeInTheDocument();
      expect(screen.getByText(wasLine)).toHaveClass('scan-batch-banner--success');
      expect(screen.queryByText(fallback)).not.toBeInTheDocument();
      return;
    }

    if (trigger === 'pending') {
      expect(screen.queryByText(fallback)).not.toBeInTheDocument();
      expect(screen.getByText('Kept oil')).toBeInTheDocument();
      expect(screen.getByText('Nothing on hand to take out.')).toBeInTheDocument();
      const line = screen.getByText(wasLine);
      expect(line).toHaveClass('scan-batch-banner--success');
      expect(line).toHaveAttribute('role', 'status');
      expect(line.closest('[role="alert"]')).toBeNull();
      return;
    }

    if (trigger === 'blocking') {
      expect(screen.queryByText(fallback)).not.toBeInTheDocument();
      expect(document.querySelector('.scan-batch-banner')?.textContent).toContain('Choose which item this is first.');
      const line = screen.getByText(wasLine);
      expect(line.closest('[role="alert"]')).not.toBeNull();
      expect(document.querySelector('.scan-batch-banner--success')).toBeNull();
      fireEvent.click(screen.getByRole('button', { name: 'Confirm' }));
      await waitFor(() => expect(screen.queryByText('Choose which item this is first.')).not.toBeInTheDocument());
      expect(screen.queryByText(wasLine)).not.toBeInTheDocument();
      return;
    }

    if (trigger === 'confirm') {
      fireEvent.click(screen.getByRole('button', { name: 'Confirm' }));
    } else if (trigger === 'batch') {
      fireEvent.click(screen.getByRole('checkbox', { name: 'Select all eligible scans for batch approval' }));
      fireEvent.click(screen.getByRole('button', { name: 'Confirm 1 selected scans' }));
    } else if (trigger === 'remove') {
      fireEvent.click(screen.getByRole('button', { name: 'Remove' }));
    } else if (trigger === 'typed') {
      const input = screen.getByLabelText('Barcode scanner input');
      fireEvent.change(input, { target: { value: '444444444444' } });
      fireEvent.keyDown(input, { key: 'Enter' });
    } else if (trigger === 'arrived') {
      FakeEventSource.instances[0]?.dispatch('scan', arrived);
    } else if (trigger === 'remote') {
      mode = 'stock_in';
      FakeEventSource.instances[0]?.dispatch('scanner_mode', 'stock_in');
    } else {
      expect(unmountClearsPartialLine).toBe(true);
      leaveRef.current();
    }

    await waitFor(() => {
      expect(screen.queryByText(wasLine)).not.toBeInTheDocument();
      expect(document.querySelector('.scan-batch-banner--success')).toBeNull();
    });
    if (trigger === 'arrived') expect(screen.getByText('New rice')).toBeInTheDocument();
    if (trigger === 'leave') expect(screen.getByRole('heading', { name: 'Left the queue' })).toBeInTheDocument();
    if (trigger === 'confirm' || trigger === 'typed' || trigger === 'batch') releaseHold();
    view.unmount();
  });
});
