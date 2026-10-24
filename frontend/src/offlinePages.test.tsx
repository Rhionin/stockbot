import type { ReactNode } from 'react';
import { fireEvent, render, screen, within } from '@testing-library/react';
import { MantineProvider } from '@mantine/core';
import { MemoryRouter, Route, Routes } from 'react-router-dom';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { OFFLINE_SENTENCE, notifyApiSuccess } from '@stockbot/shared/contract';
import { HouseholdSessionContext } from './components/auth/householdSession';
import { DiagnosticsPage } from './components/diagnostics/DiagnosticsPage';
import { HistoryPage } from './components/history/HistoryPage';
import { LedgerPage } from './components/history/LedgerPage';
import { InventoryPage } from './components/inventory/InventoryPage';
import { ScanQueuePage } from './components/queue/ScanQueuePage';
import { SettingsPage } from './components/settings/SettingsPage';
import { AppMenu } from './components/shell/AppMenu';
import { ServerNoticeBanner } from './components/shell/ServerNoticeBanner';
import { CredentialsDialog } from './components/shopping/CredentialsDialog';
import { ProvisionButton } from './components/shopping/ProvisionButton';
import { ShoppingListPage } from './components/shopping/ShoppingListPage';
import type { ProviderInfo, ScanEntry, ShoppingListEntry } from '@stockbot/shared/types';

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
  direction: 'stock_in',
  unitCount: 1,
  expiresAt: null,
  status: 'pending',
  productId: `prod-${id}`,
  product: { id: `prod-${id}`, name: 'Milk', category: 'Grocery', unitOfMeasure: 'carton' },
  committedAt: null,
  createdAt: '2026-03-20T16:02:00Z',
});

const provider = (): ProviderInfo => ({
  id: 'kroger',
  displayName: 'Kroger',
  capabilities: {
    auth: 'oauth2_authorization_code',
    delivery: 'server_push',
    confirmation: 'per_request',
    mutation: 'add_only',
    identity: 'derived',
  },
  connectionState: 'disconnected',
  credentialsConfigured: false,
});

const shoppingEntry = (): ShoppingListEntry => ({
  id: 'sli-1',
  itemId: 'item-1',
  quantity: 2,
  source: 'manual',
  purchasedAt: null,
});

function shell(node: ReactNode, banner = true) {
  return render(
    <MantineProvider>
      <MemoryRouter>
        {banner && <ServerNoticeBanner />}
        {node}
      </MemoryRouter>
    </MantineProvider>,
  );
}

function expectOnce() {
  expect(screen.getAllByText(OFFLINE_SENTENCE)).toHaveLength(1);
  expect(screen.queryByText('Failed to fetch')).not.toBeInTheDocument();
}

describe('offline sentence appears once on each page', () => {
  beforeEach(() => {
    vi.stubGlobal('EventSource', FakeEventSource);
  });

  afterEach(() => {
    notifyApiSuccess();
    vi.unstubAllGlobals();
  });

  it.each([
    {
      name: 'confirm',
      run: async () => {
        const pending = [scan('scan-milk', '111')];
        vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
          const url = String(input);
          const method = init?.method ?? 'GET';
          if (url.endsWith('/commit') && method === 'POST') return Promise.reject(new TypeError('Failed to fetch'));
          if (url.includes('status=pending')) return Promise.resolve(json(pending));
          if (url.includes('status=flagged') || url.includes('status=qr')) return Promise.resolve(json([]));
          if (url.includes('/api/inventory') || url.startsWith('/api/products') || url.endsWith('/api/scanner/config')) {
            return Promise.resolve(json(url.endsWith('/api/scanner/config')
              ? { stockInBarcode: 'STOCK_IN', stockOutBarcode: 'STOCK_OUT', currentMode: 'stock_in', connected: true }
              : []));
          }
          return Promise.resolve(json([]));
        }));
        shell(<ScanQueuePage />, false);
        const card = await screen.findByRole('article', { name: /^Scan 111(,|$)/ });
        fireEvent.click(within(card).getByRole('button', { name: 'Confirm' }));
        expect(await screen.findByText(OFFLINE_SENTENCE)).toBeInTheDocument();
        expectOnce();
      },
    },
    {
      name: 'LedgerPage',
      run: async () => {
        vi.stubGlobal('fetch', vi.fn(() => Promise.reject(new TypeError('Failed to fetch'))));
        shell(<LedgerPage />);
        expect(await screen.findByText(OFFLINE_SENTENCE)).toBeInTheDocument();
        expectOnce();
      },
    },
    {
      name: 'HistoryPage',
      run: async () => {
        vi.stubGlobal('fetch', vi.fn(() => Promise.reject(new TypeError('Failed to fetch'))));
        render(
          <MantineProvider>
            <MemoryRouter initialEntries={['/inventory/item-1/history']}>
              <ServerNoticeBanner />
              <Routes>
                <Route path="/inventory/:itemId/history" element={<HistoryPage />} />
              </Routes>
            </MemoryRouter>
          </MantineProvider>,
        );
        expect(await screen.findByText(OFFLINE_SENTENCE)).toBeInTheDocument();
        expectOnce();
      },
    },
    {
      name: 'SettingsPage',
      run: async () => {
        vi.stubGlobal('fetch', vi.fn(() => Promise.reject(new TypeError('Failed to fetch'))));
        shell(<SettingsPage />);
        expect(await screen.findByText(OFFLINE_SENTENCE)).toBeInTheDocument();
        expectOnce();
      },
    },
    {
      name: 'AppMenu',
      run: async () => {
        vi.stubGlobal('fetch', vi.fn(() => Promise.reject(new TypeError('Failed to fetch'))));
        shell(
          <HouseholdSessionContext.Provider value={{ required: false, logout: () => undefined, auth: null, replaceAuth: () => undefined }}>
            <AppMenu onCredentialsChanged={() => undefined} />
          </HouseholdSessionContext.Provider>,
        );
        expect(await screen.findByText(OFFLINE_SENTENCE)).toBeInTheDocument();
        fireEvent.click(screen.getByRole('button', { name: 'Menu' }));
        expect(await screen.findByRole('menuitem', { name: 'Diagnostics' })).toBeInTheDocument();
        expect(screen.queryByText('Unable to load store settings.')).not.toBeInTheDocument();
        expectOnce();
      },
    },
    {
      name: 'ShoppingListPage',
      run: async () => {
        vi.stubGlobal('fetch', vi.fn(() => Promise.reject(new TypeError('Failed to fetch'))));
        shell(<ShoppingListPage />);
        expect(await screen.findByText(OFFLINE_SENTENCE)).toBeInTheDocument();
        expectOnce();
      },
    },
    {
      name: 'CredentialsDialog',
      run: async () => {
        vi.stubGlobal('fetch', vi.fn(() => Promise.reject(new TypeError('Failed to fetch'))));
        shell(
          <CredentialsDialog provider={provider()} opened onClose={() => undefined} onChanged={() => undefined} />,
        );
        fireEvent.click(screen.getByRole('button', { name: 'Save credentials' }));
        expect(await screen.findByText(OFFLINE_SENTENCE)).toBeInTheDocument();
        expectOnce();
      },
    },
    {
      name: 'ProvisionButton',
      run: async () => {
        vi.stubGlobal('fetch', vi.fn(() => Promise.reject(new TypeError('Failed to fetch'))));
        shell(<ProvisionButton provider={null} entries={[shoppingEntry()]} onFinished={() => undefined} />);
        fireEvent.click(screen.getByRole('button', { name: 'Send to Kroger' }));
        expect(await screen.findByText(OFFLINE_SENTENCE)).toBeInTheDocument();
        expectOnce();
      },
    },
    {
      name: 'Diagnostics',
      run: async () => {
        vi.stubGlobal('fetch', vi.fn(() => Promise.reject(new TypeError('Failed to fetch'))));
        shell(<DiagnosticsPage />);
        expect(await screen.findByText(OFFLINE_SENTENCE)).toBeInTheDocument();
        expectOnce();
      },
    },
    {
      name: 'Inventory',
      run: async () => {
        vi.stubGlobal('fetch', vi.fn(() => Promise.reject(new TypeError('Failed to fetch'))));
        shell(<InventoryPage />);
        expect(await screen.findByText(OFFLINE_SENTENCE)).toBeInTheDocument();
        expect(screen.queryByText('Your inventory is empty.')).not.toBeInTheDocument();
        expectOnce();
      },
    },
  ])('$name', async ({ run }) => {
    await run();
  });
});
