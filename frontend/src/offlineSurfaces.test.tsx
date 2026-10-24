import { act, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { MantineProvider } from '@mantine/core';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { MemoryRouter } from 'react-router-dom';
import App from './App';
import { BUSY_SENTENCE, NO_SPACE_SENTENCE, OFFLINE_SENTENCE, UPDATING_SENTENCE, applyHttpResponse, notifyApiSuccess, raiseAuthNotConfiguredBanner, raiseOfflineBanner } from '@stockbot/shared/contract';
import { ScanQueuePage } from './components/queue/ScanQueuePage';
import { ServerNoticeBanner } from './components/shell/ServerNoticeBanner';
import type { ScanEntry } from '@stockbot/shared/types';

class FakeEventSource {
  readonly url: string;
  constructor(url: string) {
    this.url = url;
  }
  addEventListener() {}
  close() {}
}

const json = (body: unknown, status = 200) =>
  new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } });

const scan = (id: string, barcode: string): ScanEntry => ({
  id,
  userId: 'user-1',
  barcode,
  scannedAt: '2026-03-20T16:02:00Z',
  direction: 'stock_out',
  unitCount: 3,
  expiresAt: null,
  status: 'pending',
  productId: `prod-${id}`,
  product: { id: `prod-${id}`, name: barcode === '111' ? 'Milk' : 'Oats', category: 'Grocery', unitOfMeasure: 'carton' },
  committedAt: null,
  createdAt: '2026-03-20T16:02:00Z',
});

const renderQueue = () => render(
  <MantineProvider>
    <MemoryRouter>
      <ScanQueuePage />
    </MemoryRouter>
  </MantineProvider>,
);

describe('offline surfaces', () => {
  it('shows one banner on the scan queue', async () => {
    vi.stubGlobal('EventSource', FakeEventSource);
    window.history.pushState({}, '', '/');
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL) => {
      const url = String(input);
      if (url.includes('/api/session')) return Promise.resolve(json({ required: false }));
      return Promise.reject(new TypeError('Failed to fetch'));
    }));
    render(
      <MantineProvider>
        <App />
      </MantineProvider>,
    );
    expect(await screen.findByRole('heading', { name: 'Scan queue' })).toBeInTheDocument();
    expect(await screen.findByText(OFFLINE_SENTENCE)).toBeVisible();
    expect(screen.getAllByTestId('server-notice-banner')).toHaveLength(1);
  });

  it('shows updating in the banner until the next success', () => {
    render(
      <MantineProvider>
        <ServerNoticeBanner />
      </MantineProvider>,
    );
    act(() => {
      applyHttpResponse(503, 'updating');
    });
    expect(screen.getByTestId('server-notice-banner')).toHaveTextContent(UPDATING_SENTENCE);
    act(() => {
      notifyApiSuccess();
    });
    expect(screen.queryByTestId('server-notice-banner')).not.toBeInTheDocument();
  });

  it('lets no_space replace updating and offline replace the queue sentence', () => {
    render(
      <MantineProvider>
        <ServerNoticeBanner text={NO_SPACE_SENTENCE} />
      </MantineProvider>,
    );
    act(() => {
      applyHttpResponse(503, 'updating');
    });
    expect(screen.getByText(UPDATING_SENTENCE)).toBeVisible();
    act(() => {
      applyHttpResponse(503, 'no_space');
    });
    expect(screen.queryByText(UPDATING_SENTENCE)).not.toBeInTheDocument();
    expect(screen.getByText(NO_SPACE_SENTENCE)).toBeVisible();
    act(() => {
      applyHttpResponse(503, '');
    });
    expect(screen.getByText(OFFLINE_SENTENCE)).toBeVisible();
    expect(screen.queryByText(NO_SPACE_SENTENCE)).not.toBeInTheDocument();
  });

  it('replaces the auth sentence with the busy sentence', () => {
    raiseAuthNotConfiguredBanner();
    render(
      <MantineProvider>
        <ServerNoticeBanner text={BUSY_SENTENCE} />
      </MantineProvider>,
    );
    expect(screen.getByText("Sign-in isn't set up yet.")).toBeVisible();
    act(() => {
      applyHttpResponse(503, 'busy');
    });
    expect(screen.queryByText("Sign-in isn't set up yet.")).not.toBeInTheDocument();
    expect(screen.getByText(BUSY_SENTENCE)).toBeVisible();
  });

  afterEach(() => {
    vi.unstubAllGlobals();
    window.history.pushState({}, '', '/');
  });

  it('shows the shell banner on inventory and skips the empty sentence', async () => {
    vi.stubGlobal('EventSource', FakeEventSource);
    window.history.pushState({}, '', '/inventory');
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL) => {
      const url = String(input);
      if (url.includes('/api/session')) return Promise.resolve(json({ required: false }));
      return Promise.reject(new TypeError('Failed to fetch'));
    }));

    render(
      <MantineProvider>
        <App />
      </MantineProvider>,
    );

    expect(await screen.findByRole('heading', { name: 'Inventory' })).toBeInTheDocument();
    expect(await screen.findByTestId('server-notice-banner')).toHaveTextContent(OFFLINE_SENTENCE);
    await waitFor(() => expect(screen.queryByLabelText('Loading inventory')).not.toBeInTheDocument());
    expect(screen.queryByText('Your inventory is empty.')).not.toBeInTheDocument();
    expect(screen.queryByText('Unable to load inventory.')).not.toBeInTheDocument();
    expect(screen.getAllByText(OFFLINE_SENTENCE)).toHaveLength(1);

    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL) => {
      const url = String(input);
      if (url.includes('/api/session')) return Promise.resolve(json({ required: false }));
      return Promise.resolve(json([]));
    }));
    notifyApiSuccess();
    expect(await screen.findByText('Your inventory is empty.')).toBeInTheDocument();
    expect(screen.queryByText(OFFLINE_SENTENCE)).not.toBeInTheDocument();
  });

  it('hides the queue load error while offline', async () => {
    vi.stubGlobal('EventSource', FakeEventSource);
    vi.stubGlobal('fetch', vi.fn(() => Promise.reject(new TypeError('Failed to fetch'))));

    renderQueue();

    expect(await screen.findByTestId('server-notice-banner')).toHaveTextContent(OFFLINE_SENTENCE);
    expect(screen.queryByText('Unable to load scan queue.')).not.toBeInTheDocument();
    expect(screen.queryByText('No pending scans.')).not.toBeInTheDocument();
    expect(screen.queryByText('Failed to fetch')).not.toBeInTheDocument();
    expect(screen.getAllByText(OFFLINE_SENTENCE)).toHaveLength(1);
  });

  it('drops the disconnected line while offline', async () => {
    vi.stubGlobal('EventSource', FakeEventSource);
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL) => {
      const url = String(input);
      if (url.endsWith('/api/scanner/config')) {
        return Promise.resolve(json({
          stockInBarcode: 'STOCK_IN',
          stockOutBarcode: 'STOCK_OUT',
          currentMode: 'stock_in',
          connected: false,
        }));
      }
      if (url.includes('status=')) return Promise.resolve(json([]));
      if (url === '/api/inventory' || url.startsWith('/api/inventory?') || url.startsWith('/api/products')) {
        return Promise.resolve(json([]));
      }
      return Promise.resolve(json([]));
    }));

    renderQueue();
    expect(await screen.findByText('No pending scans.')).toBeInTheDocument();
    expect(screen.getByText('Scanner disconnected')).toBeInTheDocument();
    raiseOfflineBanner();
    await waitFor(() => expect(screen.queryByText('Scanner disconnected')).not.toBeInTheDocument());
    expect(screen.getByTestId('server-notice-banner')).toHaveTextContent(OFFLINE_SENTENCE);
  });

  it('hides a partial line while offline', async () => {
    vi.stubGlobal('EventSource', FakeEventSource);
    const partial = 'Only 1 of 3 Milk was on hand, so 1 was taken out.';
    const pending = [scan('scan-milk', '111'), scan('scan-oats', '222')];
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      const method = init?.method ?? 'GET';
      if (url.includes('batch-commit') && method === 'POST') {
        return Promise.resolve(json({
          updatedCount: 0,
          committedIds: [],
          failedIds: [],
          stockOutCounts: { 'scan-milk': { removedCount: 1, requestedCount: 3 } },
        }));
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
          currentMode: 'stock_out',
          connected: true,
        }));
      }
      return Promise.reject(new Error(`Unexpected ${method} ${url}`));
    }));

    renderQueue();
    await screen.findByRole('article', { name: /^Scan 111(,|$)/ });
    fireEvent.click(screen.getByRole('checkbox', { name: 'Select all eligible scans for batch approval' }));
    fireEvent.click(screen.getByRole('button', { name: 'Confirm 2 selected scans' }));
    expect(await screen.findByText(partial)).toBeInTheDocument();

    raiseOfflineBanner();
    await waitFor(() => expect(screen.queryByText(partial)).not.toBeInTheDocument());
    expect(screen.getByTestId('server-notice-banner')).toHaveTextContent(OFFLINE_SENTENCE);
    expect(screen.queryByText('Unable to load scan queue.')).not.toBeInTheDocument();
  });
});
