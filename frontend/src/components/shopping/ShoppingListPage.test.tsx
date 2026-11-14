import { fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { MantineProvider } from '@mantine/core';
import { Notifications } from '@mantine/notifications';
import { MemoryRouter } from 'react-router-dom';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import type { InventoryItem, ShoppingListEntry } from '@stockbot/shared/types';
import { getSession, resetStage1Client } from '../../api/client';
import { HOUSEHOLD_SWITCH_EVENT } from '../household/liveHints';
import { krogerAppCopy } from '@stockbot/shared/krogerAppCopy';
import * as providerList from '@stockbot/shared/providerList';
import { ShoppingListPage } from './ShoppingListPage';
import { TRIP_BUY_SWAP_ERROR } from './tripBuyCopy';

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
    <MemoryRouter>
      <ShoppingListPage />
    </MemoryRouter>
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

    expect(await screen.findByText('Build the list before sending it to your store.')).toBeInTheDocument();
    expect(screen.getByText("Sends these items to your store's cart.")).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Build the list' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Send to your store' })).toBeDisabled();
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
      if (url === '/api/auth/session') {
        return Promise.resolve(jsonResponse({
          user: { id: 'user-1', email: 'admin@testhousehold.test', name: 'Admin' },
          kind: 'account',
          memberships: [{ householdId: 'hh-1', name: 'Pantry', role: 'owner', deletedAt: null }],
          households: [{ id: 'hh-1', name: 'Pantry', role: 'owner', deletedAt: null }],
          activeHouseholdId: 'hh-1',
          operator: false,
        }));
      }
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
        return Promise.resolve(jsonResponse({ error: 'Kroger is not configured', code: 'conflict' }, 409));
      }
      throw new Error(`Unexpected request: ${url}`);
    });
    vi.stubGlobal('fetch', fetchMock);
    await getSession();
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

    expect(await screen.findByText(krogerAppCopy.linkNeed)).toBeInTheDocument();
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

    fireEvent.click(screen.getByRole('button', { name: 'Send to your store' }));
    expect(await screen.findByText('1 item sent to your store cart.')).toBeInTheDocument();
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
          { itemId: 'gv', productId: 'prod-gv', name: 'Great Value Cut Green Beans', onHand: 0 },
          { itemId: 'kr', productId: 'prod-kr', name: 'Kroger Cut Green Beans', onHand: 0 },
        ],
      },
    };
    let listItemId = entry.itemId;
    const fetchMock = vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      const method = init?.method ?? 'GET';
      if (url === '/api/providers') return Promise.resolve(jsonResponse([]));
      if (url === '/api/shopping-list' && method === 'GET') {
        return Promise.resolve(jsonResponse([{ ...entry, itemId: listItemId }]));
      }
      if (url === '/api/inventory') {
        return Promise.resolve(jsonResponse([
          inventoryItem('gv', 'Great Value Cut Green Beans', null),
          inventoryItem('kr', 'Kroger Cut Green Beans', null),
        ]));
      }
      if (url === '/api/shopping-list/items/line-1/swap' && method === 'POST') {
        expect(init?.body).toBe(JSON.stringify({ itemId: 'kr' }));
        listItemId = 'kr';
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

    fireEvent.click(screen.getByRole('radio', { name: 'Kroger Cut Green Beans' }));
    await waitFor(() => expect(fetchMock).toHaveBeenCalledWith(
      '/api/shopping-list/items/line-1/swap',
      expect.objectContaining({ method: 'POST', body: JSON.stringify({ itemId: 'kr' }) }),
    ));
    await waitFor(() => expect(screen.getByRole('radio', { name: 'Kroger Cut Green Beans' })).toBeChecked());
    expect(screen.getByRole('table', { name: 'Shopping plan entries' })).toBeInTheDocument();
  });

  it('shows trip-buy size differentiators and hides This trip on a one-member group', async () => {
    const entries: ShoppingListEntry[] = [
      {
        id: 'line-collide',
        itemId: '664-a',
        quantity: 1,
        source: 'manual',
        purchasedAt: null,
        group: {
          id: 'g664',
          name: '664 Group Alpha',
          rule: 'favorite',
          ruleConfirmed: true,
          members: [
            {
              itemId: '664-a',
              productId: 'p664-a',
              name: 'KROGER CUT GREEN BEANS',
              netAmount: 14.5,
              netUnit: 'oz',
              onHand: 2,
              lastStockedAt: '2026-03-01T00:00:00Z',
            },
            {
              itemId: '664-b',
              productId: 'p664-b',
              name: 'Kroger Cut Green Beans',
              netAmount: 8,
              netUnit: 'oz',
              onHand: 0,
              lastStockedAt: '2026-04-01T00:00:00Z',
            },
          ],
        },
      },
      {
        id: 'line-single',
        itemId: 'only',
        quantity: 1,
        source: 'manual',
        purchasedAt: null,
        group: {
          id: 'g-single',
          name: '664 Single',
          rule: 'favorite',
          ruleConfirmed: true,
          members: [{ itemId: 'only', productId: 'p-only', name: 'Only Product', onHand: 1 }],
        },
      },
    ];
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL) => {
      const url = String(input);
      if (url === '/api/providers') return Promise.resolve(jsonResponse([]));
      if (url === '/api/shopping-list') return Promise.resolve(jsonResponse(entries));
      if (url === '/api/inventory') return Promise.resolve(jsonResponse([]));
      if (url === '/api/restock') return Promise.resolve(jsonResponse(null, 404));
      throw new Error(`Unexpected request: ${url}`);
    }));
    renderPage();
    await screen.findByRole('table', { name: 'Shopping plan entries' });
    expect(screen.getByRole('radio', { name: 'Kroger Cut Green Beans, 14.5 oz' })).toBeInTheDocument();
    expect(screen.getByRole('radio', { name: 'Kroger Cut Green Beans, 8 oz' })).toBeInTheDocument();
    expect(screen.queryByLabelText('664 Single This trip, buy')).not.toBeInTheDocument();
    expect(within(screen.getByText('664 Single').closest('tr')!).queryByRole('group', { name: 'This trip, buy' })).not.toBeInTheDocument();
  });

  it('shows distinct photos without a second line and inline error when swap fails', async () => {
    const entry: ShoppingListEntry = {
      id: 'line-photos',
      itemId: 'photo-a',
      quantity: 1,
      source: 'manual',
      purchasedAt: null,
      group: {
        id: 'g-photos',
        name: '664 Photo Group',
        rule: 'favorite',
        ruleConfirmed: true,
        members: [
          {
            itemId: 'photo-a',
            productId: 'p-a',
            name: 'Alpha Beans',
            imageUrl: 'https://example.test/664-a.jpg',
            onHand: 0,
          },
          {
            itemId: 'photo-b',
            productId: 'p-b',
            name: 'Beta Beans',
            imageUrl: 'https://example.test/664-b.jpg',
            onHand: 0,
          },
        ],
      },
    };
    const fetchMock = vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      const method = init?.method ?? 'GET';
      if (url === '/api/providers') return Promise.resolve(jsonResponse([]));
      if (url === '/api/shopping-list' && method === 'GET') return Promise.resolve(jsonResponse([entry]));
      if (url === '/api/inventory') return Promise.resolve(jsonResponse([]));
      if (url === '/api/restock') return Promise.resolve(jsonResponse(null, 404));
      if (url === '/api/shopping-list/items/line-photos/swap' && method === 'POST') {
        return Promise.resolve(jsonResponse({ error: 'swap failed' }, 500));
      }
      throw new Error(`Unexpected request: ${url} ${method}`);
    });
    let listGets = 0;
    const countingFetch = vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      const method = init?.method ?? 'GET';
      if (url === '/api/shopping-list' && method === 'GET') {
        listGets += 1;
        return fetchMock(input, init);
      }
      return fetchMock(input, init);
    });
    vi.stubGlobal('fetch', countingFetch);
    renderPage();
    const table = await screen.findByRole('table', { name: 'Shopping plan entries' });
    const loadsBeforeSwap = listGets;
    expect(screen.getByRole('radio', { name: 'Alpha Beans' })).toBeChecked();
    expect(screen.getByRole('radio', { name: 'Beta Beans' })).not.toBeChecked();
    expect(screen.queryByText('This is a different product with the same name.')).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole('radio', { name: 'Beta Beans' }));
    await waitFor(() => expect(within(table).getByText(TRIP_BUY_SWAP_ERROR)).toBeInTheDocument());
    expect(screen.getByRole('radio', { name: 'Alpha Beans' })).toBeChecked();
    expect(listGets).toBe(loadsBeforeSwap);
    expect(screen.queryByText('Unable to change the product on this line.')).not.toBeInTheDocument();
  });

  it('sets aria-busy during swap and clears inline error after a later success', async () => {
    const entry: ShoppingListEntry = {
      id: 'line-busy',
      itemId: 'busy-a',
      quantity: 1,
      source: 'manual',
      purchasedAt: null,
      group: {
        id: 'g-busy',
        name: '664 Busy Group',
        rule: 'favorite',
        ruleConfirmed: true,
        members: [
          { itemId: 'busy-a', productId: 'p-ba', name: 'Busy Alpha', onHand: 0 },
          { itemId: 'busy-b', productId: 'p-bb', name: 'Busy Beta', onHand: 0 },
        ],
      },
    };
    let attempt = 0;
    let lineItemId = entry.itemId;
    const fetchMock = vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      const method = init?.method ?? 'GET';
      if (url === '/api/providers') return Promise.resolve(jsonResponse([]));
      if (url === '/api/shopping-list' && method === 'GET') {
        return Promise.resolve(jsonResponse([{ ...entry, itemId: lineItemId }]));
      }
      if (url === '/api/inventory') return Promise.resolve(jsonResponse([]));
      if (url === '/api/restock') return Promise.resolve(jsonResponse(null, 404));
      if (url === '/api/shopping-list/items/line-busy/swap' && method === 'POST') {
        attempt += 1;
        if (attempt === 1) {
          return Promise.resolve(jsonResponse({ error: 'swap failed' }, 500));
        }
        expect(init?.body).toBe(JSON.stringify({ itemId: 'busy-b' }));
        lineItemId = 'busy-b';
        return new Promise((resolve) => {
          setTimeout(
            () => resolve(jsonResponse({ ...entry, itemId: 'busy-b' })),
            40,
          );
        });
      }
      throw new Error(`Unexpected request: ${url} ${method}`);
    });
    vi.stubGlobal('fetch', fetchMock);
    renderPage();
    const table = await screen.findByRole('table', { name: 'Shopping plan entries' });
    const picker = () => within(table).getByRole('group', { name: 'This trip, buy' });
    fireEvent.click(screen.getByRole('radio', { name: 'Busy Beta' }));
    await waitFor(() => expect(within(table).getByText(TRIP_BUY_SWAP_ERROR)).toBeInTheDocument());
    expect(picker().getAttribute('aria-busy')).not.toBe('true');
    fireEvent.click(screen.getByRole('radio', { name: 'Busy Beta' }));
    await waitFor(() => expect(picker()).toHaveAttribute('aria-busy', 'true'), { timeout: 3000 });
    await waitFor(() => expect(screen.queryByText(TRIP_BUY_SWAP_ERROR)).not.toBeInTheDocument());
    await waitFor(() => expect(picker().getAttribute('aria-busy')).not.toBe('true'));
    expect(screen.getByRole('radio', { name: 'Busy Beta' })).toBeChecked();
  });

  it('clears a stale swap error before refresh when a later swap succeeds', async () => {
    const entry: ShoppingListEntry = {
      id: 'line-stale-error',
      itemId: 'se-a',
      quantity: 1,
      source: 'manual',
      purchasedAt: null,
      group: {
        id: 'g-se',
        name: '664 Stale Error',
        rule: 'favorite',
        ruleConfirmed: true,
        members: [
          { itemId: 'se-a', productId: 'p-sea', name: 'Stale Alpha', onHand: 0 },
          { itemId: 'se-b', productId: 'p-seb', name: 'Stale Beta', onHand: 0 },
        ],
      },
    };
    let attempt = 0;
    let refreshShouldFail = false;
    const fetchMock = vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      const method = init?.method ?? 'GET';
      if (url === '/api/providers') return Promise.resolve(jsonResponse([]));
      if (url === '/api/shopping-list' && method === 'GET') {
        if (refreshShouldFail) return Promise.reject(new Error('refresh failed'));
        return Promise.resolve(jsonResponse([{ ...entry, itemId: attempt >= 2 ? 'se-b' : entry.itemId }]));
      }
      if (url === '/api/inventory') return Promise.resolve(jsonResponse([]));
      if (url === '/api/restock') return Promise.resolve(jsonResponse(null, 404));
      if (url === '/api/shopping-list/items/line-stale-error/swap' && method === 'POST') {
        attempt += 1;
        if (attempt === 1) {
          return Promise.resolve(jsonResponse({ error: 'swap failed' }, 500));
        }
        refreshShouldFail = true;
        return Promise.resolve(jsonResponse({ ...entry, itemId: 'se-b' }));
      }
      throw new Error(`Unexpected request: ${url} ${method}`);
    });
    vi.stubGlobal('fetch', fetchMock);
    renderPage();
    await screen.findByRole('table', { name: 'Shopping plan entries' });
    fireEvent.click(screen.getByRole('radio', { name: 'Stale Beta' }));
    await waitFor(() => expect(screen.getByText(TRIP_BUY_SWAP_ERROR)).toBeInTheDocument());
    fireEvent.click(screen.getByRole('radio', { name: 'Stale Beta' }));
    await waitFor(() => expect(screen.queryByText(TRIP_BUY_SWAP_ERROR)).not.toBeInTheDocument());
    expect(screen.getByRole('radio', { name: 'Stale Beta' })).toBeChecked();
  });

  it('keeps the new choice when swap succeeds but refresh fails', async () => {
    const entry: ShoppingListEntry = {
      id: 'line-refresh-fail',
      itemId: 'rf-a',
      quantity: 1,
      source: 'manual',
      purchasedAt: null,
      group: {
        id: 'g-rf',
        name: '664 Refresh Fail',
        rule: 'favorite',
        ruleConfirmed: true,
        members: [
          { itemId: 'rf-a', productId: 'p-rfa', name: 'Refresh Alpha', onHand: 0 },
          { itemId: 'rf-b', productId: 'p-rfb', name: 'Refresh Beta', onHand: 0 },
        ],
      },
    };
    let refreshShouldFail = false;
    const fetchMock = vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      const method = init?.method ?? 'GET';
      if (url === '/api/providers') return Promise.resolve(jsonResponse([]));
      if (url === '/api/shopping-list' && method === 'GET') {
        if (refreshShouldFail) return Promise.reject(new Error('refresh failed'));
        return Promise.resolve(jsonResponse([entry]));
      }
      if (url === '/api/inventory') return Promise.resolve(jsonResponse([]));
      if (url === '/api/restock') return Promise.resolve(jsonResponse(null, 404));
      if (url === '/api/shopping-list/items/line-refresh-fail/swap' && method === 'POST') {
        refreshShouldFail = true;
        return Promise.resolve(jsonResponse({ ...entry, itemId: 'rf-b' }));
      }
      throw new Error(`Unexpected request: ${url} ${method}`);
    });
    vi.stubGlobal('fetch', fetchMock);
    renderPage();
    await screen.findByRole('table', { name: 'Shopping plan entries' });
    fireEvent.click(screen.getByRole('radio', { name: 'Refresh Beta' }));
    await waitFor(() => expect(screen.queryByText(TRIP_BUY_SWAP_ERROR)).not.toBeInTheDocument());
    expect(screen.getByRole('radio', { name: 'Refresh Beta' })).toBeChecked();
  });

  it('hides trip-buy picker on unsaved auto-derived grouped lines', async () => {
    const entry: ShoppingListEntry = {
      id: '',
      itemId: 'auto-a',
      quantity: 1,
      source: 'auto',
      purchasedAt: null,
      group: {
        id: 'auto-group',
        name: '664 Auto',
        rule: 'favorite',
        ruleConfirmed: true,
        members: [
          { itemId: 'auto-a', productId: 'pa', name: 'Auto A', onHand: 0 },
          { itemId: 'auto-b', productId: 'pb', name: 'Auto B', onHand: 0 },
        ],
      },
    };
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL) => {
      const url = String(input);
      if (url === '/api/providers' || url === '/api/shopping-list' || url === '/api/inventory') {
        return Promise.resolve(jsonResponse(url.endsWith('inventory') ? [] : url.endsWith('providers') ? [] : [entry]));
      }
      if (url === '/api/restock') return Promise.resolve(jsonResponse(null, 404));
      throw new Error(`Unexpected request: ${url}`);
    }));
    renderPage();
    await screen.findByRole('table', { name: 'Shopping plan entries' });
    expect(screen.queryByRole('group', { name: 'This trip, buy' })).not.toBeInTheDocument();
  });

  it('omits disabled Kroger from the provider row and Kroger send target', async () => {
    const disabledKroger = { id: 'kroger' as const, displayName: 'Kroger', status: 'disabled' as const };
    const entry = { id: 'manual-1', itemId: 'tea', quantity: 2, source: 'manual', purchasedAt: null } satisfies ShoppingListEntry;
    const fetchMock = vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url === '/api/providers') return Promise.resolve(jsonResponse([disabledKroger]));
      if (url === '/api/shopping-list' && init?.method === undefined) return Promise.resolve(jsonResponse([entry]));
      if (url === '/api/inventory') return Promise.resolve(jsonResponse([inventoryItem('tea', 'Green Tea', null)]));
      throw new Error(`Unexpected request: ${url}`);
    });
    vi.stubGlobal('fetch', fetchMock);
    const filterSpy = vi.spyOn(providerList, 'providerListEntries');
    renderPage();

    await screen.findByRole('table', { name: 'Shopping plan entries' });
    expect(screen.queryByLabelText('Grocery providers')).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Send to Kroger' })).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Connect' })).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Reconnect' })).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Start a new cart' })).not.toBeInTheDocument();
    expect(screen.queryByRole('link', { name: /online cart/i })).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Send to your store' })).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: /^Send to /i })).not.toBeInTheDocument();
    expect(
      filterSpy.mock.calls.some((args) => args[0]?.some((row) => providerList.isKrogerProviderDisabled(row))),
    ).toBe(true);
    filterSpy.mockRestore();
  });

  it('remounts provision quantity input when server quantity changes after Update the list', async () => {
    let quantity = 2;
    const entry = (): ShoppingListEntry => ({
      id: 'manual-1',
      itemId: 'tea',
      quantity,
      source: 'manual',
      purchasedAt: null,
    });
    const krogerProvider = [{
      id: 'kroger',
      displayName: 'Kroger',
      capabilities: { auth: 'oauth2_authorization_code', delivery: 'server_push', confirmation: 'per_request', mutation: 'add_only', identity: 'derived' },
      connectionState: 'connected',
      credentialsConfigured: true,
    }];
    let adjustmentBody: string | undefined;
    const fetchMock = vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      const method = init?.method ?? 'GET';
      if (url === '/api/auth/session') {
        return Promise.resolve(jsonResponse({
          user: { id: 'user-1', email: 'admin@testhousehold.test', name: 'Admin' },
          kind: 'account',
          memberships: [{ householdId: 'hh-1', name: 'Pantry', role: 'owner', deletedAt: null }],
          households: [{ id: 'hh-1', name: 'Pantry', role: 'owner', deletedAt: null }],
          activeHouseholdId: 'hh-1',
          operator: false,
        }));
      }
      if (url === '/api/providers') return Promise.resolve(jsonResponse(krogerProvider));
      if (url.startsWith('/api/shopping-list') && method === 'GET') return Promise.resolve(jsonResponse([entry()]));
      if (url === '/api/shopping-list/fill' && method === 'POST') {
        quantity = 3;
        return Promise.resolve(jsonResponse([entry()]));
      }
      if (url === '/api/inventory') {
        return Promise.resolve(jsonResponse([inventoryItem('tea', 'Green Tea', null)]));
      }
      if (url === '/api/restock') return Promise.resolve(jsonResponse(null, 404));
      if (url === '/api/shopping-list/items/manual-1/adjustment?provider=kroger' && method === 'PUT') {
        adjustmentBody = init?.body as string;
        return Promise.resolve(jsonResponse({ adjustment: JSON.parse(adjustmentBody!).adjustment }));
      }
      throw new Error(`Unexpected request: ${method} ${url}`);
    });
    vi.stubGlobal('fetch', fetchMock);
    resetStage1Client();
    await getSession();
    renderPage();

    const input = await screen.findByLabelText('Provision quantity for Green Tea');
    expect(input).toHaveValue('2');
    expect(within(screen.getByRole('table', { name: 'Shopping plan entries' })).getByText('2 boxes')).toBeInTheDocument();

    fireEvent.click(screen.getByRole('button', { name: 'Update the list' }));
    await waitFor(() => expect(within(screen.getByRole('table', { name: 'Shopping plan entries' })).getByText('3 boxes')).toBeInTheDocument());
    await waitFor(() => expect(screen.getByLabelText('Provision quantity for Green Tea')).toHaveValue('3'));

    fireEvent.blur(screen.getByLabelText('Provision quantity for Green Tea'));
    await waitFor(() => expect(adjustmentBody).toBe(JSON.stringify({ adjustment: 3 })));
    expect(adjustmentBody).not.toBe(JSON.stringify({ adjustment: 2 }));
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
