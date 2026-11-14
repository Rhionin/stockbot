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

  it('renders the shopping table when auto lines omit id in the payload', async () => {
    const entry = {
      itemId: 'rice',
      quantity: 2,
      source: 'auto',
      purchasedAt: null,
      note: 'replacing 2 you used',
    } as ShoppingListEntry;
    const fetchMock = vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url === '/api/providers') return Promise.resolve(jsonResponse([]));
      if (url === '/api/shopping-list' && init?.method === undefined) return Promise.resolve(jsonResponse([entry]));
      if (url === '/api/inventory') return Promise.resolve(jsonResponse([inventoryItem('rice', 'Brown Rice', 3)]));
      if (url === '/api/restock') return Promise.resolve(jsonResponse(null));
      throw new Error(`Unexpected request: ${url}`);
    });
    vi.stubGlobal('fetch', fetchMock);
    renderPage();

    const table = await screen.findByRole('table', { name: 'Shopping plan entries' });
    expect(within(table).getByText('Brown Rice')).toBeInTheDocument();
  });

  it('uses the trip-only Remove label, focuses Undo after remove, and restores the line on undo', async () => {
    const entry = { id: 'manual-tea', itemId: 'tea', quantity: 1, source: 'manual', purchasedAt: null } satisfies ShoppingListEntry;
    const restoredFromPost = {
      id: 'post-restored-tea',
      itemId: 'tea',
      quantity: 9,
      source: 'manual',
      purchasedAt: null,
      note: 'restored via POST',
    } satisfies ShoppingListEntry;
    const staleFromGet = {
      id: 'get-stale-tea',
      itemId: 'tea',
      quantity: 2,
      source: 'manual',
      purchasedAt: null,
      note: 'from GET only',
    } satisfies ShoppingListEntry;
    let list: ShoppingListEntry[] = [entry];
    let undoCalls = 0;
    let serveStaleOnEmptyGet = false;
    const fetchMock = vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url === '/api/providers') return Promise.resolve(jsonResponse([]));
      if (url === '/api/shopping-list' && init?.method === undefined) {
        if (list.length === 0) {
          return Promise.resolve(jsonResponse(serveStaleOnEmptyGet ? [staleFromGet] : []));
        }
        return Promise.resolve(jsonResponse(list));
      }
      if (url === '/api/inventory') return Promise.resolve(jsonResponse([inventoryItem('tea', 'Green Tea', 2)]));
      if (url === '/api/restock') return Promise.resolve(jsonResponse(null));
      if (url === '/api/shopping-list/items/manual-tea' && init?.method === 'DELETE') {
        list = [];
        return Promise.resolve(jsonResponse({}));
      }
      if (url === '/api/shopping-list/items/tea/undo-remove' && init?.method === 'POST') {
        undoCalls += 1;
        serveStaleOnEmptyGet = true;
        return Promise.resolve(jsonResponse(restoredFromPost));
      }
      throw new Error(`Unexpected request: ${url} ${init?.method ?? 'GET'}`);
    });
    vi.stubGlobal('fetch', fetchMock);
    renderPage();

    const remove = await screen.findByRole('button', { name: 'Remove Green Tea for this trip' });
    expect(remove).toHaveTextContent('Remove for this trip');
    fireEvent.click(remove);
    const undo = await screen.findByRole('button', { name: 'Undo' });
    await waitFor(() => expect(undo).toHaveFocus());
    fireEvent.click(undo);
    const table = screen.getByRole('table', { name: 'Shopping plan entries' });
    await waitFor(() => {
      expect(within(table).getByText('Green Tea')).toBeInTheDocument();
      expect(within(table).getByText('9 boxes', { exact: true })).toBeInTheDocument();
      expect(within(table).getByText('restored via POST')).toBeInTheDocument();
    });
    expect(within(table).queryByText('2 boxes', { exact: true })).not.toBeInTheDocument();
    expect(within(table).queryByText('from GET only')).not.toBeInTheDocument();
    expect(undoCalls).toBe(1);
    expect(screen.queryByRole('status')).not.toBeInTheDocument();
  });

  it('dismisses Undo quietly on 404 and refreshes the list', async () => {
    const entry = { id: 'manual-tea', itemId: 'tea', quantity: 1, source: 'manual', purchasedAt: null } satisfies ShoppingListEntry;
    let list: ShoppingListEntry[] = [entry];
    let undoCalls = 0;
    let shoppingListGets = 0;
    let saw404RefreshPayload = false;
    const fetchMock = vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url === '/api/providers') return Promise.resolve(jsonResponse([]));
      if (url === '/api/shopping-list' && init?.method === undefined) {
        shoppingListGets += 1;
        if (undoCalls > 0 && shoppingListGets > 1) {
          saw404RefreshPayload = true;
          list = [{ ...entry, note: 'payload-after-404-refresh' }];
        }
        return Promise.resolve(jsonResponse(list));
      }
      if (url === '/api/inventory') return Promise.resolve(jsonResponse([inventoryItem('tea', 'Green Tea', 2)]));
      if (url === '/api/restock') return Promise.resolve(jsonResponse(null));
      if (url === '/api/shopping-list/items/manual-tea' && init?.method === 'DELETE') {
        list = [];
        return Promise.resolve(jsonResponse({}));
      }
      if (url === '/api/shopping-list/items/tea/undo-remove' && init?.method === 'POST') {
        undoCalls += 1;
        return Promise.resolve(jsonResponse({ error: 'trip remove skip not found', code: 'not_found' }, 404));
      }
      throw new Error(`Unexpected request: ${url}`);
    });
    vi.stubGlobal('fetch', fetchMock);
    renderPage();

    fireEvent.click(await screen.findByRole('button', { name: 'Remove Green Tea for this trip' }));
    await screen.findByRole('button', { name: 'Undo' });
    const getsBeforeUndo = shoppingListGets;
    fireEvent.click(screen.getByRole('button', { name: 'Undo' }));
    await waitFor(() => expect(screen.queryByRole('button', { name: 'Undo' })).not.toBeInTheDocument());
    expect(screen.queryByText("Couldn't undo. Try again.")).not.toBeInTheDocument();
    expect(screen.queryByText('trip remove skip not found')).not.toBeInTheDocument();
    expect(undoCalls).toBe(1);
    expect(shoppingListGets).toBe(getsBeforeUndo + 1);
    expect(saw404RefreshPayload).toBe(true);
  });

  it('shows the approved undo failure copy inline on server error', async () => {
    const entry = { id: 'manual-tea', itemId: 'tea', quantity: 1, source: 'manual', purchasedAt: null } satisfies ShoppingListEntry;
    let list: ShoppingListEntry[] = [entry];
    const fetchMock = vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url === '/api/providers') return Promise.resolve(jsonResponse([]));
      if (url === '/api/shopping-list' && init?.method === undefined) return Promise.resolve(jsonResponse(list));
      if (url === '/api/inventory') return Promise.resolve(jsonResponse([inventoryItem('tea', 'Green Tea', 2)]));
      if (url === '/api/restock') return Promise.resolve(jsonResponse(null));
      if (url === '/api/shopping-list/items/manual-tea' && init?.method === 'DELETE') {
        list = [];
        return Promise.resolve(jsonResponse({}));
      }
      if (url === '/api/shopping-list/items/tea/undo-remove' && init?.method === 'POST') {
        return Promise.resolve(jsonResponse({ error: 'internal detail', code: 'internal' }, 500));
      }
      throw new Error(`Unexpected request: ${url}`);
    });
    vi.stubGlobal('fetch', fetchMock);
    renderPage();

    fireEvent.click(await screen.findByRole('button', { name: 'Remove Green Tea for this trip' }));
    fireEvent.click(await screen.findByRole('button', { name: 'Undo' }));
    expect(await screen.findByText("Couldn't undo. Try again.")).toBeInTheDocument();
    expect(screen.queryByText('internal detail')).not.toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Undo' })).toBeInTheDocument();
  });

  it('undoes a group line with one undo-remove call for the row itemId', async () => {
    const entry: ShoppingListEntry = {
      id: 'line-beans',
      itemId: 'gv',
      quantity: 1,
      source: 'auto',
      purchasedAt: null,
      group: {
        id: 'beans',
        name: 'Cut green beans',
        rule: 'favorite',
        ruleConfirmed: true,
        members: [
          { itemId: 'gv', productId: 'p-gv', name: 'Great Value' },
          { itemId: 'kr', productId: 'p-kr', name: 'Kroger' },
        ],
      },
    };
    let list: ShoppingListEntry[] = [entry];
    const undoUrls: string[] = [];
    let serveStaleOnEmptyGet = false;
    const restoredFromPost = { ...entry, quantity: 7, note: 'group undo POST body' } satisfies ShoppingListEntry;
    const staleFromGet = { ...entry, quantity: 3, id: 'get-stale-beans', note: 'from GET only' } satisfies ShoppingListEntry;
    const fetchMock = vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url === '/api/providers') return Promise.resolve(jsonResponse([]));
      if (url === '/api/shopping-list' && init?.method === undefined) {
        if (list.length === 0) {
          return Promise.resolve(jsonResponse(serveStaleOnEmptyGet ? [staleFromGet] : []));
        }
        return Promise.resolve(jsonResponse(list));
      }
      if (url === '/api/inventory') {
        return Promise.resolve(jsonResponse([
          inventoryItem('gv', 'Great Value', null),
          inventoryItem('kr', 'Kroger', null),
        ]));
      }
      if (url === '/api/restock') return Promise.resolve(jsonResponse(null));
      if (url === '/api/shopping-list/items/line-beans' && init?.method === 'DELETE') {
        list = [];
        return Promise.resolve(jsonResponse({}));
      }
      if (url.includes('/undo-remove') && init?.method === 'POST') {
        undoUrls.push(url);
        serveStaleOnEmptyGet = true;
        return new Promise<Response>((resolve) => {
          window.setTimeout(
            () => resolve(jsonResponse(restoredFromPost)),
            0,
          );
        });
      }
      throw new Error(`Unexpected request: ${url} ${init?.method ?? 'GET'}`);
    });
    vi.stubGlobal('fetch', fetchMock);
    renderPage();

    fireEvent.click(await screen.findByRole('button', { name: 'Remove the Cut green beans group for this trip' }));
    fireEvent.click(await screen.findByRole('button', { name: 'Undo' }));
    const table = await screen.findByRole('table', { name: 'Shopping plan entries' });
    await waitFor(() => {
      expect(within(table).getByText('Cut green beans')).toBeInTheDocument();
      expect(within(table).getByText('7 boxes', { exact: true })).toBeInTheDocument();
    });
    await waitFor(() => {
      expect(undoUrls).toEqual(['/api/shopping-list/items/gv/undo-remove']);
    });
    expect(undoUrls.some((path) => path.includes('/beans/undo-remove'))).toBe(false);
    expect(undoUrls.some((path) => path.includes('/kr/undo-remove'))).toBe(false);
    await new Promise((resolve) => window.setTimeout(resolve, 50));
    expect(undoUrls).toHaveLength(1);
  });

  it('does not submit undo twice while a request is in flight', async () => {
    const entry = { id: 'manual-tea', itemId: 'tea', quantity: 1, source: 'manual', purchasedAt: null } satisfies ShoppingListEntry;
    let list: ShoppingListEntry[] = [entry];
    let resolveUndo: (() => void) | undefined;
    const fetchMock = vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url === '/api/providers') return Promise.resolve(jsonResponse([]));
      if (url === '/api/shopping-list' && init?.method === undefined) return Promise.resolve(jsonResponse(list));
      if (url === '/api/inventory') return Promise.resolve(jsonResponse([inventoryItem('tea', 'Green Tea', 2)]));
      if (url === '/api/restock') return Promise.resolve(jsonResponse(null));
      if (url === '/api/shopping-list/items/manual-tea' && init?.method === 'DELETE') {
        list = [];
        return Promise.resolve(jsonResponse({}));
      }
      if (url === '/api/shopping-list/items/tea/undo-remove' && init?.method === 'POST') {
        return new Promise<Response>((resolve) => {
          resolveUndo = () => {
            list = [entry];
            resolve(jsonResponse(entry));
          };
        });
      }
      throw new Error(`Unexpected request: ${url}`);
    });
    vi.stubGlobal('fetch', fetchMock);
    renderPage();

    fireEvent.click(await screen.findByRole('button', { name: 'Remove Green Tea for this trip' }));
    const undo = await screen.findByRole('button', { name: 'Undo' });
    fireEvent.click(undo);
    expect(undo).toBeDisabled();
    fireEvent.click(undo);
    expect(fetchMock).toHaveBeenCalledWith(
      expect.stringContaining('/api/shopping-list/items/tea/undo-remove'),
      expect.anything(),
    );
    expect(fetchMock.mock.calls.filter(([u]) => String(u).includes('undo-remove')).length).toBe(1);
    resolveUndo?.();
    await waitFor(() => {
      expect(within(screen.getByRole('table', { name: 'Shopping plan entries' })).getByText('Green Tea')).toBeInTheDocument();
    });
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
