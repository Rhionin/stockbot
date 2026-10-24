import { fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { MantineProvider } from '@mantine/core';
import { Notifications } from '@mantine/notifications';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import type { InventoryItem, ShoppingListEntry } from '@stockbot/shared/types';
import { getSession, resetStage1Client } from '../../api/client';
import { HOUSEHOLD_SWITCH_EVENT } from '../household/liveHints';
import { ShoppingListPage } from './ShoppingListPage';

const inventoryItem = (id: string, name: string, targetQuantity: number | null): InventoryItem => ({
  item: {
    id,
    userId: 'user-1',
    productId: `product-${id}`,
    product: { id: `product-${id}`, name, category: 'Pantry', unitOfMeasure: 'boxes' },
    targetQuantity,
    createdAt: '2026-01-01T00:00:00Z',
  },
  instanceCount: 1,
  nearExpiryCount: 0,
  expiredCount: 0,
  needsAttention: false,
});

const jsonResponse = (body: unknown, status = 200) =>
  new Response(JSON.stringify(body), {
    status,
    statusText: status >= 400 ? 'Request failed' : 'OK',
    headers: { 'Content-Type': 'application/json' },
  });

const renderPage = () => render(
  <MantineProvider>
    <Notifications />
    <ShoppingListPage />
  </MantineProvider>,
);

beforeEach(() => {
  // A real transition timeout can fire after this file's jsdom is gone.
  vi.stubGlobal(
    'matchMedia',
    (query: string) =>
      ({
        matches: /prefers-reduced-motion/.test(query),
        media: query,
        onchange: null,
        addListener: () => {},
        removeListener: () => {},
        addEventListener: () => {},
        removeEventListener: () => {},
        dispatchEvent: () => false,
      }) as unknown as MediaQueryList,
  );
});

describe('ShoppingListPage', () => {
  it('renders derived and manual entries with product details and marks a manual item purchased', async () => {
    const initialEntries: ShoppingListEntry[] = [
      { id: '', itemId: 'rice', quantity: 2, source: 'auto', purchasedAt: null, note: 'replacing 2 you used' },
      { id: 'manual-1', itemId: 'tea', quantity: 3, source: 'manual', purchasedAt: null },
    ];
    let purchased = false;
    const fetchMock = vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url === '/api/providers') return Promise.resolve(jsonResponse([]));
      if (url === '/api/shopping-list' && init?.method === undefined) {
        return Promise.resolve(jsonResponse(purchased ? initialEntries.slice(0, 1) : initialEntries));
      }
      if (url === '/api/inventory') {
        return Promise.resolve(jsonResponse([
          inventoryItem('rice', 'Brown Rice', 3),
          inventoryItem('tea', 'Green Tea', null),
        ]));
      }
      if (url === '/api/shopping-list/items/manual-1') {
        expect(init?.method).toBe('PATCH');
        expect(init?.body).toBe(JSON.stringify({ purchased: true }));
        purchased = true;
        return Promise.resolve(jsonResponse({ ...initialEntries[1], purchasedAt: '2026-03-01T00:00:00Z' }));
      }
      throw new Error(`Unexpected request: ${url}`);
    });
    vi.stubGlobal('fetch', fetchMock);
    renderPage();

    const table = await screen.findByRole('table', { name: 'Shopping plan entries' });
    expect(screen.getByRole('heading', { name: 'Shopping plan' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Update the list' })).toBeInTheDocument();
    expect(within(table).getByText('Brown Rice')).toBeInTheDocument();
    expect(within(table).getByText('2 boxes')).toBeInTheDocument();
    expect(within(table).getByText('Derived')).toBeInTheDocument();
    expect(within(table).getByText('Green Tea')).toBeInTheDocument();
    expect(within(table).getByText('replacing 2 you used')).toBeInTheDocument();
    expect(within(table).queryByText('Set a target quantity for automatic restocking.')).not.toBeInTheDocument();

    fireEvent.click(screen.getByRole('button', { name: 'Mark Green Tea purchased' }));

    await waitFor(() => expect(
      within(screen.getByRole('table', { name: 'Shopping plan entries' })).queryByText('Green Tea'),
    ).not.toBeInTheDocument());
    expect(fetchMock).toHaveBeenCalledWith(
      '/api/shopping-list/items/manual-1',
      expect.objectContaining({ method: 'PATCH', body: JSON.stringify({ purchased: true }) }),
    );
  });

  it('adds a manual item using the selected item and quantity', async () => {
    let entries: ShoppingListEntry[] = [];
    const fetchMock = vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url === '/api/providers') return Promise.resolve(jsonResponse([]));
      if (url === '/api/shopping-list' && init?.method === undefined) return Promise.resolve(jsonResponse(entries));
      if (url === '/api/shopping-list/fill') return Promise.resolve(jsonResponse(entries));
      if (url === '/api/inventory') return Promise.resolve(jsonResponse([inventoryItem('pasta', 'Pasta', null)]));
      if (url === '/api/shopping-list/items') {
        expect(init?.method).toBe('POST');
        expect(init?.body).toBe(JSON.stringify({ itemId: 'pasta', quantity: 4 }));
        const created = { id: 'manual-pasta', itemId: 'pasta', quantity: 4, source: 'manual', purchasedAt: null } satisfies ShoppingListEntry;
        entries = [created];
        return Promise.resolve(jsonResponse(created, 201));
      }
      throw new Error(`Unexpected request: ${url}`);
    });
    vi.stubGlobal('fetch', fetchMock);
    renderPage();

    expect(await screen.findByText('Build the list before sending it to Kroger.')).toBeInTheDocument();
    expect(screen.getByText('Adds these items to your Kroger cart.')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Build the list' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Send to Kroger' })).toBeDisabled();
    expect(screen.queryByLabelText('Pantry item')).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: 'Build the list' }));
    await screen.findByLabelText('Pantry item');
    fireEvent.change(screen.getByLabelText('Pantry item'), { target: { value: 'pasta' } });
    fireEvent.change(screen.getByLabelText('Quantity'), { target: { value: '4' } });
    fireEvent.click(screen.getByRole('button', { name: 'Add to shopping list' }));

    expect(await screen.findByText('4 boxes')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Update the list' })).toBeInTheDocument();
  });

  it('shows failed cart items in a notification and keeps them in the list', async () => {
    const entry = { id: 'manual-1', itemId: 'tea', quantity: 2, source: 'manual', purchasedAt: null } satisfies ShoppingListEntry;
    const fetchMock = vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url === '/api/providers') {
        return Promise.resolve(jsonResponse([{
          id: 'kroger',
          displayName: 'Kroger',
          capabilities: { auth: 'oauth2_authorization_code', delivery: 'server_push', confirmation: 'per_request', mutation: 'add_only', identity: 'derived' },
          connectionState: 'connected',
          credentialsConfigured: true,
        }]));
      }
      if (url.startsWith('/api/shopping-list') && init?.method === undefined) return Promise.resolve(jsonResponse([entry]));
      if (url === '/api/inventory') return Promise.resolve(jsonResponse([inventoryItem('tea', 'Green Tea', null)]));
      if (url === '/api/shopping-list/export') {
        return Promise.resolve(jsonResponse({ error: 'Kroger is not connected' }, 409));
      }
      throw new Error(`Unexpected request: ${url}`);
    });
    vi.stubGlobal('fetch', fetchMock);
    renderPage();

    const table = await screen.findByRole('table', { name: 'Shopping plan entries' });
    expect(within(table).getByText('Green Tea')).toBeInTheDocument();
    expect(screen.queryByLabelText('Client ID')).not.toBeInTheDocument();
    expect(screen.queryByLabelText('Client secret')).not.toBeInTheDocument();
    expect(screen.queryByLabelText('Redirect URI')).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Save credentials' })).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Disconnect' })).not.toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Start a new cart' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Send to Kroger' })).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: /setup/i })).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: 'Send to Kroger' }));

    expect(await screen.findByText('Kroger is not connected')).toBeInTheDocument();
    expect(screen.queryByText(/sent to Kroger/)).not.toBeInTheDocument();
    expect(within(table).getByText('Green Tea')).toBeInTheDocument();
  });

  it('notes a sale without a preferred brand or an always-buy choice', async () => {
    const entry = { id: 'auto-gv', itemId: 'gv', quantity: 1, source: 'auto', purchasedAt: null } satisfies ShoppingListEntry;
    const fetchMock = vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      const method = init?.method ?? 'GET';
      if (url === '/api/providers') return Promise.resolve(jsonResponse([]));
      if (url === '/api/shopping-list' && method === 'GET') return Promise.resolve(jsonResponse([entry]));
      if (url === '/api/inventory') {
        return Promise.resolve(jsonResponse([
          inventoryItem('gv', 'Great Value Cut Green Beans', 4),
          inventoryItem('kr', 'Kroger Cut Green Beans', 4),
        ]));
      }
      if (url === '/api/shopping-list/deals' && method === 'PUT') {
        return Promise.resolve(jsonResponse({ itemId: 'kr', priceCents: 89, label: 'Noted sale' }));
      }
      if (url === '/api/shopping-list/export') {
        expect(init?.body).toBeUndefined();
        return Promise.resolve(jsonResponse({ exported: 1 }));
      }
      throw new Error(`Unexpected request: ${url} ${method}`);
    });
    vi.stubGlobal('fetch', fetchMock);
    renderPage();

    expect(await screen.findByLabelText('Brand on sale')).toBeInTheDocument();
    expect(screen.queryByLabelText('Preferred brand for cut green beans')).not.toBeInTheDocument();
    expect(screen.queryByRole('checkbox', { name: 'Always buy this brand of cut green beans' })).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: /Take the deal/ })).not.toBeInTheDocument();

    fireEvent.change(screen.getByLabelText('Brand on sale'), { target: { value: 'kr' } });
    fireEvent.change(screen.getByLabelText('Sale price in cents'), { target: { value: '89' } });
    fireEvent.click(screen.getByRole('button', { name: 'Note sale' }));
    await waitFor(() => expect(fetchMock).toHaveBeenCalledWith(
      '/api/shopping-list/deals',
      expect.objectContaining({ method: 'PUT', body: JSON.stringify({ itemId: 'kr', priceCents: 89, label: 'Noted sale' }) }),
    ));

    fireEvent.click(screen.getByRole('button', { name: 'Send to Kroger' }));
    expect(await screen.findByText('1 item sent to Kroger.')).toBeInTheDocument();
  });

  it('shows a group line with its rule and swap, and hides brand controls', async () => {
    const entry: ShoppingListEntry = {
      id: 'line-1',
      itemId: 'gv',
      quantity: 2,
      source: 'auto',
      purchasedAt: null,
      note: 'This is the one with the star.',
      group: {
        id: 'beans',
        name: 'Cut green beans',
        rule: 'favorite',
        ruleConfirmed: true,
        members: [
          { itemId: 'gv', productId: 'prod-gv', name: 'Great Value Cut Green Beans' },
          { itemId: 'kr', productId: 'prod-kr', name: 'Kroger Cut Green Beans' },
        ],
      },
    };
    const fetchMock = vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      const method = init?.method ?? 'GET';
      if (url === '/api/providers') return Promise.resolve(jsonResponse([]));
      if (url === '/api/shopping-list' && method === 'GET') return Promise.resolve(jsonResponse([entry]));
      if (url === '/api/inventory') {
        return Promise.resolve(jsonResponse([
          inventoryItem('gv', 'Great Value Cut Green Beans', null),
          inventoryItem('kr', 'Kroger Cut Green Beans', null),
        ]));
      }
      if (url === '/api/shopping-list/items/line-1/swap' && method === 'POST') {
        expect(init?.body).toBe(JSON.stringify({ itemId: 'kr' }));
        return Promise.resolve(jsonResponse({ ...entry, itemId: 'kr' }));
      }
      throw new Error(`Unexpected request: ${url} ${method}`);
    });
    vi.stubGlobal('fetch', fetchMock);
    renderPage();

    const table = await screen.findByRole('table', { name: 'Shopping plan entries' });
    expect(within(table).getByText('Cut green beans')).toBeInTheDocument();
    expect(within(table).getByText('Great Value Cut Green Beans', { selector: 'p' })).toBeInTheDocument();
    expect(within(table).getByText('Always my favorite')).toBeInTheDocument();
    expect(within(table).getByText('This is the one with the star.')).toBeInTheDocument();
    expect(screen.queryByLabelText('Preferred brand for cut green beans')).not.toBeInTheDocument();
    expect(screen.queryByRole('checkbox', { name: 'Always buy this brand of cut green beans' })).not.toBeInTheDocument();

    fireEvent.change(screen.getByLabelText('This trip, buy'), { target: { value: 'kr' } });
    await waitFor(() => expect(fetchMock).toHaveBeenCalledWith(
      '/api/shopping-list/items/line-1/swap',
      expect.objectContaining({ method: 'POST' }),
    ));
  });

  it('reconnects the event stream when the household changes', async () => {
    class FakeEventSource {
      static instances: FakeEventSource[] = [];
      closed = false;
      readonly url: string;
      constructor(url: string) {
        this.url = url;
        FakeEventSource.instances.push(this);
      }
      addEventListener() {}
      close() {
        this.closed = true;
      }
    }
    FakeEventSource.instances = [];
    vi.stubGlobal('EventSource', FakeEventSource);
    resetStage1Client();
    let loads = 0;
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL) => {
      const url = String(input);
      if (url === '/api/auth/session') {
        return Promise.resolve(jsonResponse({
          user: { id: 'user-1', email: 'ada@example.com', name: 'Ada' },
          kind: 'account',
          memberships: [{ householdId: 'hh-1', name: 'Pantry', role: 'owner', deletedAt: null }],
          households: [{ id: 'hh-1', name: 'Pantry', role: 'owner', deletedAt: null }],
          activeHouseholdId: 'hh-1',
          operator: false,
        }));
      }
      if (url === '/api/shopping-list') {
        loads += 1;
        return Promise.resolve(jsonResponse([]));
      }
      if (url === '/api/providers' || url === '/api/inventory' || url === '/api/restock') {
        return Promise.resolve(jsonResponse([]));
      }
      throw new Error(`Unexpected request: ${url}`);
    }));
    await getSession();
    renderPage();
    await waitFor(() => expect(loads).toBeGreaterThan(0));
    await waitFor(() => expect(FakeEventSource.instances).toHaveLength(1));
    const first = FakeEventSource.instances[0];
    const before = loads;
    window.dispatchEvent(new Event(HOUSEHOLD_SWITCH_EVENT));
    await waitFor(() => expect(FakeEventSource.instances).toHaveLength(2));
    expect(first?.closed).toBe(true);
    expect(FakeEventSource.instances[1]?.url).toBe('/api/events');
    await waitFor(() => expect(loads).toBeGreaterThan(before));
  });
});
