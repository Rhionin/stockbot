import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { MantineProvider } from '@mantine/core';
import { ScanQueuePage } from './ScanQueuePage';
import type { ScanEntry } from '../../types';

// Records the URL a component connects to, the listeners it registers, and
// whether it closes the connection, so tests can simulate the server pushing
// a message without a real network connection.
class FakeEventSource {
  static instances: FakeEventSource[] = [];

  readonly url: string;
  closed = false;
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

  close() {
    this.closed = true;
  }

  hasListener(type: string) {
    return (this.listeners.get(type)?.length ?? 0) > 0;
  }

  dispatch(type: string, payload: unknown) {
    for (const listener of this.listeners.get(type) ?? []) {
      listener({ data: JSON.stringify(payload) } as MessageEvent);
    }
  }
}

const scanEntry = (overrides: Partial<ScanEntry>): ScanEntry => ({
  id: 'scan-1',
  userId: 'user-1',
  barcode: '111',
  scannedAt: '2026-03-20T10:00:00Z',
  direction: null,
  unitCount: 1,
  expiresAt: null,
  status: 'pending',
  productId: null,
  product: null,
  committedAt: null,
  createdAt: '2026-03-20T10:00:00Z',
  ...overrides,
});

const jsonResponse = (body: unknown) =>
  new Response(JSON.stringify(body), { status: 200, headers: { 'Content-Type': 'application/json' } });

// The selected queue tab is the scan direction, and the hint next to Camera
// says what a new scan will do.
async function expectScannerMode(mode: 'stock_in' | 'stock_out') {
  const selectedName = mode === 'stock_in' ? /^Stock in/ : /^Stock out/;
  const otherName = mode === 'stock_in' ? /^Stock out/ : /^Stock in/;
  const hint = mode === 'stock_in' ? 'Scans add to stock' : 'Scans remove from stock';
  await waitFor(() => {
    expect(screen.getByRole('tab', { name: selectedName })).toHaveAttribute('aria-selected', 'true');
    expect(screen.getByRole('tab', { name: otherName })).toHaveAttribute('aria-selected', 'false');
    expect(screen.getByText(hint)).toBeInTheDocument();
  });
}

const findScan = (barcode: string) => screen.findByRole('article', { name: `Scan ${barcode}` });

const scansNamed = (cards: HTMLElement[], barcodes: string[]) =>
  cards.filter((card) => barcodes.includes(card.getAttribute('aria-label')?.replace(/^Scan /, '') ?? ''));

describe('ScanQueuePage', () => {
  beforeEach(() => {
    FakeEventSource.instances = [];
    vi.stubGlobal('EventSource', FakeEventSource);
  });

  it('shows pending and flagged scans newest first with a flagged indicator', async () => {
    const fetchMock = vi.fn((input: RequestInfo | URL) => {
      const url = String(input);
      if (url.includes('status=pending')) {
        return Promise.resolve(jsonResponse([
          scanEntry({
            id: 'newer',
            barcode: '222',
            scannedAt: '2026-03-20T11:00:00Z',
            productId: 'product-1',
            product: { id: 'product-1', name: 'Milk', category: 'Dairy', unitOfMeasure: 'carton' },
          }),
        ]));
      }
      if (url.includes('status=qr')) return Promise.resolve(jsonResponse([]));
      if (url.includes('status=flagged')) {
        return Promise.resolve(jsonResponse([
          scanEntry({ id: 'older', barcode: '111', scannedAt: '2026-03-20T09:00:00Z', status: 'flagged' }),
        ]));
      }
      if (url === '/api/inventory' || url === '/api/products') return Promise.resolve(jsonResponse([]));
      if (url.endsWith('/api/scanner/config')) {
        return Promise.resolve(jsonResponse({ stockInBarcode: 'STOCK_IN', stockOutBarcode: 'STOCK_OUT', currentMode: 'stock_out' }));
      }
      throw new Error(`Unexpected request: ${url}`);
    });
    vi.stubGlobal('fetch', fetchMock);

    render(<MantineProvider><ScanQueuePage /></MantineProvider>);

    expect(await screen.findByText('Barcode: 222')).toBeInTheDocument();
    // The earlier scan is a separate session and starts collapsed.
    expect(screen.queryByText('Barcode: 111')).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: /1 scan/, expanded: false }));

    const cards = await screen.findAllByRole('article');
    expect(within(cards[0]).getByText('Barcode: 222')).toBeInTheDocument();
    expect(within(cards[1]).getByText('Barcode: 111')).toBeInTheDocument();
    expect(within(cards[1]).getByText('Flagged')).toBeInTheDocument();
    expect(fetchMock).toHaveBeenCalledWith(
      '/api/scans?userId=user-1&status=pending',
      expect.any(Object),
    );
    expect(fetchMock).toHaveBeenCalledWith(
      '/api/scans?userId=user-1&status=flagged',
      expect.any(Object),
    );
  });

  // Reproduction test A (FEAT-001): scanning the STOCK_IN control barcode in
  // the browser must switch the displayed scanner mode to STOCK IN and must NOT
  // POST the literal control string 'STOCK_IN' as a product scan. Against
  // current code this FAILS because the browser capture path
  // (BarcodeInputField -> captureBarcode -> POST /api/scans) never classifies
  // the reserved control barcodes, so 'STOCK_IN' is sent as a product barcode
  // and no mode switch occurs. This test is the acceptance gate for the fix in
  // FEAT-002/FEAT-003.
  it('scanning the STOCK_IN control barcode switches to STOCK IN mode without posting a scan', async () => {
    const scanPosts: string[] = [];
    const modePosts: string[] = [];
    const fetchMock = vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url.endsWith('/api/scanner/mode') && init?.method === 'POST') {
        const body = init.body ? (JSON.parse(String(init.body)) as { mode: string }) : { mode: '' };
        modePosts.push(body.mode);
        return Promise.resolve(jsonResponse({ mode: body.mode }));
      }
      if (url.endsWith('/api/scanner/config')) {
        return Promise.resolve(jsonResponse({ stockInBarcode: 'STOCK_IN', stockOutBarcode: 'STOCK_OUT', currentMode: 'stock_out' }));
      }
      if (url.endsWith('/api/scans') && init?.method === 'POST') {
        const body = init.body ? (JSON.parse(String(init.body)) as { barcode: string }) : { barcode: '' };
        scanPosts.push(body.barcode);
        return Promise.resolve(jsonResponse(scanEntry({ id: 'created', barcode: body.barcode })));
      }
      if (url.includes('status=pending') || url.includes('status=flagged') || url.includes('status=qr')) {
        return Promise.resolve(jsonResponse([]));
      }
      if (url === '/api/inventory' || url === '/api/products') return Promise.resolve(jsonResponse([]));
      if (url.endsWith('/api/scanner/config')) {
        return Promise.resolve(jsonResponse({ stockInBarcode: 'STOCK_IN', stockOutBarcode: 'STOCK_OUT', currentMode: 'stock_out' }));
      }
      throw new Error(`Unexpected request: ${url}`);
    });
    vi.stubGlobal('fetch', fetchMock);

    render(<MantineProvider><ScanQueuePage /></MantineProvider>);
    await screen.findByText('No pending scans.');

    // The page defaults to STOCK OUT; scanning STOCK_IN must flip it to STOCK IN.
    await expectScannerMode('stock_out');

    const input = screen.getByLabelText(/barcode scanner input/i);
    fireEvent.change(input, { target: { value: 'STOCK_IN' } });
    fireEvent.keyDown(input, { key: 'Enter' });

    // The control barcode must switch the displayed mode to STOCK IN.
    await expectScannerMode('stock_in');

    // The literal control string must never be posted as a product scan;
    // instead the mode-switch endpoint must be called.
    expect(scanPosts).not.toContain('STOCK_IN');
    expect(modePosts).toContain('stock_in');
  });

  it('scanning the STOCK_OUT control barcode switches to STOCK OUT mode without posting a scan', async () => {
    const scanPosts: string[] = [];
    const modePosts: string[] = [];
    const fetchMock = vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url.endsWith('/api/scanner/mode') && init?.method === 'POST') {
        const body = init.body ? (JSON.parse(String(init.body)) as { mode: string }) : { mode: '' };
        modePosts.push(body.mode);
        return Promise.resolve(jsonResponse({ mode: body.mode }));
      }
      if (url.endsWith('/api/scanner/config')) {
        return Promise.resolve(jsonResponse({ stockInBarcode: 'STOCK_IN', stockOutBarcode: 'STOCK_OUT', currentMode: 'stock_out' }));
      }
      if (url.endsWith('/api/scans') && init?.method === 'POST') {
        const body = init.body ? (JSON.parse(String(init.body)) as { barcode: string }) : { barcode: '' };
        scanPosts.push(body.barcode);
        return Promise.resolve(jsonResponse(scanEntry({ id: 'created', barcode: body.barcode })));
      }
      if (url.includes('status=pending') || url.includes('status=flagged') || url.includes('status=qr')) {
        return Promise.resolve(jsonResponse([]));
      }
      if (url === '/api/inventory' || url === '/api/products') return Promise.resolve(jsonResponse([]));
      if (url.endsWith('/api/scanner/config')) {
        return Promise.resolve(jsonResponse({ stockInBarcode: 'STOCK_IN', stockOutBarcode: 'STOCK_OUT', currentMode: 'stock_out' }));
      }
      throw new Error(`Unexpected request: ${url}`);
    });
    vi.stubGlobal('fetch', fetchMock);

    render(<MantineProvider><ScanQueuePage /></MantineProvider>);
    await screen.findByText('No pending scans.');

    // Switch to STOCK IN first via SSE so the STOCK_OUT scan produces a visible change.
    const eventSource = FakeEventSource.instances[0];
    eventSource.dispatch('scanner_mode', { mode: 'stock_in' });
    await expectScannerMode('stock_in');

    const input = screen.getByLabelText(/barcode scanner input/i);
    fireEvent.change(input, { target: { value: 'STOCK_OUT' } });
    fireEvent.keyDown(input, { key: 'Enter' });

    await expectScannerMode('stock_out');
    expect(scanPosts).not.toContain('STOCK_OUT');
    expect(modePosts).toContain('stock_out');
  });

  it('scanning a normal product barcode still creates a scan entry tagged with the current mode', async () => {
    const scanPosts: Array<{ barcode: string; direction?: string }> = [];
    const modePosts: string[] = [];
    const fetchMock = vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url.endsWith('/api/scanner/mode') && init?.method === 'POST') {
        const body = init.body ? (JSON.parse(String(init.body)) as { mode: string }) : { mode: '' };
        modePosts.push(body.mode);
        return Promise.resolve(jsonResponse({ mode: body.mode }));
      }
      if (url.endsWith('/api/scanner/config')) {
        return Promise.resolve(jsonResponse({ stockInBarcode: 'STOCK_IN', stockOutBarcode: 'STOCK_OUT', currentMode: 'stock_out' }));
      }
      if (url.endsWith('/api/scans') && init?.method === 'POST') {
        const body = init.body ? (JSON.parse(String(init.body)) as { barcode: string; direction?: string }) : { barcode: '' };
        scanPosts.push({ barcode: body.barcode, direction: body.direction });
        return Promise.resolve(jsonResponse(scanEntry({ id: 'created', barcode: body.barcode })));
      }
      if (url.includes('status=pending')) {
        return Promise.resolve(jsonResponse(scanPosts.map((post, index) =>
          scanEntry({ id: `created-${index}`, barcode: post.barcode }))));
      }
      if (url.includes('status=flagged') || url.includes('status=qr')) return Promise.resolve(jsonResponse([]));
      if (url === '/api/inventory' || url === '/api/products') return Promise.resolve(jsonResponse([]));
      if (url.endsWith('/api/scanner/config')) {
        return Promise.resolve(jsonResponse({ stockInBarcode: 'STOCK_IN', stockOutBarcode: 'STOCK_OUT', currentMode: 'stock_out' }));
      }
      throw new Error(`Unexpected request: ${url}`);
    });
    vi.stubGlobal('fetch', fetchMock);

    render(<MantineProvider><ScanQueuePage /></MantineProvider>);
    await screen.findByText('No pending scans.');

    const input = screen.getByLabelText(/barcode scanner input/i);
    fireEvent.change(input, { target: { value: '0123456789012' } });
    fireEvent.keyDown(input, { key: 'Enter' });

    // A normal product barcode is posted as a scan and rendered as a card.
    expect(await screen.findByText('Barcode: 0123456789012')).toBeInTheDocument();
    // The scan carries the current mode as its direction (stock_out here), so
    // it lands in the same view as the selected direction.
    expect(scanPosts).toEqual([{ barcode: '0123456789012', direction: 'stock_out' }]);
    // No mode switch is triggered for an ordinary barcode.
    expect(modePosts).toHaveLength(0);
  });

  it('a product barcode scanned after a control barcode is tagged with the switched-to direction', async () => {
    const scanPosts: Array<{ barcode: string; direction?: string }> = [];
    const fetchMock = vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url.endsWith('/api/scanner/mode') && init?.method === 'POST') {
        const body = init.body ? (JSON.parse(String(init.body)) as { mode: string }) : { mode: '' };
        return Promise.resolve(jsonResponse({ mode: body.mode }));
      }
      if (url.endsWith('/api/scanner/config')) {
        return Promise.resolve(jsonResponse({ stockInBarcode: 'STOCK_IN', stockOutBarcode: 'STOCK_OUT', currentMode: 'stock_out' }));
      }
      if (url.endsWith('/api/scans') && init?.method === 'POST') {
        const body = init.body ? (JSON.parse(String(init.body)) as { barcode: string; direction?: string }) : { barcode: '' };
        scanPosts.push({ barcode: body.barcode, direction: body.direction });
        return Promise.resolve(jsonResponse(scanEntry({ id: 'created', barcode: body.barcode })));
      }
      if (url.includes('status=pending') || url.includes('status=flagged') || url.includes('status=qr')) {
        return Promise.resolve(jsonResponse([]));
      }
      if (url === '/api/inventory' || url === '/api/products') return Promise.resolve(jsonResponse([]));
      if (url.endsWith('/api/scanner/config')) {
        return Promise.resolve(jsonResponse({ stockInBarcode: 'STOCK_IN', stockOutBarcode: 'STOCK_OUT', currentMode: 'stock_out' }));
      }
      throw new Error(`Unexpected request: ${url}`);
    });
    vi.stubGlobal('fetch', fetchMock);

    render(<MantineProvider><ScanQueuePage /></MantineProvider>);
    await screen.findByText('No pending scans.');
    await expectScannerMode('stock_out');

    const input = screen.getByLabelText(/barcode scanner input/i);
    // Scan the STOCK_IN control barcode to switch mode.
    fireEvent.change(input, { target: { value: 'STOCK_IN' } });
    fireEvent.keyDown(input, { key: 'Enter' });
    await expectScannerMode('stock_in');

    // Now scan a product barcode; it must be tagged stock_in, not stock_out.
    fireEvent.change(input, { target: { value: '0123456789012' } });
    fireEvent.keyDown(input, { key: 'Enter' });

    await vi.waitFor(() =>
      expect(scanPosts).toEqual([{ barcode: '0123456789012', direction: 'stock_in' }]),
    );
  });

  it('applies a scanner_mode event in the server wire format and tags the next scan with it', async () => {
    const scanPosts: Array<{ barcode: string; direction?: string }> = [];
    const fetchMock = vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url.endsWith('/api/scanner/config')) {
        return Promise.resolve(jsonResponse({ stockInBarcode: 'STOCK_IN', stockOutBarcode: 'STOCK_OUT', currentMode: 'stock_out' }));
      }
      if (url.endsWith('/api/scans') && init?.method === 'POST') {
        const body = init.body ? (JSON.parse(String(init.body)) as { barcode: string; direction?: string }) : { barcode: '' };
        scanPosts.push({ barcode: body.barcode, direction: body.direction });
        return Promise.resolve(jsonResponse(scanEntry({ id: 'created', barcode: body.barcode, direction: 'stock_in' })));
      }
      if (url.includes('status=pending') || url.includes('status=flagged') || url.includes('status=qr')) {
        return Promise.resolve(jsonResponse([]));
      }
      if (url === '/api/inventory' || url === '/api/products') return Promise.resolve(jsonResponse([]));
      throw new Error(`Unexpected request: ${url}`);
    });
    vi.stubGlobal('fetch', fetchMock);

    render(<MantineProvider><ScanQueuePage /></MantineProvider>);
    await expectScannerMode('stock_out');

    // The server writes the direction as a JSON string, not { mode: ... }.
    const eventSource = FakeEventSource.instances[0];
    eventSource.dispatch('scanner_mode', 'stock_in');

    await expectScannerMode('stock_in');
    expect(screen.getByRole('tab', { name: /Stock in/ })).toHaveAttribute('aria-selected', 'true');

    const input = screen.getByLabelText(/barcode scanner input/i);
    fireEvent.change(input, { target: { value: '0123456789012' } });
    fireEvent.keyDown(input, { key: 'Enter' });

    await vi.waitFor(() =>
      expect(scanPosts).toEqual([{ barcode: '0123456789012', direction: 'stock_in' }]),
    );
  });

  it('shows a mode the server reports on the config refresh and tags the next scan with it', async () => {
    vi.useFakeTimers({ toFake: ['setInterval', 'clearInterval'] });
    try {
      let currentMode: 'stock_in' | 'stock_out' = 'stock_out';
      const scanPosts: Array<{ barcode: string; direction?: string }> = [];
      const fetchMock = vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
        const url = String(input);
        if (url.endsWith('/api/scanner/config')) {
          return Promise.resolve(jsonResponse({
            stockInBarcode: 'STOCK_IN',
            stockOutBarcode: 'STOCK_OUT',
            currentMode,
          }));
        }
        if (url.endsWith('/api/scans') && init?.method === 'POST') {
          const body = init.body ? (JSON.parse(String(init.body)) as { barcode: string; direction?: string }) : { barcode: '' };
          scanPosts.push({ barcode: body.barcode, direction: body.direction });
          return Promise.resolve(jsonResponse(scanEntry({ id: 'created', barcode: body.barcode })));
        }
        if (url.includes('status=pending') || url.includes('status=flagged') || url.includes('status=qr')) {
          return Promise.resolve(jsonResponse([]));
        }
        if (url === '/api/inventory' || url === '/api/products') return Promise.resolve(jsonResponse([]));
        throw new Error(`Unexpected request: ${url}`);
      });
      vi.stubGlobal('fetch', fetchMock);

      render(<MantineProvider><ScanQueuePage /></MantineProvider>);
      await expectScannerMode('stock_out');

      currentMode = 'stock_in';
      await act(async () => {
        vi.advanceTimersByTime(2000);
      });

      await expectScannerMode('stock_in');

      const input = screen.getByLabelText(/barcode scanner input/i);
      fireEvent.change(input, { target: { value: '0123456789012' } });
      fireEvent.keyDown(input, { key: 'Enter' });

      await vi.waitFor(() =>
        expect(scanPosts).toEqual([{ barcode: '0123456789012', direction: 'stock_in' }]),
      );
    } finally {
      vi.useRealTimers();
    }
  });

  it('keeps a mode switch when a config response that started earlier reports the old mode', async () => {
    vi.useFakeTimers({ toFake: ['setInterval', 'clearInterval'] });
    try {
      const pendingConfig: Array<(response: Response) => void> = [];
      const fetchMock = vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
        const url = String(input);
        if (url.endsWith('/api/scanner/mode') && init?.method === 'POST') {
          const body = init.body ? (JSON.parse(String(init.body)) as { mode: string }) : { mode: '' };
          return Promise.resolve(jsonResponse({ mode: body.mode }));
        }
        if (url.endsWith('/api/scanner/config')) {
          return new Promise<Response>((resolve) => {
            pendingConfig.push(resolve);
          });
        }
        if (url.endsWith('/api/scans') && init?.method === 'POST') {
          const body = init.body ? (JSON.parse(String(init.body)) as { barcode: string }) : { barcode: '' };
          return Promise.resolve(jsonResponse(scanEntry({ id: 'created', barcode: body.barcode })));
        }
        if (url.includes('status=pending') || url.includes('status=flagged') || url.includes('status=qr')) {
          return Promise.resolve(jsonResponse([]));
        }
        if (url === '/api/inventory' || url === '/api/products') return Promise.resolve(jsonResponse([]));
        throw new Error(`Unexpected request: ${url}`);
      });
      vi.stubGlobal('fetch', fetchMock);

      render(<MantineProvider><ScanQueuePage /></MantineProvider>);
      await act(async () => {
        pendingConfig.shift()?.(jsonResponse({
          stockInBarcode: 'STOCK_IN',
          stockOutBarcode: 'STOCK_OUT',
          currentMode: 'stock_out',
        }));
      });
      await expectScannerMode('stock_out');

      await act(async () => {
        vi.advanceTimersByTime(2000);
      });
      expect(pendingConfig).toHaveLength(1);

      const input = screen.getByLabelText(/barcode scanner input/i);
      fireEvent.change(input, { target: { value: 'STOCK_IN' } });
      fireEvent.keyDown(input, { key: 'Enter' });
      await expectScannerMode('stock_in');

      await act(async () => {
        pendingConfig.shift()?.(jsonResponse({
          stockInBarcode: 'STOCK_IN',
          stockOutBarcode: 'STOCK_OUT',
          currentMode: 'stock_out',
        }));
      });

      await expectScannerMode('stock_in');
    } finally {
      vi.useRealTimers();
    }
  });

  it('scanning an unrecognized string still posts it as a product barcode', async () => {
    const scanPosts: string[] = [];
    const modePosts: string[] = [];
    const fetchMock = vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url.endsWith('/api/scanner/mode') && init?.method === 'POST') {
        const body = init.body ? (JSON.parse(String(init.body)) as { mode: string }) : { mode: '' };
        modePosts.push(body.mode);
        return Promise.resolve(jsonResponse({ mode: body.mode }));
      }
      if (url.endsWith('/api/scanner/config')) {
        return Promise.resolve(jsonResponse({ stockInBarcode: 'STOCK_IN', stockOutBarcode: 'STOCK_OUT', currentMode: 'stock_out' }));
      }
      if (url.endsWith('/api/scans') && init?.method === 'POST') {
        const body = init.body ? (JSON.parse(String(init.body)) as { barcode: string }) : { barcode: '' };
        scanPosts.push(body.barcode);
        return Promise.resolve(jsonResponse(scanEntry({ id: 'created', barcode: body.barcode })));
      }
      if (url.includes('status=pending') || url.includes('status=flagged') || url.includes('status=qr')) {
        return Promise.resolve(jsonResponse([]));
      }
      if (url === '/api/inventory' || url === '/api/products') return Promise.resolve(jsonResponse([]));
      if (url.endsWith('/api/scanner/config')) {
        return Promise.resolve(jsonResponse({ stockInBarcode: 'STOCK_IN', stockOutBarcode: 'STOCK_OUT', currentMode: 'stock_out' }));
      }
      throw new Error(`Unexpected request: ${url}`);
    });
    vi.stubGlobal('fetch', fetchMock);

    render(<MantineProvider><ScanQueuePage /></MantineProvider>);
    await screen.findByText('No pending scans.');

    const input = screen.getByLabelText(/barcode scanner input/i);
    // 'stock_in' (lowercase) must NOT match the exact-case control string.
    fireEvent.change(input, { target: { value: 'stock_in' } });
    fireEvent.keyDown(input, { key: 'Enter' });

    await vi.waitFor(() => expect(scanPosts).toEqual(['stock_in']));
    expect(modePosts).toHaveLength(0);
  });

  it('adds a scan pushed over the event stream without an extra fetch', async () => {
    const fetchMock = vi.fn((input: RequestInfo | URL) => {
      const url = String(input);
      if (url.includes('status=pending') || url.includes('status=flagged') || url.includes('status=qr')) {
        return Promise.resolve(jsonResponse([]));
      }
      if (url === '/api/inventory' || url === '/api/products') return Promise.resolve(jsonResponse([]));
      if (url.endsWith('/api/scanner/config')) {
        return Promise.resolve(jsonResponse({ stockInBarcode: 'STOCK_IN', stockOutBarcode: 'STOCK_OUT', currentMode: 'stock_out' }));
      }
      throw new Error(`Unexpected request: ${url}`);
    });
    vi.stubGlobal('fetch', fetchMock);

    render(<MantineProvider><ScanQueuePage /></MantineProvider>);
    await screen.findByText('No pending scans.');

    const eventSource = FakeEventSource.instances[0];
    expect(eventSource.url).toBe('/api/events');
    expect(eventSource.hasListener('error')).toBe(true);

    const callCountBeforeEvent = fetchMock.mock.calls.length;
    eventSource.dispatch('scan', scanEntry({ id: 'pushed', barcode: '999' }));

    expect(await screen.findByText('Barcode: 999')).toBeInTheDocument();
    expect(fetchMock).toHaveBeenCalledTimes(callCountBeforeEvent);
  });

  it('shows a lookup in progress at the top until the scan event arrives', async () => {
    vi.stubGlobal('fetch', (input: RequestInfo | URL) => {
      const url = String(input);
      if (url.includes('status=pending')) {
        return Promise.resolve(jsonResponse([
          scanEntry({ id: 'older', barcode: '111', direction: 'stock_out', scannedAt: '2026-03-20T09:00:00Z' }),
        ]));
      }
      if (url.includes('status=flagged') || url.includes('status=qr')) return Promise.resolve(jsonResponse([]));
      if (url === '/api/inventory' || url === '/api/products') return Promise.resolve(jsonResponse([]));
      if (url.endsWith('/api/scanner/config')) {
        return Promise.resolve(jsonResponse({ stockInBarcode: 'STOCK_IN', stockOutBarcode: 'STOCK_OUT', currentMode: 'stock_out', connected: false }));
      }
      throw new Error(`Unexpected request: ${url}`);
    });

    render(<MantineProvider><ScanQueuePage /></MantineProvider>);
    await screen.findByText('Barcode: 111');

    const eventSource = FakeEventSource.instances[0];
    eventSource.dispatch('scan_processing', {
      id: 'lookup-1',
      userId: 'user-1',
      barcode: '999',
      direction: 'stock_out',
      scannedAt: '2026-03-20T12:00:00Z',
    });

    expect(await screen.findByText('Looking up product')).toBeInTheDocument();
    expect(screen.getByText('Checking the open food database…')).toBeInTheDocument();
    const cards = screen.getAllByRole('article');
    expect(within(cards[0]).getByText('Barcode: 999')).toBeInTheDocument();
    expect(within(cards[1]).getByText('Barcode: 111')).toBeInTheDocument();

    eventSource.dispatch('scan', scanEntry({
      id: 'done',
      barcode: '999',
      direction: 'stock_out',
      scannedAt: '2026-03-20T12:00:00Z',
      product: { id: 'product-9', name: 'Oats', category: 'Grocery', unitOfMeasure: 'box' },
      productId: 'product-9',
    }));

    await vi.waitFor(() => expect(screen.queryByText('Looking up product')).not.toBeInTheDocument());
    expect(screen.getByRole('heading', { name: 'Oats' })).toBeInTheDocument();
    // The earlier scan is now an older session, so it starts collapsed.
    expect(screen.queryByText('Barcode: 111')).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: /1 scan/, expanded: false }));
    const settled = screen.getAllByRole('article');
    expect(within(settled[0]).getByText('Oats')).toBeInTheDocument();
    expect(within(settled[1]).getByText('Barcode: 111')).toBeInTheDocument();
  });

  it('clears a failed lookup and shows the failure message', async () => {
    vi.stubGlobal('fetch', (input: RequestInfo | URL) => {
      const url = String(input);
      if (url.includes('status=pending') || url.includes('status=flagged') || url.includes('status=qr')) {
        return Promise.resolve(jsonResponse([]));
      }
      if (url === '/api/inventory' || url === '/api/products') return Promise.resolve(jsonResponse([]));
      if (url.endsWith('/api/scanner/config')) {
        return Promise.resolve(jsonResponse({ stockInBarcode: 'STOCK_IN', stockOutBarcode: 'STOCK_OUT', currentMode: 'stock_out', connected: false }));
      }
      throw new Error(`Unexpected request: ${url}`);
    });

    render(<MantineProvider><ScanQueuePage /></MantineProvider>);
    await screen.findByText('No pending scans.');

    const eventSource = FakeEventSource.instances[0];
    eventSource.dispatch('scan_processing', {
      id: 'lookup-1',
      userId: 'user-1',
      barcode: '999',
      direction: 'stock_out',
      scannedAt: '2026-03-20T12:00:00Z',
    });
    expect(await screen.findByText('Looking up product')).toBeInTheDocument();

    eventSource.dispatch('scan_processing_failed', {
      id: 'lookup-1',
      barcode: '999',
      message: "Couldn't look up that barcode.",
    });

    await vi.waitFor(() => expect(screen.queryByText('Looking up product')).not.toBeInTheDocument());
    expect(screen.getByText("Couldn't look up that barcode.")).toBeInTheDocument();
  });

  it('shows whether the scanner device is connected', async () => {
    vi.stubGlobal('fetch', (input: RequestInfo | URL) => {
      const url = String(input);
      if (url.includes('status=pending') || url.includes('status=flagged') || url.includes('status=qr')) {
        return Promise.resolve(jsonResponse([]));
      }
      if (url === '/api/inventory' || url === '/api/products') return Promise.resolve(jsonResponse([]));
      if (url.endsWith('/api/scanner/config')) {
        return Promise.resolve(jsonResponse({ stockInBarcode: 'STOCK_IN', stockOutBarcode: 'STOCK_OUT', currentMode: 'stock_in', connected: true }));
      }
      throw new Error(`Unexpected request: ${url}`);
    });

    render(<MantineProvider><ScanQueuePage /></MantineProvider>);
    expect(await screen.findByText('Scanner connected')).toBeInTheDocument();
  });

  it('ignores a manufactured error event and leaves displayed scans unchanged', async () => {
    const fetchMock = vi.fn((input: RequestInfo | URL) => {
      const url = String(input);
      if (url.includes('status=pending')) {
        return Promise.resolve(jsonResponse([scanEntry({ id: 'existing', barcode: '111' })]));
      }
      if (url.includes('status=flagged') || url.includes('status=qr')) return Promise.resolve(jsonResponse([]));
      if (url === '/api/inventory' || url === '/api/products') return Promise.resolve(jsonResponse([]));
      if (url.endsWith('/api/scanner/config')) {
        return Promise.resolve(jsonResponse({ stockInBarcode: 'STOCK_IN', stockOutBarcode: 'STOCK_OUT', currentMode: 'stock_out' }));
      }
      throw new Error(`Unexpected request: ${url}`);
    });
    vi.stubGlobal('fetch', fetchMock);

    render(<MantineProvider><ScanQueuePage /></MantineProvider>);
    await screen.findByText('Barcode: 111');

    const eventSource = FakeEventSource.instances[0];
    eventSource.dispatch('error', {});

    expect(screen.getByText('Barcode: 111')).toBeInTheDocument();
    expect(screen.queryAllByRole('article')).toHaveLength(1);
  });

  it('closes the event stream connection on unmount', async () => {
    const fetchMock = vi.fn(() => Promise.resolve(jsonResponse([])));
    vi.stubGlobal('fetch', fetchMock);

    const { unmount } = render(<MantineProvider><ScanQueuePage /></MantineProvider>);
    await screen.findByText('No pending scans.');

    const eventSource = FakeEventSource.instances[0];
    expect(eventSource.closed).toBe(false);

    unmount();

    expect(eventSource.closed).toBe(true);
  });

  it('shows the view matching the seeded scanner mode on initial mount (stock_out here)', async () => {
    const fetchMock = vi.fn((input: RequestInfo | URL) => {
      const url = String(input);
      if (url.includes('status=pending')) {
        return Promise.resolve(jsonResponse([
          scanEntry({ id: 'scan-1', barcode: '111', direction: 'stock_out' }),
          scanEntry({ id: 'scan-2', barcode: '222', direction: 'stock_in' }),
        ]));
      }
      if (url.includes('status=flagged') || url.includes('status=qr')) return Promise.resolve(jsonResponse([]));
      if (url === '/api/inventory' || url === '/api/products') return Promise.resolve(jsonResponse([]));
      // The seeded scanner mode drives the default visible tab; stock_out here.
      if (url.endsWith('/api/scanner/config')) {
        return Promise.resolve(jsonResponse({ stockInBarcode: 'STOCK_IN', stockOutBarcode: 'STOCK_OUT', currentMode: 'stock_out' }));
      }
      throw new Error(`Unexpected request: ${url}`);
    });
    vi.stubGlobal('fetch', fetchMock);

    render(<MantineProvider><ScanQueuePage /></MantineProvider>);

    // The visible tab follows the seeded scanner mode (stock_out). Wait for
    // the seeded view to settle before asserting the active tab.
    await screen.findByText('Barcode: 111');

    // Find tabs by their ID attributes to ensure we get the correct tab elements
    const stockOutTab = screen.getByRole('tab', { name: 'Stock out' });
    const stockInTab = screen.getByRole('tab', { name: 'Stock in' });

    // Verify Stock_Out tab is selected (aria-selected="true" for active, "false" for inactive)
    expect(stockOutTab).toHaveAttribute('aria-selected', 'true');
    expect(stockInTab).toHaveAttribute('aria-selected', 'false');

    // Verify the grid shows only stock_out entries (direction === 'stock_out' or null)
    const cards = screen.getAllByRole('article');
    expect(cards).toHaveLength(1);
    expect(within(cards[0]).getByText('Barcode: 111')).toBeInTheDocument();

    // Verify clicking Stock_In tab changes the content
    stockInTab.click();

    // The open stock-in session shows the barcode as secondary text.
    await findScan('222');
    const cardsAfterSwitch = await screen.findAllByRole('article');
    expect(cardsAfterSwitch).toHaveLength(1);
    expect(cardsAfterSwitch[0]).toHaveAttribute('aria-label', 'Scan 222');
    expect(within(cardsAfterSwitch[0]).getByText('Barcode: 222')).toHaveClass('copyable-barcode');
  });

  it('persists a Stock in tab selection as the scanner mode for the next mount', async () => {
    let currentMode: 'stock_in' | 'stock_out' = 'stock_out';
    const modePosts: string[] = [];
    const fetchMock = vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url.endsWith('/api/scanner/mode') && init?.method === 'POST') {
        const body = init.body ? (JSON.parse(String(init.body)) as { mode: 'stock_in' | 'stock_out' }) : { mode: 'stock_in' as const };
        modePosts.push(body.mode);
        currentMode = body.mode;
        return Promise.resolve(jsonResponse({ mode: body.mode }));
      }
      if (url.includes('status=pending')) {
        return Promise.resolve(jsonResponse([
          scanEntry({ id: 'scan-1', barcode: '111', direction: 'stock_out' }),
          scanEntry({ id: 'scan-2', barcode: '222', direction: 'stock_in' }),
        ]));
      }
      if (url.includes('status=flagged') || url.includes('status=qr')) return Promise.resolve(jsonResponse([]));
      if (url === '/api/inventory' || url === '/api/products') return Promise.resolve(jsonResponse([]));
      if (url.endsWith('/api/scanner/config')) {
        return Promise.resolve(jsonResponse({ stockInBarcode: 'STOCK_IN', stockOutBarcode: 'STOCK_OUT', currentMode }));
      }
      throw new Error(`Unexpected request: ${url}`);
    });
    vi.stubGlobal('fetch', fetchMock);

    const { unmount } = render(<MantineProvider><ScanQueuePage /></MantineProvider>);

    await screen.findByText('Barcode: 111');
    await expectScannerMode('stock_out');
    fireEvent.click(screen.getByRole('tab', { name: 'Stock in' }));

    await findScan('222');
    await expectScannerMode('stock_in');
    expect(screen.getByText('Scans add to stock')).toBeInTheDocument();
    await waitFor(() => expect(modePosts).toEqual(['stock_in']));

    unmount();

    render(<MantineProvider><ScanQueuePage /></MantineProvider>);

    const cards2 = await screen.findAllByRole('article');
    expect(cards2).toHaveLength(1);
    expect(within(cards2[0]).getByText('Barcode: 222')).toBeInTheDocument();
    await expectScannerMode('stock_in');
  });

  // Regression (FEAT-003): the backend defaults the scanner mode to stock_in,
  // so a product scanned right after load is tagged stock_in. If the visible
  // tab were hardcoded to stock_out, that card would be filed under the hidden
  // Stock-in tab and nothing would appear in the active view (the reported
  // bug). The visible tab must follow the seeded mode so the card shows up
  // without any manual tab switch. This fails against the pre-fix code.
  it('a scan taken in the default stock_in mode is visible in the active view without switching tabs', async () => {
    const scanPosts: Array<{ barcode: string; direction?: string }> = [];
    const createdEntries: ScanEntry[] = [];
    const fetchMock = vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url.endsWith('/api/scanner/config')) {
        return Promise.resolve(jsonResponse({ stockInBarcode: 'STOCK_IN', stockOutBarcode: 'STOCK_OUT', currentMode: 'stock_in' }));
      }
      if (url.endsWith('/api/scans') && init?.method === 'POST') {
        const body = init.body ? (JSON.parse(String(init.body)) as { barcode: string; direction?: string }) : { barcode: '' };
        scanPosts.push({ barcode: body.barcode, direction: body.direction });
        const entry = scanEntry({ id: 'created', barcode: body.barcode, direction: 'stock_in' });
        createdEntries.push(entry);
        return Promise.resolve(jsonResponse(entry));
      }
      if (url.includes('status=pending')) return Promise.resolve(jsonResponse(createdEntries));
      if (url.includes('status=flagged') || url.includes('status=qr')) return Promise.resolve(jsonResponse([]));
      if (url === '/api/inventory' || url === '/api/products') return Promise.resolve(jsonResponse([]));
      throw new Error(`Unexpected request: ${url}`);
    });
    vi.stubGlobal('fetch', fetchMock);

    render(<MantineProvider><ScanQueuePage /></MantineProvider>);
    await screen.findByText('No pending scans.');

    // The seeded mode is stock_in, so the Stock in tab is the active view.
    await expectScannerMode('stock_in');
    expect(screen.getByRole('tab', { name: 'Stock in' })).toHaveAttribute('aria-selected', 'true');

    const input = screen.getByLabelText(/barcode scanner input/i);
    fireEvent.change(input, { target: { value: '0123456789012' } });
    fireEvent.keyDown(input, { key: 'Enter' });

    // The scan is tagged stock_in and its row is visible without a manual
    // tab switch (it would be hidden under the Stock-in tab pre-fix).
    expect(await findScan('0123456789012')).toBeInTheDocument();
    expect(scanPosts).toEqual([{ barcode: '0123456789012', direction: 'stock_in' }]);
  });

  it('Stock_In_View renders only direction === "stock_in" entries', async () => {
    const fetchMock = vi.fn((input: RequestInfo | URL) => {
      const url = String(input);
      if (url.includes('status=pending')) {
        return Promise.resolve(jsonResponse([
          scanEntry({ id: 'stock-in-1', barcode: '111', direction: 'stock_in' }),
          scanEntry({ id: 'stock-out-1', barcode: '222', direction: 'stock_out' }),
          scanEntry({ id: 'null-direction-1', barcode: '333', direction: null }),
        ]));
      }
      if (url.includes('status=flagged') || url.includes('status=qr')) return Promise.resolve(jsonResponse([]));
      if (url === '/api/inventory' || url === '/api/products') return Promise.resolve(jsonResponse([]));
      if (url.endsWith('/api/scanner/config')) {
        return Promise.resolve(jsonResponse({ stockInBarcode: 'STOCK_IN', stockOutBarcode: 'STOCK_OUT', currentMode: 'stock_out' }));
      }
      throw new Error(`Unexpected request: ${url}`);
    });
    vi.stubGlobal('fetch', fetchMock);

    render(<MantineProvider><ScanQueuePage /></MantineProvider>);

    // Let the seeded stock_out view settle, then switch to Stock_In_View.
    await screen.findByText('Barcode: 222');
    const stockInTab = screen.getByRole('tab', { name: 'Stock in' });
    stockInTab.click();

    // Verify only the stock_in entry is rendered
    await findScan('111');
    const cards = screen.getAllByRole('article');
    expect(cards).toHaveLength(1);
    expect(cards[0]).toHaveAttribute('aria-label', 'Scan 111');
    expect(within(cards[0]).getByText('Barcode: 111')).toHaveClass('copyable-barcode');
  });

  it('Stock_Out_View renders direction === "stock_out" and direction === null entries, but not stock_in', async () => {
    const fetchMock = vi.fn((input: RequestInfo | URL) => {
      const url = String(input);
      if (url.includes('status=pending')) {
        return Promise.resolve(jsonResponse([
          scanEntry({ id: 'stock-in-1', barcode: '111', direction: 'stock_in' }),
          scanEntry({ id: 'stock-out-1', barcode: '222', direction: 'stock_out' }),
          scanEntry({ id: 'null-direction-1', barcode: '333', direction: null }),
        ]));
      }
      if (url.includes('status=flagged') || url.includes('status=qr')) return Promise.resolve(jsonResponse([]));
      if (url === '/api/inventory' || url === '/api/products') return Promise.resolve(jsonResponse([]));
      if (url.endsWith('/api/scanner/config')) {
        return Promise.resolve(jsonResponse({ stockInBarcode: 'STOCK_IN', stockOutBarcode: 'STOCK_OUT', currentMode: 'stock_out' }));
      }
      throw new Error(`Unexpected request: ${url}`);
    });
    vi.stubGlobal('fetch', fetchMock);

    render(<MantineProvider><ScanQueuePage /></MantineProvider>);

    // Stock_Out_View is the default, verify both stock_out and null-direction entries are rendered
    const cards = await screen.findAllByRole('article');
    expect(cards).toHaveLength(2);
    expect(within(cards[0]).getByText('Barcode: 222')).toBeInTheDocument();
    expect(within(cards[1]).getByText('Barcode: 333')).toBeInTheDocument();
  });

  it('Stock_Out_View excludes stock_in entries even when mixed with other directions', async () => {
    const fetchMock = vi.fn((input: RequestInfo | URL) => {
      const url = String(input);
      if (url.includes('status=pending')) {
        return Promise.resolve(jsonResponse([
          scanEntry({ id: 'stock-in-1', barcode: '111', direction: 'stock_in' }),
          scanEntry({ id: 'stock-in-2', barcode: '222', direction: 'stock_in' }),
          scanEntry({ id: 'stock-out-1', barcode: '333', direction: 'stock_out' }),
          scanEntry({ id: 'null-direction-1', barcode: '444', direction: null }),
        ]));
      }
      if (url.includes('status=flagged') || url.includes('status=qr')) return Promise.resolve(jsonResponse([]));
      if (url === '/api/inventory' || url === '/api/products') return Promise.resolve(jsonResponse([]));
      if (url.endsWith('/api/scanner/config')) {
        return Promise.resolve(jsonResponse({ stockInBarcode: 'STOCK_IN', stockOutBarcode: 'STOCK_OUT', currentMode: 'stock_out' }));
      }
      throw new Error(`Unexpected request: ${url}`);
    });
    vi.stubGlobal('fetch', fetchMock);

    render(<MantineProvider><ScanQueuePage /></MantineProvider>);

    // Stock_Out_View is the default, verify stock_in entries are NOT rendered
    const cards = await screen.findAllByRole('article');
    expect(cards).toHaveLength(2);
    expect(within(cards[0]).getByText('Barcode: 333')).toBeInTheDocument();
    expect(within(cards[1]).getByText('Barcode: 444')).toBeInTheDocument();

    // Verify stock_in entries are NOT in the document
    expect(screen.queryByText('Barcode: 111')).not.toBeInTheDocument();
    expect(screen.queryByText('Barcode: 222')).not.toBeInTheDocument();
  });

  it('switching from Stock_Out_View to Stock_In_View renders only stock_in entries', async () => {
    const fetchMock = vi.fn((input: RequestInfo | URL) => {
      const url = String(input);
      if (url.includes('status=pending')) {
        return Promise.resolve(jsonResponse([
          scanEntry({ id: 'stock-in-1', barcode: '111', direction: 'stock_in' }),
          scanEntry({ id: 'stock-out-1', barcode: '222', direction: 'stock_out' }),
          scanEntry({ id: 'null-direction-1', barcode: '333', direction: null }),
        ]));
      }
      if (url.includes('status=flagged') || url.includes('status=qr')) return Promise.resolve(jsonResponse([]));
      if (url === '/api/inventory' || url === '/api/products') return Promise.resolve(jsonResponse([]));
      if (url.endsWith('/api/scanner/config')) {
        return Promise.resolve(jsonResponse({ stockInBarcode: 'STOCK_IN', stockOutBarcode: 'STOCK_OUT', currentMode: 'stock_out' }));
      }
      throw new Error(`Unexpected request: ${url}`);
    });
    vi.stubGlobal('fetch', fetchMock);

    render(<MantineProvider><ScanQueuePage /></MantineProvider>);

    // Initially Stock_Out_View should be active and showing stock_out and null-direction entries
    let cards = await screen.findAllByRole('article');
    expect(cards).toHaveLength(2);
    expect(within(cards[0]).getByText('Barcode: 222')).toBeInTheDocument();
    expect(within(cards[1]).getByText('Barcode: 333')).toBeInTheDocument();

    // Switch to Stock_In_View
    const stockInTab = screen.getByRole('tab', { name: 'Stock in' });
    stockInTab.click();

    // Wait for the stock_in entry to appear and verify stock_out/null entries disappear
    await findScan('111');
    expect(screen.queryByText('Barcode: 222')).not.toBeInTheDocument();
    expect(screen.queryByText('Barcode: 333')).not.toBeInTheDocument();

    cards = screen.getAllByRole('article');
    expect(cards).toHaveLength(1);
    expect(within(cards[0]).getByText('Barcode: 111')).toBeInTheDocument();
  });

  describe('Selection clearing on tab switch', () => {
    it('clears selection when switching from Stock_Out_View to Stock_In_View', async () => {
      const fetchMock = vi.fn((input: RequestInfo | URL) => {
        const url = String(input);
        if (url.includes('status=pending')) {
          return Promise.resolve(jsonResponse([
            scanEntry({ id: 'stock-in-1', barcode: '111', direction: 'stock_in' }),
            scanEntry({ id: 'stock-out-1', barcode: '222', direction: 'stock_out' }),
          ]));
        }
        if (url.includes('status=flagged') || url.includes('status=qr')) return Promise.resolve(jsonResponse([]));
        if (url === '/api/inventory' || url === '/api/products') return Promise.resolve(jsonResponse([]));
        if (url.endsWith('/api/scanner/config')) {
        return Promise.resolve(jsonResponse({ stockInBarcode: 'STOCK_IN', stockOutBarcode: 'STOCK_OUT', currentMode: 'stock_out' }));
      }
      throw new Error(`Unexpected request: ${url}`);
      });
      vi.stubGlobal('fetch', fetchMock);

      render(<MantineProvider><ScanQueuePage /></MantineProvider>);

      // Start in Stock_Out_View with 1 eligible entry
      const cards = await screen.findAllByRole('article');
      expect(cards).toHaveLength(1);
      expect(within(cards[0]).getByText('Barcode: 222')).toBeInTheDocument();

      // Select the entry
      const checkbox = within(cards[0]).getByRole('checkbox');
      checkbox.click();
      expect(checkbox).toBeChecked();

      // Switch to Stock_In_View
      const stockInTab = screen.getByRole('tab', { name: 'Stock in' });
      stockInTab.click();

      // Verify Stock_In_View displays the stock_in entry
      await findScan('111');
      const cardsAfterSwitch = screen.getAllByRole('article');
      const cardsInStockInView = scansNamed(cardsAfterSwitch, ['111']);
      expect(cardsInStockInView).toHaveLength(1);

      // Verify the stock_in entry is not selected (selection was cleared)
      const checkboxInStockInView = within(cardsInStockInView[0]).getByRole('checkbox');
      expect(checkboxInStockInView).not.toBeChecked();
    });

    it('clears selection when switching from Stock_In_View to Stock_Out_View', async () => {
      const fetchMock = vi.fn((input: RequestInfo | URL) => {
        const url = String(input);
        if (url.includes('status=pending')) {
          return Promise.resolve(jsonResponse([
            scanEntry({ id: 'stock-in-1', barcode: '111', direction: 'stock_in' }),
            scanEntry({ id: 'stock-out-1', barcode: '222', direction: 'stock_out' }),
          ]));
        }
        if (url.includes('status=flagged') || url.includes('status=qr')) return Promise.resolve(jsonResponse([]));
        if (url === '/api/inventory' || url === '/api/products') return Promise.resolve(jsonResponse([]));
        if (url.endsWith('/api/scanner/config')) {
        return Promise.resolve(jsonResponse({ stockInBarcode: 'STOCK_IN', stockOutBarcode: 'STOCK_OUT', currentMode: 'stock_out' }));
      }
      throw new Error(`Unexpected request: ${url}`);
      });
      vi.stubGlobal('fetch', fetchMock);

      render(<MantineProvider><ScanQueuePage /></MantineProvider>);

      // Let the seeded stock_out view settle, then switch to Stock_In_View.
      await screen.findByText('Barcode: 222');
      const stockInTab = screen.getByRole('tab', { name: 'Stock in' });
      stockInTab.click();

      // Verify Stock_In_View displays the stock_in entry
      await findScan('111');
      let cards = screen.getAllByRole('article');
      const cardsInStockInView = scansNamed(cards, ['111']);
      expect(cardsInStockInView).toHaveLength(1);

      // Select the entry
      const checkbox = within(cardsInStockInView[0]).getByRole('checkbox');
      checkbox.click();
      expect(checkbox).toBeChecked();

      // Switch back to Stock_Out_View
      const stockOutTab = screen.getByRole('tab', { name: 'Stock out' });
      stockOutTab.click();

      // Verify Stock_Out_View displays the stock_out entry
      await screen.findByText('Barcode: 222');
      cards = screen.getAllByRole('article');
      const cardsInStockOutView = cards.filter((card) => {
        try {
          within(card).getByText('Barcode: 222');
          return true;
        } catch {
          return false;
        }
      });
      expect(cardsInStockOutView).toHaveLength(1);

      // Verify the stock_out entry is not selected (selection was cleared)
      const checkboxInStockOutView = within(cardsInStockOutView[0]).getByRole('checkbox');
      expect(checkboxInStockOutView).not.toBeChecked();
    });

    it('clears selection when switching tabs with multiple selected entries', async () => {
      const fetchMock = vi.fn((input: RequestInfo | URL) => {
        const url = String(input);
        if (url.includes('status=pending')) {
          return Promise.resolve(jsonResponse([
            scanEntry({ id: 'stock-in-1', barcode: '111', direction: 'stock_in' }),
            scanEntry({ id: 'stock-in-2', barcode: '222', direction: 'stock_in' }),
            scanEntry({ id: 'stock-out-1', barcode: '333', direction: 'stock_out' }),
            scanEntry({ id: 'stock-out-2', barcode: '444', direction: 'stock_out' }),
          ]));
        }
        if (url.includes('status=flagged') || url.includes('status=qr')) return Promise.resolve(jsonResponse([]));
        if (url === '/api/inventory' || url === '/api/products') return Promise.resolve(jsonResponse([]));
        if (url.endsWith('/api/scanner/config')) {
        return Promise.resolve(jsonResponse({ stockInBarcode: 'STOCK_IN', stockOutBarcode: 'STOCK_OUT', currentMode: 'stock_out' }));
      }
      throw new Error(`Unexpected request: ${url}`);
      });
      vi.stubGlobal('fetch', fetchMock);

      render(<MantineProvider><ScanQueuePage /></MantineProvider>);

      // Start in Stock_Out_View with 2 eligible entries
      let cards = await screen.findAllByRole('article');
      const outCards = cards.filter((card) => {
        try {
          within(card).getByText(/Barcode: (333|444)/);
          return true;
        } catch {
          return false;
        }
      });
      expect(outCards).toHaveLength(2);

      // Select both entries
      const checkbox1 = within(outCards[0]).getByRole('checkbox');
      const checkbox2 = within(outCards[1]).getByRole('checkbox');
      checkbox1.click();
      checkbox2.click();
      expect(checkbox1).toBeChecked();
      expect(checkbox2).toBeChecked();

      // Switch to Stock_In_View
      const stockInTab = screen.getByRole('tab', { name: 'Stock in' });
      stockInTab.click();

      // Verify Stock_In_View is now active and shows stock_in entries
      await findScan('111');
      cards = screen.getAllByRole('article');
      const inCards = scansNamed(cards, ['111', '222']);
      expect(inCards).toHaveLength(2);

      // Verify none of the entries are selected
      const inCheckbox1 = within(inCards[0]).getByRole('checkbox');
      const inCheckbox2 = within(inCards[1]).getByRole('checkbox');
      expect(inCheckbox1).not.toBeChecked();
      expect(inCheckbox2).not.toBeChecked();
    });

    it('clears selection when switching back and forth between tabs', async () => {
      const fetchMock = vi.fn((input: RequestInfo | URL) => {
        const url = String(input);
        if (url.includes('status=pending')) {
          return Promise.resolve(jsonResponse([
            scanEntry({ id: 'stock-in-1', barcode: '111', direction: 'stock_in' }),
            scanEntry({ id: 'stock-out-1', barcode: '222', direction: 'stock_out' }),
          ]));
        }
        if (url.includes('status=flagged') || url.includes('status=qr')) return Promise.resolve(jsonResponse([]));
        if (url === '/api/inventory' || url === '/api/products') return Promise.resolve(jsonResponse([]));
        if (url.endsWith('/api/scanner/config')) {
        return Promise.resolve(jsonResponse({ stockInBarcode: 'STOCK_IN', stockOutBarcode: 'STOCK_OUT', currentMode: 'stock_out' }));
      }
      throw new Error(`Unexpected request: ${url}`);
      });
      vi.stubGlobal('fetch', fetchMock);

      render(<MantineProvider><ScanQueuePage /></MantineProvider>);

      // Start in Stock_Out_View
      let cards = await screen.findAllByRole('article');
      let checkbox = within(cards[0]).getByRole('checkbox');

      // Select entry in Stock_Out_View
      checkbox.click();
      expect(checkbox).toBeChecked();

      // Switch to Stock_In_View
      const stockInTab = screen.getByRole('tab', { name: 'Stock in' });
      stockInTab.click();

      // Verify selection was cleared
      await findScan('111');
      cards = scansNamed(screen.getAllByRole('article'), ['111']);
      checkbox = within(cards[0]).getByRole('checkbox');
      expect(checkbox).not.toBeChecked();

      // Select entry in Stock_In_View
      checkbox.click();
      expect(checkbox).toBeChecked();

      // Switch back to Stock_Out_View
      const stockOutTab = screen.getByRole('tab', { name: 'Stock out' });
      stockOutTab.click();

      // Verify selection was cleared again
      await screen.findByText('Barcode: 222');
      cards = screen.getAllByRole('article').filter((card) => {
        try {
          within(card).getByText('Barcode: 222');
          return true;
        } catch {
          return false;
        }
      });
      checkbox = within(cards[0]).getByRole('checkbox');
      expect(checkbox).not.toBeChecked();
    });

    it('clears selection when using Select_All_Control then switching tabs', async () => {
      const fetchMock = vi.fn((input: RequestInfo | URL) => {
        const url = String(input);
        if (url.includes('status=pending')) {
          return Promise.resolve(jsonResponse([
            scanEntry({ id: 'stock-in-1', barcode: '111', direction: 'stock_in' }),
            scanEntry({ id: 'stock-in-2', barcode: '222', direction: 'stock_in' }),
            scanEntry({ id: 'stock-out-1', barcode: '333', direction: 'stock_out' }),
            scanEntry({ id: 'stock-out-2', barcode: '444', direction: 'stock_out' }),
          ]));
        }
        if (url.includes('status=flagged') || url.includes('status=qr')) return Promise.resolve(jsonResponse([]));
        if (url === '/api/inventory' || url === '/api/products') return Promise.resolve(jsonResponse([]));
        if (url.endsWith('/api/scanner/config')) {
        return Promise.resolve(jsonResponse({ stockInBarcode: 'STOCK_IN', stockOutBarcode: 'STOCK_OUT', currentMode: 'stock_out' }));
      }
      throw new Error(`Unexpected request: ${url}`);
      });
      vi.stubGlobal('fetch', fetchMock);

      render(<MantineProvider><ScanQueuePage /></MantineProvider>);

      // Start in Stock_Out_View
      await screen.findByText('Barcode: 333');

      // Use Select_All_Control to select all eligible entries in Stock_Out_View
      const selectAllCheckbox = screen.getByRole('checkbox', { name: 'Select all eligible scans for batch approval' });
      selectAllCheckbox.click();

      // Verify all eligible entries are selected
      let cards = screen.getAllByRole('article').filter((card) => {
        try {
          within(card).getByText(/Barcode: (333|444)/);
          return true;
        } catch {
          return false;
        }
      });
      const checkbox1 = within(cards[0]).getByRole('checkbox');
      const checkbox2 = within(cards[1]).getByRole('checkbox');
      expect(checkbox1).toBeChecked();
      expect(checkbox2).toBeChecked();

      // Switch to Stock_In_View
      const stockInTab = screen.getByRole('tab', { name: 'Stock in' });
      stockInTab.click();

      // Verify stock_in entries are not selected (selection was cleared)
      await findScan('111');
      cards = scansNamed(screen.getAllByRole('article'), ['111', '222']);
      const inCheckbox1 = within(cards[0]).getByRole('checkbox');
      const inCheckbox2 = within(cards[1]).getByRole('checkbox');
      expect(inCheckbox1).not.toBeChecked();
      expect(inCheckbox2).not.toBeChecked();
    });
  });

  describe('Select_All_Control (view-scoped)', () => {
    it('is unchecked when no eligible entries exist in the Active_View', async () => {
      const fetchMock = vi.fn((input: RequestInfo | URL) => {
        const url = String(input);
        if (url.includes('status=pending')) {
          return Promise.resolve(jsonResponse([
            // Only flagged entries with no direction (not eligible because status !== 'pending')
            scanEntry({ id: 'flagged-1', barcode: '111', status: 'flagged', direction: null }),
          ]));
        }
        if (url.includes('status=qr')) return Promise.resolve(jsonResponse([]));
        if (url.includes('status=flagged')) {
          return Promise.resolve(jsonResponse([
            scanEntry({ id: 'flagged-2', barcode: '222', status: 'flagged', direction: null }),
          ]));
        }
        if (url === '/api/inventory' || url === '/api/products') return Promise.resolve(jsonResponse([]));
        if (url.endsWith('/api/scanner/config')) {
        return Promise.resolve(jsonResponse({ stockInBarcode: 'STOCK_IN', stockOutBarcode: 'STOCK_OUT', currentMode: 'stock_out' }));
      }
      throw new Error(`Unexpected request: ${url}`);
      });
      vi.stubGlobal('fetch', fetchMock);

      render(<MantineProvider><ScanQueuePage /></MantineProvider>);

      const selectAllCheckbox = await screen.findByRole('checkbox', { name: 'Select all eligible scans for batch approval' });
      expect(selectAllCheckbox).not.toBeChecked();
    });

    it('is disabled when no eligible entries exist in the Active_View', async () => {
      const fetchMock = vi.fn((input: RequestInfo | URL) => {
        const url = String(input);
        if (url.includes('status=pending')) {
          return Promise.resolve(jsonResponse([
            // Only pending entries with no direction (not eligible because direction === null)
            scanEntry({ id: 'pending-1', barcode: '111', status: 'pending', direction: null }),
          ]));
        }
        if (url.includes('status=flagged') || url.includes('status=qr')) return Promise.resolve(jsonResponse([]));
        if (url === '/api/inventory' || url === '/api/products') return Promise.resolve(jsonResponse([]));
        if (url.endsWith('/api/scanner/config')) {
        return Promise.resolve(jsonResponse({ stockInBarcode: 'STOCK_IN', stockOutBarcode: 'STOCK_OUT', currentMode: 'stock_out' }));
      }
      throw new Error(`Unexpected request: ${url}`);
      });
      vi.stubGlobal('fetch', fetchMock);

      render(<MantineProvider><ScanQueuePage /></MantineProvider>);

      const selectAllCheckbox = await screen.findByRole('checkbox', { name: 'Select all eligible scans for batch approval' });
      expect(selectAllCheckbox).toBeDisabled();
    });

    it('is checked when all eligible entries in the Active_View are selected', async () => {
      const fetchMock = vi.fn((input: RequestInfo | URL) => {
        const url = String(input);
        if (url.includes('status=pending')) {
          return Promise.resolve(jsonResponse([
            scanEntry({ id: 'stock-out-1', barcode: '111', direction: 'stock_out' }),
            scanEntry({ id: 'stock-out-2', barcode: '222', direction: 'stock_out' }),
          ]));
        }
        if (url.includes('status=flagged') || url.includes('status=qr')) return Promise.resolve(jsonResponse([]));
        if (url === '/api/inventory' || url === '/api/products') return Promise.resolve(jsonResponse([]));
        if (url.endsWith('/api/scanner/config')) {
        return Promise.resolve(jsonResponse({ stockInBarcode: 'STOCK_IN', stockOutBarcode: 'STOCK_OUT', currentMode: 'stock_out' }));
      }
      throw new Error(`Unexpected request: ${url}`);
      });
      vi.stubGlobal('fetch', fetchMock);

      render(<MantineProvider><ScanQueuePage /></MantineProvider>);

      const selectAllCheckbox = await screen.findByRole('checkbox', { name: 'Select all eligible scans for batch approval' });
      
      // Initially unchecked
      expect(selectAllCheckbox).not.toBeChecked();

      // Click to select all
      selectAllCheckbox.click();

      // Now should be checked
      expect(selectAllCheckbox).toBeChecked();

      // Verify all cards are selected
      const cards = await screen.findAllByRole('article');
      const checkboxes = cards.map((card) => within(card).getByRole('checkbox'));
      checkboxes.forEach((checkbox) => {
        expect(checkbox).toBeChecked();
      });
    });

    it('toggles only eligible entries within the Active_View', async () => {
      const fetchMock = vi.fn((input: RequestInfo | URL) => {
        const url = String(input);
        if (url.includes('status=pending')) {
          return Promise.resolve(jsonResponse([
            scanEntry({ id: 'eligible-1', barcode: '111', status: 'pending', direction: 'stock_out' }),
            scanEntry({ id: 'eligible-2', barcode: '222', status: 'pending', direction: 'stock_out' }),
            scanEntry({ id: 'ineligible-1', barcode: '333', status: 'pending', direction: null }),
          ]));
        }
        if (url.includes('status=flagged') || url.includes('status=qr')) return Promise.resolve(jsonResponse([]));
        if (url === '/api/inventory' || url === '/api/products') return Promise.resolve(jsonResponse([]));
        if (url.endsWith('/api/scanner/config')) {
        return Promise.resolve(jsonResponse({ stockInBarcode: 'STOCK_IN', stockOutBarcode: 'STOCK_OUT', currentMode: 'stock_out' }));
      }
      throw new Error(`Unexpected request: ${url}`);
      });
      vi.stubGlobal('fetch', fetchMock);

      render(<MantineProvider><ScanQueuePage /></MantineProvider>);

      const selectAllCheckbox = await screen.findByRole('checkbox', { name: 'Select all eligible scans for batch approval' });

      // Verify we have 3 entries (2 eligible, 1 ineligible)
      const cards = await screen.findAllByRole('article');
      expect(cards).toHaveLength(3);

      // Click Select_All_Control
      selectAllCheckbox.click();

      // Verify only the 2 eligible entries are selected, not the ineligible one
      const cardCheckboxes = cards.map((card) => within(card).getByRole('checkbox'));
      expect(cardCheckboxes[0]).toBeChecked(); // eligible-1
      expect(cardCheckboxes[1]).toBeChecked(); // eligible-2
      expect(cardCheckboxes[2]).not.toBeChecked(); // ineligible-1 (direction === null)
    });

    it('Select_All_Control respects view filtering by toggling only visible eligible entries', async () => {
      const fetchMock = vi.fn((input: RequestInfo | URL) => {
        const url = String(input);
        if (url.includes('status=pending')) {
          return Promise.resolve(jsonResponse([
            scanEntry({ id: 'stock-in-1', barcode: '111', direction: 'stock_in' }),
            scanEntry({ id: 'stock-out-1', barcode: '222', direction: 'stock_out' }),
            scanEntry({ id: 'stock-out-2', barcode: '333', direction: 'stock_out' }),
          ]));
        }
        if (url.includes('status=flagged') || url.includes('status=qr')) return Promise.resolve(jsonResponse([]));
        if (url === '/api/inventory' || url === '/api/products') return Promise.resolve(jsonResponse([]));
        if (url.endsWith('/api/scanner/config')) {
        return Promise.resolve(jsonResponse({ stockInBarcode: 'STOCK_IN', stockOutBarcode: 'STOCK_OUT', currentMode: 'stock_out' }));
      }
      throw new Error(`Unexpected request: ${url}`);
      });
      vi.stubGlobal('fetch', fetchMock);

      render(<MantineProvider><ScanQueuePage /></MantineProvider>);

      // Start in Stock_Out_View with 2 eligible entries
      let cards = await screen.findAllByRole('article');
      expect(cards).toHaveLength(2); // stock-out-1 and stock-out-2
      expect(within(cards[0]).getByText('Barcode: 222')).toBeInTheDocument();
      expect(within(cards[1]).getByText('Barcode: 333')).toBeInTheDocument();

      // Use Select_All_Control in Stock_Out_View - should select only the 2 visible entries
      const selectAllCheckbox = screen.getByRole('checkbox', { name: 'Select all eligible scans for batch approval' });
      selectAllCheckbox.click();

      // Verify both visible entries are selected
      expect(within(cards[0]).getByRole('checkbox')).toBeChecked();
      expect(within(cards[1]).getByRole('checkbox')).toBeChecked();

      // Switch to Stock_In_View (clears selection per Requirement 11)
      const stockInTab = screen.getByRole('tab', { name: 'Stock in' });
      stockInTab.click();

      // Verify we're now showing only the stock_in entry
      await findScan('111');
      cards = scansNamed(screen.getAllByRole('article'), ['111']);
      expect(cards).toHaveLength(1);

      // The stock_in entry should NOT be selected (selection was cleared on tab switch)
      expect(within(cards[0]).getByRole('checkbox')).not.toBeChecked();

      // Use Select_All_Control in Stock_In_View - should select only this 1 entry
      const selectAllCheckboxStockIn = screen.getByRole('checkbox', { name: 'Select all eligible scans for batch approval' });
      selectAllCheckboxStockIn.click();
      expect(within(cards[0]).getByRole('checkbox')).toBeChecked();
    });


    it('preserves selection state of ineligible entries when Select_All_Control is activated', async () => {
      const fetchMock = vi.fn((input: RequestInfo | URL) => {
        const url = String(input);
        if (url.includes('status=pending')) {
          return Promise.resolve(jsonResponse([
            scanEntry({ id: 'eligible-1', barcode: '111', status: 'pending', direction: 'stock_out' }),
            scanEntry({ id: 'ineligible-1', barcode: '222', status: 'pending', direction: null }),
            scanEntry({ id: 'ineligible-2', barcode: '333', status: 'pending', direction: null }),
          ]));
        }
        if (url.includes('status=flagged') || url.includes('status=qr')) return Promise.resolve(jsonResponse([]));
        if (url === '/api/inventory' || url === '/api/products') return Promise.resolve(jsonResponse([]));
        if (url.endsWith('/api/scanner/config')) {
        return Promise.resolve(jsonResponse({ stockInBarcode: 'STOCK_IN', stockOutBarcode: 'STOCK_OUT', currentMode: 'stock_out' }));
      }
      throw new Error(`Unexpected request: ${url}`);
      });
      vi.stubGlobal('fetch', fetchMock);

      render(<MantineProvider><ScanQueuePage /></MantineProvider>);

      // Get all entries (eligible and ineligible)
      const cards = await screen.findAllByRole('article');
      expect(cards).toHaveLength(3);

      // Manually select the ineligible entries before using Select_All_Control
      const ineligibleCheckbox1 = within(cards[1]).getByRole('checkbox');
      const ineligibleCheckbox2 = within(cards[2]).getByRole('checkbox');
      ineligibleCheckbox1.click();
      ineligibleCheckbox2.click();

      expect(ineligibleCheckbox1).toBeChecked();
      expect(ineligibleCheckbox2).toBeChecked();

      // Now use Select_All_Control to toggle all eligible entries
      const selectAllCheckbox = screen.getByRole('checkbox', { name: 'Select all eligible scans for batch approval' });
      selectAllCheckbox.click();

      // Verify:
      // - The eligible entry is now selected
      // - The ineligible entries REMAIN selected (preserved)
      const eligibleCheckbox = within(cards[0]).getByRole('checkbox');
      expect(eligibleCheckbox).toBeChecked();
      expect(ineligibleCheckbox1).toBeChecked();
      expect(ineligibleCheckbox2).toBeChecked();
    });

    it('deselects all eligible entries in Active_View when all are already selected', async () => {
      const fetchMock = vi.fn((input: RequestInfo | URL) => {
        const url = String(input);
        if (url.includes('status=pending')) {
          return Promise.resolve(jsonResponse([
            scanEntry({ id: 'eligible-1', barcode: '111', direction: 'stock_out' }),
            scanEntry({ id: 'eligible-2', barcode: '222', direction: 'stock_out' }),
          ]));
        }
        if (url.includes('status=flagged') || url.includes('status=qr')) return Promise.resolve(jsonResponse([]));
        if (url === '/api/inventory' || url === '/api/products') return Promise.resolve(jsonResponse([]));
        if (url.endsWith('/api/scanner/config')) {
        return Promise.resolve(jsonResponse({ stockInBarcode: 'STOCK_IN', stockOutBarcode: 'STOCK_OUT', currentMode: 'stock_out' }));
      }
      throw new Error(`Unexpected request: ${url}`);
      });
      vi.stubGlobal('fetch', fetchMock);

      render(<MantineProvider><ScanQueuePage /></MantineProvider>);

      const selectAllCheckbox = await screen.findByRole('checkbox', { name: 'Select all eligible scans for batch approval' });

      // Select all
      selectAllCheckbox.click();
      expect(selectAllCheckbox).toBeChecked();

      // Deselect all
      selectAllCheckbox.click();
      expect(selectAllCheckbox).not.toBeChecked();

      // Verify all individual checkboxes are also unchecked
      const cards = screen.getAllByRole('article');
      const cardCheckboxes = cards.map((card) => within(card).getByRole('checkbox'));
      cardCheckboxes.forEach((checkbox) => {
        expect(checkbox).not.toBeChecked();
      });
    });

    it('only shows eligible entries as eligible in each view', async () => {
      const fetchMock = vi.fn((input: RequestInfo | URL) => {
        const url = String(input);
        if (url.includes('status=pending')) {
          return Promise.resolve(jsonResponse([
            scanEntry({ id: 'stock-in-eligible', barcode: '111', status: 'pending', direction: 'stock_in' }),
            scanEntry({ id: 'stock-out-eligible', barcode: '222', status: 'pending', direction: 'stock_out' }),
            scanEntry({ id: 'undirected', barcode: '333', status: 'pending', direction: null }),
          ]));
        }
        if (url.includes('status=flagged') || url.includes('status=qr')) return Promise.resolve(jsonResponse([]));
        if (url === '/api/inventory' || url === '/api/products') return Promise.resolve(jsonResponse([]));
        if (url.endsWith('/api/scanner/config')) {
        return Promise.resolve(jsonResponse({ stockInBarcode: 'STOCK_IN', stockOutBarcode: 'STOCK_OUT', currentMode: 'stock_out' }));
      }
      throw new Error(`Unexpected request: ${url}`);
      });
      vi.stubGlobal('fetch', fetchMock);

      render(<MantineProvider><ScanQueuePage /></MantineProvider>);

      // Start in Stock_Out_View
      let selectAllCheckbox = await screen.findByRole('checkbox', { name: 'Select all eligible scans for batch approval' });
      
      // In Stock_Out_View, only the stock_out entry and undirected are shown
      // but only stock_out is eligible (undirected is not eligible because direction === null)
      let cards = screen.getAllByRole('article');
      // Filter to entries in Stock_Out_View (showing barcodes 222 and 333)
      cards = cards.filter((card) => {
        try {
          within(card).getByText(/Barcode: (222|333)/);
          return true;
        } catch {
          return false;
        }
      });
      expect(cards).toHaveLength(2);

      // Use Select_All_Control - should only select the stock_out entry
      selectAllCheckbox.click();

      // Find the cards again after click
      cards = screen.getAllByRole('article').filter((card) => {
        try {
          within(card).getByText(/Barcode: (222|333)/);
          return true;
        } catch {
          return false;
        }
      });

      // The first card should be the stock_out entry (222), second is undirected (333)
      let stockOutCheckbox: HTMLElement | null = null;
      let undirectedCheckbox: HTMLElement | null = null;
      for (const card of cards) {
        try {
          within(card).getByText('Barcode: 222');
          stockOutCheckbox = within(card).getByRole('checkbox');
        } catch {
          // not this card
        }
        try {
          within(card).getByText('Barcode: 333');
          undirectedCheckbox = within(card).getByRole('checkbox');
        } catch {
          // not this card
        }
      }

      expect(stockOutCheckbox).toBeChecked(); // eligible in this view
      expect(undirectedCheckbox).not.toBeChecked(); // ineligible (direction === null)

      // Switch to Stock_In_View
      const stockInTab = screen.getByRole('tab', { name: 'Stock in' });
      stockInTab.click();

      selectAllCheckbox = screen.getByRole('checkbox', { name: 'Select all eligible scans for batch approval' });
      
      // Wait for stock_in entry to appear and filter cards
      await findScan('111');
      cards = scansNamed(screen.getAllByRole('article'), ['111']);
      expect(cards).toHaveLength(1);

      const stockInCheckbox = within(cards[0]).getByRole('checkbox');
      expect(stockInCheckbox).not.toBeChecked(); // Not selected (selection was cleared on tab switch)

      // Select all in Stock_In_View
      selectAllCheckbox.click();
      expect(stockInCheckbox).toBeChecked(); // Now selected
    });
  });

  describe('Queue tab review-count badges', () => {
    const seedQueue = (entries: ScanEntry[]) =>
      vi.fn((input: RequestInfo | URL) => {
        const url = String(input);
        if (url.includes('status=pending')) return Promise.resolve(jsonResponse(entries));
        if (url.includes('status=flagged') || url.includes('status=qr')) return Promise.resolve(jsonResponse([]));
        if (url === '/api/inventory' || url === '/api/products') return Promise.resolve(jsonResponse([]));
        if (url.endsWith('/api/scanner/config')) {
          return Promise.resolve(jsonResponse({ stockInBarcode: 'STOCK_IN', stockOutBarcode: 'STOCK_OUT', currentMode: 'stock_out' }));
        }
        throw new Error(`Unexpected request: ${url}`);
      });

    it('shows a per-tab count badge for each direction, including null-direction entries under stock out', async () => {
      vi.stubGlobal('fetch', seedQueue([
        scanEntry({ id: 'in-1', barcode: '111', direction: 'stock_in' }),
        scanEntry({ id: 'out-1', barcode: '222', direction: 'stock_out' }),
        scanEntry({ id: 'null-1', barcode: '333', direction: null }),
      ]));

      render(<MantineProvider><ScanQueuePage /></MantineProvider>);

      const stockInTab = await screen.findByRole('tab', { name: /Stock in/ });
      const stockOutTab = screen.getByRole('tab', { name: /Stock out/ });
      // stock_in has one entry; stock_out is the catchall for stock_out + null.
      expect(within(stockInTab).getByText('1')).toBeInTheDocument();
      expect(within(stockOutTab).getByText('2')).toBeInTheDocument();
    });

    it('hides the badge for a direction with an empty queue but keeps the tab', async () => {
      vi.stubGlobal('fetch', seedQueue([
        scanEntry({ id: 'out-1', barcode: '222', direction: 'stock_out' }),
      ]));

      render(<MantineProvider><ScanQueuePage /></MantineProvider>);

      const stockInTab = await screen.findByRole('tab', { name: /Stock in/ });
      const stockOutTab = screen.getByRole('tab', { name: /Stock out/ });
      // Both tabs remain visible; only the empty stock_in tab has no badge.
      expect(stockInTab).toBeInTheDocument();
      expect(within(stockOutTab).getByText('1')).toBeInTheDocument();
      expect(within(stockInTab).queryByText(/\d/)).not.toBeInTheDocument();
    });

    it('caps a count above 99 as "99+"', async () => {
      const manyStockIn = Array.from({ length: 150 }, (_unused, index) =>
        scanEntry({ id: `in-${index}`, barcode: String(index), direction: 'stock_in' }));

      vi.stubGlobal('fetch', seedQueue(manyStockIn));

      render(<MantineProvider><ScanQueuePage /></MantineProvider>);

      const stockInTab = await screen.findByRole('tab', { name: /Stock in/ });
      expect(within(stockInTab).getByText('99+')).toBeInTheDocument();
    });

    it('shows exactly "99" at the cap boundary without the plus', async () => {
      const ninetyNineStockIn = Array.from({ length: 99 }, (_unused, index) =>
        scanEntry({ id: `in-${index}`, barcode: String(index), direction: 'stock_in' }));

      vi.stubGlobal('fetch', seedQueue(ninetyNineStockIn));

      render(<MantineProvider><ScanQueuePage /></MantineProvider>);

      const stockInTab = await screen.findByRole('tab', { name: /Stock in/ });
      expect(within(stockInTab).getByText('99')).toBeInTheDocument();
    });

    it('drops a tab badge live when its queue empties over the event stream', async () => {
      vi.stubGlobal('fetch', seedQueue([
        scanEntry({ id: 'in-1', barcode: '111', direction: 'stock_in' }),
        scanEntry({ id: 'out-1', barcode: '222', direction: 'stock_out' }),
      ]));

      render(<MantineProvider><ScanQueuePage /></MantineProvider>);

      const stockInTab = await screen.findByRole('tab', { name: /Stock in/ });
      expect(within(stockInTab).getByText('1')).toBeInTheDocument();

      // Committing the only stock_in entry removes it from the displayed list.
      const eventSource = FakeEventSource.instances[0];
      eventSource.dispatch('scan', scanEntry({ id: 'in-1', barcode: '111', direction: 'stock_in', status: 'committed' }));

      await vi.waitFor(() => expect(within(stockInTab).queryByText(/\d/)).not.toBeInTheDocument());
      expect(stockInTab).toBeInTheDocument();
    });
  });

  describe('scan sessions', () => {
    const product = (id: string, name: string): ScanEntry['product'] => ({
      id,
      name,
      category: 'Grocery',
      unitOfMeasure: 'bag',
    });

    it('groups a stock-in trip into an expanded session and selects only that session', async () => {
      const posted: unknown[] = [];
      const fetchMock = vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
        const url = String(input);
        if (url.includes('batch-commit') && init?.method === 'POST') {
          posted.push(JSON.parse(String(init.body)));
          return Promise.resolve(jsonResponse({}));
        }
        if (url.includes('status=pending')) {
          return Promise.resolve(jsonResponse([
            scanEntry({
              id: 'oats',
              barcode: '111',
              direction: 'stock_in',
              scannedAt: '2026-03-20T16:02:00Z',
              product: product('oats', 'Oats'),
              productId: 'oats',
            }),
            scanEntry({
              id: 'milk',
              barcode: '222',
              direction: 'stock_in',
              scannedAt: '2026-03-20T16:06:00Z',
              product: product('milk', 'Milk'),
              productId: 'milk',
            }),
            scanEntry({
              id: 'pasta',
              barcode: '333',
              direction: 'stock_in',
              scannedAt: '2026-03-20T14:40:00Z',
              product: product('pasta', 'Pasta'),
              productId: 'pasta',
            }),
            scanEntry({
              id: 'bread',
              barcode: '444',
              direction: 'stock_in',
              scannedAt: '2026-03-20T14:42:00Z',
              product: product('bread', 'Bread'),
              productId: 'bread',
            }),
            scanEntry({
              id: 'out',
              barcode: '999',
              direction: 'stock_out',
              scannedAt: '2026-03-20T16:04:00Z',
            }),
          ]));
        }
        if (url.includes('status=qr')) return Promise.resolve(jsonResponse([]));
        if (url.includes('status=flagged')) {
          return Promise.resolve(jsonResponse([
            scanEntry({
              id: 'flagged',
              barcode: '555',
              direction: 'stock_in',
              status: 'flagged',
              scannedAt: '2026-03-20T16:08:00Z',
            }),
          ]));
        }
        if (url === '/api/inventory' || url === '/api/products') return Promise.resolve(jsonResponse([]));
        if (url.endsWith('/api/scanner/config')) {
          return Promise.resolve(jsonResponse({ stockInBarcode: 'STOCK_IN', stockOutBarcode: 'STOCK_OUT', currentMode: 'stock_in' }));
        }
        throw new Error(`Unexpected request: ${url}`);
      });
      vi.stubGlobal('fetch', fetchMock);

      render(<MantineProvider><ScanQueuePage /></MantineProvider>);

      const oats = await screen.findByRole('article', { name: 'Scan 111' });
      const milk = screen.getByRole('article', { name: 'Scan 222' });
      expect(screen.getByRole('article', { name: 'Scan 555' })).toBeInTheDocument();
      expect(screen.queryByRole('article', { name: 'Scan 333' })).not.toBeInTheDocument();
      expect(screen.queryByRole('article', { name: 'Scan 999' })).not.toBeInTheDocument();
      expect(within(oats).getByLabelText('Unit count')).toBeInTheDocument();
      expect(within(oats).getByRole('button', { name: 'Increase unit count' })).toBeInTheDocument();
      expect(within(milk).getByLabelText('Unit count')).toBeInTheDocument();

      const sessions = screen.getAllByRole('region', { name: /Scan session/ });
      const expanded = sessions.find((session) => within(session).queryByRole('article', { name: 'Scan 111' }));
      expect(expanded).toBeDefined();
      const selectSession = within(expanded!).getByRole('checkbox', { name: /Select all eligible scans in/ });
      fireEvent.click(selectSession);

      expect(within(oats).getByRole('checkbox')).toBeChecked();
      expect(within(milk).getByRole('checkbox')).toBeChecked();
      expect(within(screen.getByRole('article', { name: 'Scan 555' })).queryByRole('checkbox')).not.toBeInTheDocument();

      fireEvent.click(selectSession);
      expect(within(oats).getByRole('checkbox')).not.toBeChecked();
      expect(within(milk).getByRole('checkbox')).not.toBeChecked();

      fireEvent.click(selectSession);
      fireEvent.click(screen.getByRole('button', { name: 'Confirm 2 selected scans' }));
      await vi.waitFor(() => expect(posted).toEqual([{ scanEntryIds: ['milk', 'oats'], commit: true }]));
      expect(await screen.findByRole('button', { name: 'Confirm 0 selected scans' })).toBeInTheDocument();

      fireEvent.click(screen.getByRole('checkbox', { name: 'Select all eligible scans for batch approval' }));
      fireEvent.click(screen.getByRole('button', { name: /2 scans/, expanded: false }));
      const pasta = await screen.findByRole('article', { name: 'Scan 333' });
      const bread = screen.getByRole('article', { name: 'Scan 444' });
      expect(within(pasta).getByRole('checkbox')).toBeChecked();
      expect(within(bread).getByRole('checkbox')).toBeChecked();
      expect(within(oats).getByRole('checkbox')).toBeChecked();
    });

    it('keeps newest-first rows on a fresh load and after a live insert, and hides barcodes in a collapsed session', async () => {
      const product = (id: string, name: string): ScanEntry['product'] => ({
        id,
        name,
        category: 'Grocery',
        unitOfMeasure: 'bag',
      });
      vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL) => {
        const url = String(input);
        if (url.includes('status=pending')) {
          return Promise.resolve(jsonResponse([
            scanEntry({
              id: 'oats',
              barcode: '111',
              direction: 'stock_in',
              scannedAt: '2026-03-20T16:00:00Z',
              product: product('oats', 'Oats'),
              productId: 'oats',
            }),
            scanEntry({
              id: 'milk',
              barcode: '222',
              direction: 'stock_in',
              scannedAt: '2026-03-20T16:02:00Z',
              product: product('milk', 'Milk'),
              productId: 'milk',
            }),
            scanEntry({
              id: 'bread',
              barcode: '333',
              direction: 'stock_in',
              scannedAt: '2026-03-20T16:04:00Z',
              product: product('bread', 'Bread'),
              productId: 'bread',
            }),
            scanEntry({
              id: 'yogurt',
              barcode: '444',
              direction: 'stock_in',
              scannedAt: '2026-03-20T15:00:00Z',
              product: product('yogurt', 'Yogurt'),
              productId: 'yogurt',
            }),
          ]));
        }
        if (url.includes('status=flagged') || url.includes('status=qr')) return Promise.resolve(jsonResponse([]));
        if (url === '/api/inventory' || url === '/api/products') return Promise.resolve(jsonResponse([]));
        if (url.endsWith('/api/scanner/config')) {
          return Promise.resolve(jsonResponse({ stockInBarcode: 'STOCK_IN', stockOutBarcode: 'STOCK_OUT', currentMode: 'stock_in' }));
        }
        throw new Error(`Unexpected request: ${url}`);
      }));

      render(<MantineProvider><ScanQueuePage /></MantineProvider>);

      const bread = await screen.findByRole('article', { name: 'Scan 333' });
      expect(screen.getAllByRole('article').map((article) => article.getAttribute('aria-label'))).toEqual([
        'Scan 333',
        'Scan 222',
        'Scan 111',
      ]);
      expect(within(bread).getByText('Barcode: 333')).toHaveClass('copyable-barcode');
      expect(screen.queryByRole('article', { name: 'Scan 444' })).not.toBeInTheDocument();
      expect(screen.queryByText('Barcode: 444')).not.toBeInTheDocument();

      FakeEventSource.instances[0].dispatch('scan', scanEntry({
        id: 'rice',
        barcode: '555',
        direction: 'stock_in',
        scannedAt: '2026-03-20T16:06:00Z',
        product: product('rice', 'Rice'),
        productId: 'rice',
      }));

      await screen.findByRole('article', { name: 'Scan 555' });
      expect(screen.getAllByRole('article').map((article) => article.getAttribute('aria-label'))).toEqual([
        'Scan 555',
        'Scan 333',
        'Scan 222',
        'Scan 111',
      ]);
      expect(within(screen.getByRole('article', { name: 'Scan 555' })).getByText('Barcode: 555')).toBeInTheDocument();
    });

    const sessionQueue = (pending: ScanEntry[]) => vi.fn((input: RequestInfo | URL) => {
      const url = String(input);
      if (url.includes('status=pending')) return Promise.resolve(jsonResponse(pending));
      if (url.includes('status=flagged') || url.includes('status=qr')) return Promise.resolve(jsonResponse([]));
      if (url === '/api/inventory' || url === '/api/products') return Promise.resolve(jsonResponse([]));
      if (url.endsWith('/api/scanner/config')) {
        return Promise.resolve(jsonResponse({ stockInBarcode: 'STOCK_IN', stockOutBarcode: 'STOCK_OUT', currentMode: 'stock_in' }));
      }
      throw new Error(`Unexpected request: ${url}`);
    });

    const articleOrder = () => screen.getAllByRole('article').map((article) => article.getAttribute('aria-label'));

    it('moves a rescanned card to the top of the open session and flashes it again', async () => {
      // The highlight clears on a 600ms timer. Step that clock explicitly: a
      // real sleep can return before the timer runs, and a later wait can
      // outlive the second flash.
      vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout'] });
      try {
        vi.stubGlobal('fetch', sessionQueue([
          scanEntry({
            id: 'oats', barcode: '111', direction: 'stock_in',
            scannedAt: '2026-03-20T16:00:00Z', unitCount: 1,
            product: product('oats', 'Oats'), productId: 'oats',
          }),
          scanEntry({
            id: 'milk', barcode: '222', direction: 'stock_in',
            scannedAt: '2026-03-20T16:02:00Z', unitCount: 1,
            product: product('milk', 'Milk'), productId: 'milk',
          }),
        ]));

        render(<MantineProvider><ScanQueuePage /></MantineProvider>);
        await act(async () => {
          await vi.advanceTimersByTimeAsync(0);
        });
        expect(articleOrder()).toEqual(['Scan 222', 'Scan 111']);

        act(() => {
          vi.advanceTimersByTime(600);
        });
        expect(screen.getByRole('article', { name: 'Scan 111' })).not.toHaveClass('scan-entry-card--changed-animated');

        act(() => {
          FakeEventSource.instances[0].dispatch('scan', scanEntry({
            id: 'oats',
            barcode: '111',
            direction: 'stock_in',
            scannedAt: '2026-03-20T16:04:00Z',
            unitCount: 2,
            product: product('oats', 'Oats'),
            productId: 'oats',
          }));
        });

        expect(articleOrder()).toEqual(['Scan 111', 'Scan 222']);
        const oats = screen.getByRole('article', { name: 'Scan 111' });
        expect(oats).toHaveClass('scan-entry-card--changed-animated');
        expect(screen.getByRole('article', { name: 'Scan 222' })).not.toHaveClass('scan-entry-card--changed-animated');
        expect(within(oats).getByLabelText('Unit count')).toHaveValue('2');
      } finally {
        vi.useRealTimers();
      }
    });

    it('pulls a rescanned card out of an older session to the top', async () => {
      vi.stubGlobal('fetch', sessionQueue([
        scanEntry({
          id: 'yogurt', barcode: '444', direction: 'stock_in',
          scannedAt: '2026-03-20T15:00:00Z', product: product('yogurt', 'Yogurt'), productId: 'yogurt',
        }),
        scanEntry({
          id: 'bread', barcode: '333', direction: 'stock_in',
          scannedAt: '2026-03-20T16:04:00Z', product: product('bread', 'Bread'), productId: 'bread',
        }),
      ]));

      render(<MantineProvider><ScanQueuePage /></MantineProvider>);
      await screen.findByRole('article', { name: 'Scan 333' });
      expect(screen.queryByRole('article', { name: 'Scan 444' })).not.toBeInTheDocument();

      FakeEventSource.instances[0].dispatch('scan', scanEntry({
        id: 'yogurt',
        barcode: '444',
        direction: 'stock_in',
        scannedAt: '2026-03-20T16:06:00Z',
        unitCount: 2,
        product: product('yogurt', 'Yogurt'),
        productId: 'yogurt',
      }));

      await screen.findByRole('article', { name: 'Scan 444' });
      expect(articleOrder()).toEqual(['Scan 444', 'Scan 333']);
      expect(screen.getByRole('article', { name: 'Scan 444' })).toHaveClass('scan-entry-card--changed-animated');
    });
  });

  describe('camera scanning', () => {
    const originalBarcodeDetector = window.BarcodeDetector;
    const originalPlay = HTMLMediaElement.prototype.play;
    const originalSecure = window.isSecureContext;

    afterEach(() => {
      if (originalBarcodeDetector === undefined) {
        delete window.BarcodeDetector;
      } else {
        window.BarcodeDetector = originalBarcodeDetector;
      }
      HTMLMediaElement.prototype.play = originalPlay;
      Object.defineProperty(window, 'isSecureContext', { configurable: true, value: originalSecure });
      vi.unstubAllGlobals();
      vi.restoreAllMocks();
    });

    function queueFetch(options: {
      mode?: 'stock_in' | 'stock_out';
      connected?: boolean;
      onScan?: (body: { barcode: string; direction?: string }) => ScanEntry;
      onMode?: (mode: string) => void;
    }) {
      const created: ScanEntry[] = [];
      return vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
        const url = String(input);
        if (url.endsWith('/api/scanner/mode') && init?.method === 'POST') {
          const body = init.body ? (JSON.parse(String(init.body)) as { mode: string }) : { mode: '' };
          options.onMode?.(body.mode);
          return Promise.resolve(jsonResponse({ mode: body.mode }));
        }
        if (url.endsWith('/api/scanner/config')) {
          return Promise.resolve(jsonResponse({
            stockInBarcode: 'STOCK_IN',
            stockOutBarcode: 'STOCK_OUT',
            currentMode: options.mode ?? 'stock_in',
            connected: options.connected ?? false,
          }));
        }
        if (url.endsWith('/api/scans') && init?.method === 'POST') {
          const body = init.body
            ? (JSON.parse(String(init.body)) as { barcode: string; direction?: string })
            : { barcode: '' };
          const entry = options.onScan?.(body) ?? scanEntry({
            id: 'created',
            barcode: body.barcode,
            direction: (body.direction ?? 'stock_in') as ScanEntry['direction'],
          });
          created.push(entry);
          return Promise.resolve(jsonResponse(entry));
        }
        if (url.includes('status=pending')) return Promise.resolve(jsonResponse(created));
        if (url.includes('status=flagged') || url.includes('status=qr')) return Promise.resolve(jsonResponse([]));
        if (url === '/api/inventory' || url === '/api/products') return Promise.resolve(jsonResponse([]));
        throw new Error(`Unexpected request: ${url}`);
      });
    }

    function installDetector(rawValue: string) {
      Object.defineProperty(window, 'isSecureContext', { configurable: true, value: true });
      const stop = vi.fn();
      const getUserMedia = vi.fn().mockResolvedValue({ getTracks: () => [{ stop }] });
      vi.stubGlobal('navigator', {
        ...navigator,
        mediaDevices: {
          getUserMedia,
          enumerateDevices: vi.fn().mockResolvedValue([]),
        },
      });
      HTMLMediaElement.prototype.play = vi.fn().mockResolvedValue(undefined);
      window.BarcodeDetector = vi.fn(function BarcodeDetector() {
        return { detect: vi.fn().mockResolvedValue([{ rawValue }]) };
      }) as unknown as typeof window.BarcodeDetector;
      return getUserMedia;
    }

    it('points a disconnected hardware scanner at the camera and does not open it yet', async () => {
      const getUserMedia = vi.fn();
      vi.stubGlobal('navigator', { ...navigator, mediaDevices: { getUserMedia } });
      vi.stubGlobal('fetch', queueFetch({ connected: false }));

      render(<MantineProvider><ScanQueuePage /></MantineProvider>);

      expect(await screen.findByText('Scanner disconnected')).toBeInTheDocument();
      expect(screen.getByText(/scan with this device's camera instead/i)).toBeInTheDocument();
      expect(screen.getByRole('button', { name: 'Scan with camera' })).toBeInTheDocument();
      expect(getUserMedia).not.toHaveBeenCalled();
    });

    it('enqueues a camera barcode in the current scanner mode', async () => {
      installDetector('0123456789012');
      const posts: Array<{ barcode: string; direction?: string }> = [];
      vi.stubGlobal('fetch', queueFetch({
        mode: 'stock_out',
        connected: false,
        onScan: (body) => {
          posts.push(body);
          return scanEntry({ id: 'created', barcode: body.barcode, direction: 'stock_out' });
        },
      }));

      render(<MantineProvider><ScanQueuePage /></MantineProvider>);
      await expectScannerMode('stock_out');

      fireEvent.click(screen.getByRole('button', { name: 'Scan with camera' }));

      expect(await screen.findByText('Barcode: 0123456789012')).toBeInTheDocument();
      expect(screen.getByRole('article', { name: 'Scan 0123456789012' })).toHaveClass('scan-entry-card--just-captured');
      expect(posts).toEqual([{ barcode: '0123456789012', direction: 'stock_out', userId: 'user-1' }]);
    });

    it('treats a camera read of a control barcode as a mode switch', async () => {
      installDetector('STOCK_IN');
      const posts: string[] = [];
      const modes: string[] = [];
      vi.stubGlobal('fetch', queueFetch({
        mode: 'stock_out',
        onScan: (body) => {
          posts.push(body.barcode);
          return scanEntry({ barcode: body.barcode });
        },
        onMode: (mode) => modes.push(mode),
      }));

      render(<MantineProvider><ScanQueuePage /></MantineProvider>);
      await expectScannerMode('stock_out');

      fireEvent.click(screen.getByRole('button', { name: 'Scan with camera' }));

      await expectScannerMode('stock_in');
      expect(modes).toContain('stock_in');
      expect(posts).not.toContain('STOCK_IN');
    });

    function installIdleCamera() {
      Object.defineProperty(window, 'isSecureContext', { configurable: true, value: true });
      const stop = vi.fn();
      const getUserMedia = vi.fn().mockResolvedValue({ getTracks: () => [{ stop }] });
      vi.stubGlobal('navigator', {
        ...navigator,
        mediaDevices: {
          getUserMedia,
          enumerateDevices: vi.fn().mockResolvedValue([]),
        },
      });
      HTMLMediaElement.prototype.play = vi.fn().mockResolvedValue(undefined);
      window.BarcodeDetector = vi.fn(function BarcodeDetector() {
        return { detect: vi.fn().mockResolvedValue([]) };
      }) as unknown as typeof window.BarcodeDetector;
    }

    it('selecting the Stock out tab sets the direction and tags the next scan with it', async () => {
      const modes: string[] = [];
      const posts: Array<{ barcode: string; direction?: string; userId?: string }> = [];
      vi.stubGlobal('fetch', queueFetch({
        mode: 'stock_in',
        onMode: (mode) => modes.push(mode),
        onScan: (body) => {
          posts.push(body);
          return scanEntry({
            id: 'created',
            barcode: body.barcode,
            direction: (body.direction ?? null) as ScanEntry['direction'],
          });
        },
      }));

      render(<MantineProvider><ScanQueuePage /></MantineProvider>);
      await expectScannerMode('stock_in');
      expect(screen.queryByRole('radiogroup', { name: 'Scanning mode' })).not.toBeInTheDocument();
      expect(screen.queryByText('Scanning mode')).not.toBeInTheDocument();

      fireEvent.click(screen.getByRole('tab', { name: 'Stock out' }));

      await expectScannerMode('stock_out');
      expect(modes).toEqual(['stock_out']);
      expect(screen.getByText('Scans remove from stock')).toBeInTheDocument();
      expect(screen.getByText('Mode: stock_out')).toBeInTheDocument();

      const input = screen.getByLabelText(/barcode scanner input/i);
      fireEvent.change(input, { target: { value: '0123456789012' } });
      fireEvent.keyDown(input, { key: 'Enter' });

      await waitFor(() => {
        expect(posts).toEqual([{ barcode: '0123456789012', direction: 'stock_out', userId: 'user-1' }]);
      });
    });

    it('keeps the camera open while the Stock out tab changes the direction', async () => {
      installIdleCamera();
      const modes: string[] = [];
      vi.stubGlobal('fetch', queueFetch({
        mode: 'stock_in',
        onMode: (mode) => modes.push(mode),
      }));

      render(<MantineProvider><ScanQueuePage /></MantineProvider>);
      await expectScannerMode('stock_in');

      fireEvent.click(screen.getByRole('button', { name: 'Scan with camera' }));
      expect(await screen.findByLabelText('Camera preview')).toBeInTheDocument();

      fireEvent.click(screen.getByRole('tab', { name: 'Stock out' }));

      await expectScannerMode('stock_out');
      expect(modes).toEqual(['stock_out']);
      expect(screen.getByLabelText('Camera preview')).toBeInTheDocument();
      expect(screen.queryByRole('button', { name: 'Scan with camera' })).not.toBeInTheDocument();
      expect(screen.queryByRole('radiogroup', { name: 'Scanning mode' })).not.toBeInTheDocument();
      expect(screen.getByText('Mode: stock_out')).toBeInTheDocument();

      FakeEventSource.instances[0].dispatch('scanner_mode', 'stock_in');

      await expectScannerMode('stock_in');
      expect(screen.getByText('Scans add to stock')).toBeInTheDocument();
      expect(screen.getByLabelText('Camera preview')).toBeInTheDocument();

      fireEvent.click(screen.getByRole('button', { name: 'Stop camera' }));

      expect(screen.queryByLabelText('Camera preview')).not.toBeInTheDocument();
      expect(screen.queryByRole('radiogroup', { name: 'Scanning mode' })).not.toBeInTheDocument();
      await expectScannerMode('stock_in');
    });

    it('shows the server error when a mode switch cannot be saved', async () => {
      vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
        const url = String(input);
        if (url.endsWith('/api/scanner/mode') && init?.method === 'POST') {
          return Promise.resolve(new Response(JSON.stringify({ error: 'The scanner mode could not be saved.' }), {
            status: 500,
            headers: { 'Content-Type': 'application/json' },
          }));
        }
        if (url.endsWith('/api/scanner/config')) {
          return Promise.resolve(jsonResponse({
            stockInBarcode: 'STOCK_IN',
            stockOutBarcode: 'STOCK_OUT',
            currentMode: 'stock_in',
          }));
        }
        if (url.includes('status=pending') || url.includes('status=flagged') || url.includes('status=qr')) {
          return Promise.resolve(jsonResponse([]));
        }
        if (url === '/api/inventory' || url === '/api/products') return Promise.resolve(jsonResponse([]));
        throw new Error(`Unexpected request: ${url}`);
      }));

      render(<MantineProvider><ScanQueuePage /></MantineProvider>);
      await expectScannerMode('stock_in');

      fireEvent.click(screen.getByRole('tab', { name: 'Stock out' }));

      expect(await screen.findByText('The scanner mode could not be saved.')).toBeInTheDocument();
      expect(screen.getByRole('tab', { name: 'Stock out' })).toHaveAttribute('aria-selected', 'true');
    });
  });

  describe('quantity changes', () => {
    const oats = scanEntry({
      id: 'oats',
      barcode: '111',
      direction: 'stock_in',
      unitCount: 1,
      productId: 'oats',
      product: { id: 'oats', name: 'Oats', category: 'Grocery', unitOfMeasure: 'bag' },
    });
    const milk = scanEntry({
      id: 'milk',
      barcode: '222',
      direction: 'stock_out',
      unitCount: 1,
      productId: 'milk',
      product: { id: 'milk', name: 'Milk', category: 'Dairy', unitOfMeasure: 'carton' },
    });

    it('shows the queue loader only before the first load returns', async () => {
      let releasePending: (response: Response) => void = () => {};
      const pending = new Promise<Response>((resolve) => {
        releasePending = resolve;
      });
      vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL) => {
        const url = String(input);
        if (url.includes('status=pending')) return pending;
        if (url.includes('status=flagged') || url.includes('status=qr')) return Promise.resolve(jsonResponse([]));
        if (url === '/api/inventory' || url === '/api/products') return Promise.resolve(jsonResponse([]));
        if (url.endsWith('/api/scanner/config')) {
          return Promise.resolve(jsonResponse({ stockInBarcode: 'STOCK_IN', stockOutBarcode: 'STOCK_OUT', currentMode: 'stock_in' }));
        }
        throw new Error(`Unexpected request: ${url}`);
      }));

      render(<MantineProvider><ScanQueuePage /></MantineProvider>);

      expect(screen.getByLabelText('Loading scan queue')).toBeInTheDocument();
      await act(async () => {
        releasePending(jsonResponse([]));
      });
      expect(await screen.findByText('No pending scans.')).toBeInTheDocument();
      expect(screen.queryByLabelText('Loading scan queue')).not.toBeInTheDocument();
    });

    const renderQueue = (entry: ScanEntry, mode: 'stock_in' | 'stock_out') => {
      let pendingLoads = 0;
      let releaseRefresh: (response: Response) => void = () => {};
      const refresh = new Promise<Response>((resolve) => {
        releaseRefresh = resolve;
      });
      const patches: unknown[] = [];
      const fetchMock = vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
        const url = String(input);
        if (url === `/api/scans/${entry.id}` && init?.method === 'PATCH') {
          const body = JSON.parse(String(init.body)) as { unitCount: number };
          patches.push(body);
          return Promise.resolve(jsonResponse({ ...entry, unitCount: body.unitCount }));
        }
        if (url.includes('status=pending')) {
          pendingLoads += 1;
          if (pendingLoads === 1) return Promise.resolve(jsonResponse([entry]));
          return refresh;
        }
        if (url.includes('status=flagged') || url.includes('status=qr')) return Promise.resolve(jsonResponse([]));
        if (url === '/api/inventory' || url === '/api/products') return Promise.resolve(jsonResponse([]));
        if (url.endsWith('/api/scanner/config')) {
          return Promise.resolve(jsonResponse({ stockInBarcode: 'STOCK_IN', stockOutBarcode: 'STOCK_OUT', currentMode: mode }));
        }
        throw new Error(`Unexpected request: ${url}`);
      });
      vi.stubGlobal('fetch', fetchMock);
      render(<MantineProvider><ScanQueuePage /></MantineProvider>);
      return { patches, releaseRefresh, pendingLoads: () => pendingLoads };
    };

    it('updates a stock-in count on tap without showing the queue loader', async () => {
      const { patches, releaseRefresh, pendingLoads } = renderQueue(oats, 'stock_in');
      const card = await screen.findByRole('article', { name: 'Scan 111' });
      expect(screen.queryByLabelText('Loading scan queue')).not.toBeInTheDocument();
      expect(screen.getByRole('region', { name: /Scan session/ })).toBeInTheDocument();

      fireEvent.click(within(card).getByRole('button', { name: 'Increase unit count' }));

      expect(within(card).getByLabelText('Unit count')).toHaveValue('2');
      expect(screen.queryByLabelText('Loading scan queue')).not.toBeInTheDocument();

      await vi.waitFor(() => expect(patches).toEqual([{ unitCount: 2 }]));
      await vi.waitFor(() => expect(pendingLoads()).toBeGreaterThan(1));
      expect(within(card).getByLabelText('Unit count')).toHaveValue('2');
      expect(screen.queryByLabelText('Loading scan queue')).not.toBeInTheDocument();
      expect(screen.getByRole('article', { name: 'Scan 111' })).toBeInTheDocument();

      await act(async () => {
        releaseRefresh(jsonResponse([{ ...oats, unitCount: 2 }]));
      });
      expect(screen.queryByLabelText('Loading scan queue')).not.toBeInTheDocument();
      expect(within(card).getByLabelText('Unit count')).toHaveValue('2');
    });

    it('updates a stock-out count on tap without showing the queue loader', async () => {
      const { patches, releaseRefresh, pendingLoads } = renderQueue(milk, 'stock_out');
      const card = await screen.findByRole('article', { name: 'Scan 222' });
      expect(screen.queryByLabelText('Loading scan queue')).not.toBeInTheDocument();
      expect(screen.getByRole('button', { name: 'More' })).toBeInTheDocument();

      fireEvent.click(within(card).getByRole('button', { name: 'Increase unit count' }));

      expect(within(card).getByLabelText('Unit count')).toHaveValue('2');
      expect(screen.queryByLabelText('Loading scan queue')).not.toBeInTheDocument();

      await vi.waitFor(() => expect(patches).toEqual([{ unitCount: 2 }]));
      await vi.waitFor(() => expect(pendingLoads()).toBeGreaterThan(1));
      expect(within(card).getByLabelText('Unit count')).toHaveValue('2');
      expect(screen.queryByLabelText('Loading scan queue')).not.toBeInTheDocument();
      expect(screen.getByRole('region', { name: /Scan session/ })).toBeInTheDocument();

      await act(async () => {
        releaseRefresh(jsonResponse([{ ...milk, unitCount: 2 }]));
      });
      expect(screen.queryByLabelText('Loading scan queue')).not.toBeInTheDocument();
      expect(within(card).getByLabelText('Unit count')).toHaveValue('2');
    });
  });
});

describe('QR misread', () => {
  beforeEach(() => {
    FakeEventSource.instances = [];
    vi.stubGlobal('EventSource', FakeEventSource);
  });

  it('shows the QR sentence, the domain, and rescan or dismiss', async () => {
    const fetchMock = vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url.includes('status=qr')) {
        return Promise.resolve(jsonResponse([
          scanEntry({
            id: 'qr-1',
            barcode: 'https://example.com/groceries/milk',
            status: 'qr',
            direction: 'stock_in',
            productId: null,
            product: null,
          }),
        ]));
      }
      if (url.includes('status=pending') || url.includes('status=flagged')) {
        return Promise.resolve(jsonResponse([]));
      }
      if (url === `/api/scans/qr-1` && init?.method === 'PATCH') {
        expect(JSON.parse(String(init.body))).toEqual({ status: 'cancelled' });
        return Promise.resolve(jsonResponse(scanEntry({
          id: 'qr-1',
          barcode: 'https://example.com/groceries/milk',
          status: 'cancelled',
          direction: 'stock_in',
        })));
      }
      if (url === '/api/inventory' || url === '/api/products') return Promise.resolve(jsonResponse([]));
      if (url.endsWith('/api/scanner/config')) {
        return Promise.resolve(jsonResponse({
          stockInBarcode: 'STOCK_IN',
          stockOutBarcode: 'STOCK_OUT',
          currentMode: 'stock_in',
        }));
      }
      throw new Error(`Unexpected request: ${url}`);
    });
    vi.stubGlobal('fetch', fetchMock);

    render(<MantineProvider><ScanQueuePage /></MantineProvider>);

    const card = await screen.findByRole('article', { name: /This was a QR code, not the barcode/ });
    expect(within(card).getByText('This was a QR code, not the barcode')).toBeInTheDocument();
    expect(within(card).getByText('example.com')).toBeInTheDocument();
    expect(within(card).queryByRole('checkbox')).not.toBeInTheDocument();
    expect(screen.queryByRole('checkbox', { name: 'Select all eligible scans for batch approval' })).not.toBeInTheDocument();

    fireEvent.click(within(card).getByRole('button', { name: 'Rescan the barcode' }));
    expect(screen.getByRole('textbox', { name: 'Barcode scanner input' })).toHaveFocus();

    fireEvent.click(within(card).getByRole('button', { name: 'Dismiss' }));
    await waitFor(() => expect(fetchMock).toHaveBeenCalledWith(
      '/api/scans/qr-1',
      expect.objectContaining({ method: 'PATCH' }),
    ));
  });
});
