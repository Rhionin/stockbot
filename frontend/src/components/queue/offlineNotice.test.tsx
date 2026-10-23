import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { MantineProvider } from '@mantine/core';
import { notifications, Notifications } from '@mantine/notifications';
import { OFFLINE_SENTENCE } from '../../api/contract';
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
}

const scan = (id: string, barcode: string): ScanEntry => ({
  id,
  userId: 'user-1',
  barcode,
  scannedAt: '2026-03-20T16:02:00Z',
  direction: 'stock_in',
  unitCount: 1,
  expiresAt: null,
  status: 'pending',
  productId: `prod-${id}`,
  product: { id: `prod-${id}`, name: barcode === '111' ? 'Milk' : 'Oats', category: 'Grocery', unitOfMeasure: 'carton' },
  committedAt: null,
  createdAt: '2026-03-20T16:02:00Z',
});

const json = (body: unknown, status = 200) =>
  new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } });

const RAW = ['Failed to fetch', 'Network request failed'];

type Action = 'confirm' | 'all' | 'remove';

function renderQueue(failure: unknown) {
  let pending = [scan('scan-milk', '111'), scan('scan-oats', '222')];
  let open = true;
  const fetchMock = vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
    const url = String(input);
    const method = init?.method ?? 'GET';
    if (url.includes('batch-commit') && method === 'POST') {
      if (open) return Promise.reject(failure);
      pending = [];
      return Promise.resolve(json({ updatedCount: 2, committedIds: ['scan-milk', 'scan-oats'] }));
    }
    if (url.endsWith('/commit') && method === 'POST') {
      if (open) return Promise.reject(failure);
      const id = url.split('/').at(-2) ?? '';
      pending = pending.filter((entry) => entry.id !== id);
      return Promise.resolve(json({ id, status: 'committed', direction: 'stock_in' }));
    }
    if (method === 'PATCH' && /\/api\/scans\/[^/]+$/.test(url)) {
      if (open) return Promise.reject(failure);
      const id = url.split('/').at(-1) ?? '';
      pending = pending.filter((entry) => entry.id !== id);
      return Promise.resolve(json({ id, status: 'cancelled' }));
    }
    if (url.includes('status=pending')) return Promise.resolve(json(pending));
    if (url.includes('status=flagged') || url.includes('status=qr')) return Promise.resolve(json([]));
    if (url === '/api/inventory' || url.startsWith('/api/inventory?') || url.startsWith('/api/products')) {
      return Promise.resolve(json([]));
    }
    if (url.endsWith('/api/scanner/config')) {
      return Promise.resolve(json({
        stockInBarcode: 'STOCK_IN',
        stockOutBarcode: 'STOCK_OUT',
        currentMode: 'stock_in',
        connected: false,
      }));
    }
    return Promise.reject(new Error(`Unexpected ${method} ${url}`));
  });
  vi.stubGlobal('fetch', fetchMock);
  render(
    <MantineProvider>
      <Notifications />
      <ScanQueuePage />
    </MantineProvider>,
  );
  return {
    fetchMock,
    succeed: () => {
      open = false;
    },
  };
}

async function press(action: Action, select: boolean) {
  const milk = await screen.findByRole('article', { name: /^Scan 111(,|$)/ });
  expect(screen.getByRole('article', { name: /^Scan 222(,|$)/ })).toBeInTheDocument();
  if (action === 'all') {
    if (select) {
      fireEvent.click(screen.getByRole('checkbox', { name: 'Select all eligible scans for batch approval' }));
    }
    fireEvent.click(screen.getByRole('button', { name: 'Confirm 2 selected scans' }));
  } else if (action === 'remove') {
    fireEvent.click(within(milk).getByRole('button', { name: 'Remove' }));
  } else {
    fireEvent.click(within(milk).getByRole('button', { name: 'Confirm' }));
  }
}

describe('offline notice', () => {
  beforeEach(() => {
    FakeEventSource.instances = [];
    notifications.clean();
    vi.stubGlobal('EventSource', FakeEventSource);
  });

  afterEach(() => {
    notifications.clean();
    vi.unstubAllGlobals();
  });

  it.each([
    { name: 'single confirm', action: 'confirm' as const, failure: new TypeError('Failed to fetch') },
    { name: 'confirm all', action: 'all' as const, failure: new TypeError('Failed to fetch') },
    { name: 'remove', action: 'remove' as const, failure: new TypeError('Failed to fetch') },
    { name: 'timeout', action: 'confirm' as const, failure: new DOMException('The operation was aborted due to timeout', 'TimeoutError') },
    { name: 'abort', action: 'confirm' as const, failure: new DOMException('The operation was aborted.', 'AbortError') },
  ])('$name shows the sentence and a later success clears it', async ({ action, failure }) => {
    const queue = renderQueue(failure);
    await press(action, true);

    await waitFor(() => {
      expect(screen.getAllByText(OFFLINE_SENTENCE).length).toBeGreaterThan(0);
    });
    expect(screen.getByRole('article', { name: /^Scan 111(,|$)/ })).toBeInTheDocument();
    expect(screen.getByRole('article', { name: /^Scan 222(,|$)/ })).toBeInTheDocument();
    expect(within(screen.getByRole('article', { name: /^Scan 111(,|$)/ })).getByRole('button', { name: 'Confirm' })).toBeInTheDocument();
    for (const raw of RAW) expect(screen.queryByText(raw)).not.toBeInTheDocument();

    queue.succeed();
    await press(action, false);

    await waitFor(() => {
      expect(screen.queryAllByText(OFFLINE_SENTENCE)).toHaveLength(0);
    });
    for (const raw of RAW) expect(screen.queryByText(raw)).not.toBeInTheDocument();
    if (action === 'all') {
      expect(screen.queryByRole('article')).not.toBeInTheDocument();
    } else {
      expect(screen.queryByRole('article', { name: /^Scan 111(,|$)/ })).not.toBeInTheDocument();
      expect(screen.getByRole('article', { name: /^Scan 222(,|$)/ })).toBeInTheDocument();
    }
  });

  it('keeps a 409 body and leaves the scan open', async () => {
    const pending = [scan('scan-milk', '111'), scan('scan-oats', '222')];
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      const method = init?.method ?? 'GET';
      if (url.endsWith('/commit') && method === 'POST') {
        return Promise.resolve(json({ error: 'That scan is no longer pending.', code: 'not_pending' }, 409));
      }
      if (url.includes('status=pending')) return Promise.resolve(json(pending));
      if (url.includes('status=flagged') || url.includes('status=qr')) return Promise.resolve(json([]));
      if (url === '/api/inventory' || url.startsWith('/api/inventory?') || url.startsWith('/api/products')) {
        return Promise.resolve(json([]));
      }
      if (url.endsWith('/api/scanner/config')) {
        return Promise.resolve(json({
          stockInBarcode: 'STOCK_IN',
          stockOutBarcode: 'STOCK_OUT',
          currentMode: 'stock_in',
          connected: false,
        }));
      }
      return Promise.reject(new Error(`Unexpected ${method} ${url}`));
    }));
    render(
      <MantineProvider>
        <Notifications />
        <ScanQueuePage />
      </MantineProvider>,
    );

    const milk = await screen.findByRole('article', { name: /^Scan 111(,|$)/ });
    fireEvent.click(within(milk).getByRole('button', { name: 'Confirm' }));

    expect(await screen.findByText('That scan is no longer pending.')).toBeInTheDocument();
    expect(screen.queryByText(OFFLINE_SENTENCE)).not.toBeInTheDocument();
    expect(screen.getByRole('article', { name: /^Scan 111(,|$)/ })).toBeInTheDocument();
  });
});
