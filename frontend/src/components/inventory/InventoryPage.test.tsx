import { fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { notifyApiSuccess, raiseOfflineBanner } from '@stockbot/shared/contract';
import { MantineProvider } from '@mantine/core';
import { Notifications } from '@mantine/notifications';
import { MemoryRouter } from 'react-router-dom';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import type { InventoryItem } from '@stockbot/shared/types';
import { InventoryPage } from './InventoryPage';

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

const inventoryItem = (
  id: string,
  name: string,
  category: string,
  needsAttention: boolean,
): InventoryItem => ({
  item: {
    id,
    userId: 'user-1',
    productId: `product-${id}`,
    product: { id: `product-${id}`, name, category, unitOfMeasure: 'unit' },
    targetQuantity: null,
    createdAt: '2026-01-01T00:00:00Z',
  },
  instanceCount: needsAttention ? 2 : 1,
  nearExpiryCount: needsAttention ? 1 : 0,
  expiredCount: needsAttention ? 1 : 0,
  needsAttention,
});

const jsonResponse = (body: unknown, status = 200) =>
  new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  });

describe('InventoryPage', () => {
  beforeEach(() => {
    FakeEventSource.instances = [];
    vi.stubGlobal('EventSource', FakeEventSource);
  });

  it('shows attention items first and filters the fetched inventory locally', async () => {
    const fetchMock = vi.fn(() => Promise.resolve(jsonResponse([
      inventoryItem('bread', 'Sourdough', 'Bakery', false),
      inventoryItem('milk', 'Whole Milk', 'Dairy', true),
    ])));
    vi.stubGlobal('fetch', fetchMock);

    render(<MantineProvider env="test"><MemoryRouter><InventoryPage /></MemoryRouter></MantineProvider>);

    await screen.findByText('Whole Milk');
    expect(screen.getByText('1 near expiry')).toBeInTheDocument();
    expect(screen.getByText('1 expired')).toBeInTheDocument();

    fireEvent.change(screen.getByLabelText('Search products or groups'), {
      target: { value: 'bakery' },
    });

    expect(screen.getByText('Sourdough')).toBeInTheDocument();
    expect(screen.queryByText('Whole Milk')).not.toBeInTheDocument();
    expect(fetchMock).toHaveBeenCalledWith('/api/inventory', expect.any(Object));
  });

  it('hides a missing category and keeps the source label out of the controls', async () => {
    const pineapple = inventoryItem('pineapple', 'Crushed Pineapple', 'undefined', false);
    pineapple.item.product.externalSource = 'openfoodfacts';
    const fetchMock = vi.fn(() => Promise.resolve(jsonResponse([
      pineapple,
      inventoryItem('milk', 'Evaporated milk', '', false),
      inventoryItem('pasta', 'Lasagna', 'Pastas', false),
    ])));
    vi.stubGlobal('fetch', fetchMock);

    render(<MantineProvider env="test"><MemoryRouter><InventoryPage /></MemoryRouter></MantineProvider>);

    await screen.findByText('Crushed Pineapple');
    expect(screen.queryByText(/^undefined$/i)).not.toBeInTheDocument();
    expect(screen.queryByText(/^null$/i)).not.toBeInTheDocument();
    expect(screen.getByText('Pastas')).toBeInTheDocument();

    const articleNamed = (name: string) => screen.getAllByRole('article').find((article) =>
      within(article).queryByRole('heading', { name }) !== null,
    );
    const pineappleArticle = articleNamed('Crushed Pineapple');
    const milkArticle = articleNamed('Evaporated milk');
    expect(pineappleArticle).toBeDefined();
    expect(milkArticle).toBeDefined();
    const source = within(pineappleArticle as HTMLElement).getByText('Open Food Facts');
    expect(source.closest('a, button, .mantine-Badge-root')).toBeNull();
    expect(within(milkArticle as HTMLElement).queryByText('Open Food Facts')).not.toBeInTheDocument();
  });

  it('fetches instances only after an item is selected', async () => {
    const fetchMock = vi.fn((input: RequestInfo | URL) => {
      const url = String(input);
      if (url === '/api/inventory') {
        return Promise.resolve(jsonResponse([
          inventoryItem('bread', 'Sourdough', 'Bakery', false),
        ]));
      }
      if (url === '/api/inventory/bread/instances') {
        return Promise.resolve(jsonResponse([]));
      }
      if (url === '/api/settings/supply') {
        return Promise.resolve(jsonResponse({ months: 3, opening: false, wipePhrase: 'WIPE INVENTORY' }));
      }
      if (url === '/api/products/product-bread') {
        return Promise.resolve(jsonResponse({
          id: 'product-bread',
          name: 'Sourdough',
          category: 'Bakery',
          unitOfMeasure: 'unit',
          createdAt: '2026-01-01T00:00:00Z',
          barcodes: [],
        }));
      }
      throw new Error(`Unexpected request: ${url}`);
    });
    vi.stubGlobal('fetch', fetchMock);

    render(<MantineProvider env="test"><MemoryRouter><InventoryPage /></MemoryRouter></MantineProvider>);
    await screen.findByText('Sourdough');

    fireEvent.click(screen.getByRole('button', { name: 'Actions for Sourdough' }));
    fireEvent.click(screen.getByRole('menuitem', { name: 'View instances' }));

    const article = screen.getByRole('article');
    await within(article).findByRole('heading', { name: 'Sourdough instances' });
    await waitFor(() => expect(fetchMock).toHaveBeenCalledWith(
      '/api/inventory/bread/instances',
      expect.any(Object),
    ));
  });

  it('updates a displayed item pushed over the event stream', async () => {
    const fetchMock = vi.fn(() => Promise.resolve(jsonResponse([
      inventoryItem('bread', 'Sourdough', 'Bakery', false),
    ])));
    vi.stubGlobal('fetch', fetchMock);

    render(<MantineProvider env="test"><MemoryRouter><InventoryPage /></MemoryRouter></MantineProvider>);
    await screen.findByText('Sourdough');

    const eventSource = FakeEventSource.instances[0];
    expect(eventSource.url).toBe('/api/events');
    expect(eventSource.hasListener('error')).toBe(true);

    eventSource.dispatch('inventory', inventoryItem('bread', 'Sourdough', 'Bakery', true));

    expect(await screen.findByText('1 near expiry')).toBeInTheDocument();
    expect(screen.getByText('Sourdough')).toBeInTheDocument();
  });

  it('ignores a manufactured error event and leaves displayed inventory unchanged', async () => {
    const fetchMock = vi.fn(() => Promise.resolve(jsonResponse([
      inventoryItem('bread', 'Sourdough', 'Bakery', false),
    ])));
    vi.stubGlobal('fetch', fetchMock);

    render(<MantineProvider env="test"><MemoryRouter><InventoryPage /></MemoryRouter></MantineProvider>);
    await screen.findByText('Sourdough');

    const eventSource = FakeEventSource.instances[0];
    eventSource.dispatch('error', {});

    expect(screen.getByText('Sourdough')).toBeInTheDocument();
    expect(screen.queryByText('1 near expiry')).not.toBeInTheDocument();
  });

  it('does not call an offline load a search miss', async () => {
    notifyApiSuccess();
    vi.stubGlobal('fetch', vi.fn(() => Promise.reject(new TypeError('Failed to fetch'))));
    render(<MantineProvider env="test"><MemoryRouter><InventoryPage /></MemoryRouter></MantineProvider>);
    await waitFor(() => expect(screen.queryByLabelText('Loading inventory')).not.toBeInTheDocument());
    expect(screen.queryByText('No products or groups match.')).not.toBeInTheDocument();
    expect(screen.queryByText('Your inventory is empty.')).not.toBeInTheDocument();
  });

  it('does not reload inventory when a normal failure is followed by a cleared banner', async () => {
    notifyApiSuccess();
    const fetchMock = vi.fn(() => Promise.resolve(jsonResponse({ error: 'Unable to load inventory.' }, 500)));
    vi.stubGlobal('fetch', fetchMock);
    render(<MantineProvider env="test"><MemoryRouter><InventoryPage /></MemoryRouter></MantineProvider>);
    expect(await screen.findByText('Unable to load inventory.')).toBeInTheDocument();
    const seen = fetchMock.mock.calls.length;
    raiseOfflineBanner();
    notifyApiSuccess();
    expect(fetchMock.mock.calls.length).toBe(seen);
  });

  it('closes the event stream connection on unmount', async () => {
    const fetchMock = vi.fn(() => Promise.resolve(jsonResponse([])));
    vi.stubGlobal('fetch', fetchMock);

    const { unmount } = render(<MantineProvider env="test"><MemoryRouter><InventoryPage /></MemoryRouter></MantineProvider>);
    await screen.findByText('Your inventory is empty.');

    const eventSource = FakeEventSource.instances[0];
    expect(eventSource.closed).toBe(false);

    unmount();

    expect(eventSource.closed).toBe(true);
  });

  it('changes the on-hand count from the expanded row without collapsing it', async () => {
    let onHand = 0;
    const oats = (): InventoryItem => ({
      ...inventoryItem('oats', 'Rolled Oats', 'Grains', false),
      instanceCount: onHand,
    });
    const fetchMock = vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      const method = init?.method ?? 'GET';
      if (url === '/api/settings/supply') {
        return Promise.resolve(jsonResponse({ months: 3, opening: false, wipePhrase: 'WIPE INVENTORY' }));
      }
      if (url === '/api/inventory' && method === 'GET') {
        return Promise.resolve(jsonResponse([oats()]));
      }
      if (url === '/api/inventory/oats/instances' && method === 'GET') {
        return Promise.resolve(jsonResponse([]));
      }
      if (url === '/api/inventory/oats/instances' && method === 'POST') {
        onHand += 1;
        return Promise.resolve(jsonResponse({ id: `inst-${onHand}`, itemId: 'oats' }, 201));
      }
      if (url === '/api/inventory/oats/stock-out' && method === 'POST') {
        onHand = Math.max(0, onHand - 1);
        return Promise.resolve(jsonResponse({}));
      }
      if (url === '/api/products/product-oats' && method === 'GET') {
        return Promise.resolve(jsonResponse({
          id: 'product-oats',
          name: 'Rolled Oats',
          category: 'Grains',
          unitOfMeasure: 'unit',
          createdAt: '2026-01-01T00:00:00Z',
          barcodes: [],
        }));
      }
      throw new Error(`Unexpected request: ${method} ${url}`);
    });
    vi.stubGlobal('fetch', fetchMock);

    render(<MantineProvider env="test"><MemoryRouter><InventoryPage /></MemoryRouter></MantineProvider>);
    await screen.findByText('Rolled Oats');
    expect(screen.getByText('0 unit')).toBeInTheDocument();

    fireEvent.click(screen.getByRole('button', { name: 'Actions for Rolled Oats' }));
    fireEvent.click(screen.getByRole('menuitem', { name: 'View instances' }));
    const article = screen.getByRole('article');
    expect(within(article).getByRole('button', { name: 'Stock out' })).toBeDisabled();
    expect(within(article).getByText('There is nothing on hand.')).toBeInTheDocument();

    fireEvent.click(within(article).getByRole('button', { name: 'Stock in' }));
    await within(article).findByText('1 unit');
    fireEvent.click(screen.getByRole('button', { name: 'Actions for Rolled Oats' }));
    expect(screen.getByRole('menuitem', { name: 'Hide instances' })).toBeInTheDocument();
    fireEvent.keyDown(screen.getByRole('menu'), { key: 'Escape' });
    expect(within(article).getByRole('button', { name: 'Stock out' })).toBeEnabled();
    expect(within(article).queryByText('There is nothing on hand.')).not.toBeInTheDocument();

    fireEvent.click(within(article).getByRole('button', { name: 'Stock out' }));
    await within(article).findByText('0 unit');
    fireEvent.click(screen.getByRole('button', { name: 'Actions for Rolled Oats' }));
    expect(screen.getByRole('menuitem', { name: 'Hide instances' })).toBeInTheDocument();
    fireEvent.keyDown(screen.getByRole('menu'), { key: 'Escape' });
    expect(within(article).getByRole('button', { name: 'Stock out' })).toBeDisabled();
    expect(within(article).getByText('There is nothing on hand.')).toBeInTheDocument();
  });

  it('shows the opening banner only while the first scan is unfinished', async () => {
    let opening = true;
    const fetchMock = vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      const method = init?.method ?? 'GET';
      if (url === '/api/settings/supply') {
        return Promise.resolve(jsonResponse({ months: 3, opening, wipePhrase: 'WIPE INVENTORY' }));
      }
      if (url === '/api/onboarding/complete' && method === 'POST') {
        opening = false;
        return Promise.resolve(jsonResponse({ startedAt: '2026-04-01T00:00:00Z' }));
      }
      if (url === '/api/inventory') {
        return Promise.resolve(jsonResponse([inventoryItem('bread', 'Sourdough', 'Bakery', false)]));
      }
      throw new Error(`Unexpected request: ${method} ${url}`);
    });
    vi.stubGlobal('fetch', fetchMock);

    render(<MantineProvider env="test"><MemoryRouter><InventoryPage /></MemoryRouter></MantineProvider>);
    await screen.findByText('Sourdough');
    expect(screen.getByRole('alert', { name: 'Opening inventory' })).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Wipe inventory' })).not.toBeInTheDocument();

    fireEvent.click(screen.getByRole('button', { name: 'This scan is complete' }));
    await waitFor(() => expect(screen.queryByRole('alert', { name: 'Opening inventory' })).not.toBeInTheDocument());
    expect(fetchMock).toHaveBeenCalledWith('/api/onboarding/complete', expect.objectContaining({ method: 'POST' }));
  });

  it('shows the product barcode only in the expanded row', async () => {
    const fetchMock = vi.fn((input: RequestInfo | URL) => {
      const url = String(input);
      if (url === '/api/inventory') {
        return Promise.resolve(jsonResponse([
          inventoryItem('bread', 'Sourdough', 'Bakery', false),
        ]));
      }
      if (url === '/api/settings/supply') {
        return Promise.resolve(jsonResponse({ months: 3, opening: false, wipePhrase: 'WIPE INVENTORY' }));
      }
      if (url === '/api/inventory/bread/instances') {
        return Promise.resolve(jsonResponse([]));
      }
      if (url === '/api/products/product-bread') {
        return Promise.resolve(jsonResponse({
          id: 'product-bread',
          name: 'Sourdough',
          category: 'Bakery',
          unitOfMeasure: 'unit',
          createdAt: '2026-01-01T00:00:00Z',
          barcodes: ['012345678905', '998877665544'],
        }));
      }
      throw new Error(`Unexpected request: ${url}`);
    });
    vi.stubGlobal('fetch', fetchMock);

    render(<MantineProvider env="test"><MemoryRouter><InventoryPage /></MemoryRouter></MantineProvider>);
    const article = await screen.findByRole('article');
    expect(within(article).queryByText(/Barcode:/)).not.toBeInTheDocument();

    fireEvent.click(screen.getByRole('button', { name: 'Actions for Sourdough' }));
    fireEvent.click(screen.getByRole('menuitem', { name: 'View instances' }));

    const barcode = await within(article).findByText('Barcode: 012345678905, 998877665544');
    expect(barcode).toHaveClass('copyable-barcode');
    expect(barcode.closest('button')).toBeNull();

    fireEvent.click(screen.getByRole('button', { name: 'Actions for Sourdough' }));
    fireEvent.click(screen.getByRole('menuitem', { name: 'Hide instances' }));
    expect(within(article).queryByText(/Barcode:/)).not.toBeInTheDocument();
  });

  it('expands one group row and previews the rule', async () => {
    const group = {
      id: 'g-beans',
      name: 'Cut green beans',
      rule: 'same_as_ran_out',
      ruleConfirmed: false,
      onHand: 5,
      memberCount: 2,
      members: [
        { productId: 'product-gv', name: 'Great Value Cut Green Beans', onHand: 3, barcodes: ['111'] },
        { productId: 'product-kr', name: 'Kroger Cut Green Beans', onHand: 2 },
      ],
    };
    const gv = inventoryItem('gv', 'Great Value Cut Green Beans', 'Canned', false);
    const kr = inventoryItem('kr', 'Kroger Cut Green Beans', 'Canned', false);
    gv.group = group;
    kr.group = group;
    const fetchMock = vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      const method = init?.method ?? 'GET';
      if (url === '/api/groups/preview' && method === 'POST') {
        return Promise.resolve(jsonResponse({
          productId: 'product-gv',
          because: 'Same as what ran out',
          buy: 1,
          explain: 'Buy the can that ran out.',
        }));
      }
      if (url === '/api/settings/supply') {
        return Promise.resolve(jsonResponse({ months: 3, opening: false, wipePhrase: 'WIPE INVENTORY' }));
      }
      return Promise.resolve(jsonResponse([gv, kr]));
    });
    vi.stubGlobal('fetch', fetchMock);

    render(<MantineProvider env="test"><MemoryRouter><InventoryPage /></MemoryRouter></MantineProvider>);
    expect(await screen.findByRole('heading', { name: 'Cut green beans' })).toBeInTheDocument();
    expect(screen.queryByRole('heading', { name: 'Great Value Cut Green Beans' })).not.toBeInTheDocument();
    expect(screen.getByText('Pick a rule')).toBeInTheDocument();

    fireEvent.click(screen.getByRole('button', { name: 'Expand Cut green beans' }));
    expect(await screen.findByText('Great Value Cut Green Beans')).toBeInTheDocument();
    expect(screen.getByText('unit, 3 on hand')).toBeInTheDocument();
    expect(screen.getByText('Barcode: 111')).toBeInTheDocument();
    expect(screen.getByText('5 units on hand')).toBeInTheDocument();
    expect(screen.getByRole('link', { name: 'Open group settings ›' })).toHaveAttribute('href', '/groups/g-beans');
  });

  it('groups two selected products', async () => {
    const milk = inventoryItem('milk', 'Whole Milk', 'Dairy', false);
    const oats = inventoryItem('oats', 'Oats', 'Cereal', false);
    const fetchMock = vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      const method = init?.method ?? 'GET';
      if (url === '/api/groups' && method === 'GET') {
        return Promise.resolve(jsonResponse([]));
      }
      if (url === '/api/groups' && method === 'POST') {
        return Promise.resolve(jsonResponse({ id: 'g-new', name: 'Breakfast', members: [] }));
      }
      if (url === '/api/settings/supply') {
        return Promise.resolve(jsonResponse({ months: 3, opening: false, wipePhrase: 'WIPE INVENTORY' }));
      }
      return Promise.resolve(jsonResponse([milk, oats]));
    });
    vi.stubGlobal('fetch', fetchMock);

    render(<MantineProvider env="test"><MemoryRouter><InventoryPage /></MemoryRouter></MantineProvider>);
    await screen.findByText('Whole Milk');
    fireEvent.click(screen.getByRole('button', { name: 'Select' }));
    fireEvent.click(screen.getByRole('checkbox', { name: 'Select Whole Milk' }));
    fireEvent.click(screen.getByRole('checkbox', { name: 'Select Oats' }));
    fireEvent.change(screen.getByLabelText('New group'), { target: { value: 'Breakfast' } });
    fireEvent.click(screen.getByRole('button', { name: 'Group these' }));

    await waitFor(() => expect(fetchMock).toHaveBeenCalledWith('/api/groups', expect.objectContaining({
      method: 'POST',
      body: JSON.stringify({ name: 'Breakfast', productIds: ['product-milk', 'product-oats'] }),
    })));
  });

  it('adds an ungrouped product to an existing group', async () => {
    const milk = inventoryItem('milk', 'Whole Milk', 'Dairy', false);
    const posts: string[] = [];
    const fetchMock = vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      const method = init?.method ?? 'GET';
      if (url === '/api/groups' && method === 'GET') {
        return Promise.resolve(jsonResponse([{
          id: 'g-breakfast',
          name: 'Breakfast',
          rule: 'same_as_ran_out',
          ruleConfirmed: false,
          members: [],
          runningLow: false,
        }]));
      }
      if (url === '/api/groups/g-breakfast/members' && method === 'POST') {
        posts.push(String(init?.body));
        return Promise.resolve(jsonResponse({ id: 'g-breakfast', name: 'Breakfast', members: [] }));
      }
      if (url === '/api/settings/supply') {
        return Promise.resolve(jsonResponse({ months: 3, opening: false, wipePhrase: 'WIPE INVENTORY' }));
      }
      return Promise.resolve(jsonResponse([milk]));
    });
    vi.stubGlobal('fetch', fetchMock);

    render(<MantineProvider env="test"><MemoryRouter><InventoryPage /></MemoryRouter></MantineProvider>);
    fireEvent.click(await screen.findByRole('button', { name: 'Actions for Whole Milk' }));
    expect(screen.getByRole('menuitem', { name: 'Add to a group…' })).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: 'Select' }));
    expect(screen.queryByRole('button', { name: 'Actions for Whole Milk' })).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: 'Done' }));
    fireEvent.click(screen.getByRole('button', { name: 'Actions for Whole Milk' }));
    fireEvent.click(screen.getByRole('menuitem', { name: 'Add to a group…' }));
    fireEvent.click(await screen.findByRole('button', { name: 'Breakfast' }));
    await waitFor(() => expect(posts).toEqual([
      JSON.stringify({ productIds: ['product-milk'] }),
    ]));
  });

  it('moves a product that is already in a group', async () => {
    const group = {
      id: 'g-beans',
      name: 'Cut green beans',
      rule: 'same_as_ran_out',
      ruleConfirmed: true,
      onHand: 3,
      memberCount: 1,
      members: [{ productId: 'product-gv', name: 'Great Value Cut Green Beans', onHand: 3 }],
    };
    const gv = inventoryItem('gv', 'Great Value Cut Green Beans', 'Canned', false);
    gv.group = group;
    const posts: string[] = [];
    const fetchMock = vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      const method = init?.method ?? 'GET';
      if (url === '/api/groups' && method === 'GET') {
        return Promise.resolve(jsonResponse([
          {
            id: 'g-beans',
            name: 'Cut green beans',
            rule: 'same_as_ran_out',
            ruleConfirmed: true,
            members: [{ productId: 'product-gv', name: 'Great Value Cut Green Beans', onHand: 3 }],
            runningLow: false,
          },
          {
            id: 'g-soup',
            name: 'Soup',
            rule: 'same_as_ran_out',
            ruleConfirmed: false,
            members: [],
            runningLow: false,
          },
        ]));
      }
      if (url === '/api/groups/g-soup/members' && method === 'POST') {
        posts.push(String(init?.body));
        return Promise.resolve(jsonResponse({ id: 'g-soup', name: 'Soup', members: [] }));
      }
      if (url === '/api/settings/supply') {
        return Promise.resolve(jsonResponse({ months: 3, opening: false, wipePhrase: 'WIPE INVENTORY' }));
      }
      return Promise.resolve(jsonResponse([gv]));
    });
    vi.stubGlobal('fetch', fetchMock);

    render(<MantineProvider env="test"><MemoryRouter><InventoryPage /></MemoryRouter></MantineProvider>);
    await screen.findByRole('heading', { name: 'Cut green beans' });
    fireEvent.click(screen.getByRole('button', { name: 'Expand Cut green beans' }));
    fireEvent.click(screen.getByRole('button', { name: 'Actions for Great Value Cut Green Beans' }));
    fireEvent.click(screen.getByRole('menuitem', { name: 'Move to another group' }));
    expect(await screen.findByText('Already in Cut green beans.')).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Cut green beans' })).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: 'Soup' }));
    await waitFor(() => expect(posts).toEqual([
      JSON.stringify({ productIds: ['product-gv'], fromGroupId: 'g-beans' }),
    ]));
  });

  it('reloads when a grouped product changes', async () => {
    const group = {
      id: 'g-beans',
      name: 'Cut green beans',
      rule: 'same_as_ran_out',
      ruleConfirmed: true,
      onHand: 1,
      memberCount: 1,
      members: [{ productId: 'product-gv', name: 'Great Value Cut Green Beans', onHand: 1 }],
    };
    const gv = inventoryItem('gv', 'Great Value Cut Green Beans', 'Canned', false);
    gv.group = group;
    let inventoryCalls = 0;
    const fetchMock = vi.fn((input: RequestInfo | URL) => {
      const url = String(input);
      if (url === '/api/inventory') inventoryCalls += 1;
      if (url === '/api/settings/supply') {
        return Promise.resolve(jsonResponse({ months: 3, opening: false, wipePhrase: 'WIPE INVENTORY' }));
      }
      return Promise.resolve(jsonResponse([gv]));
    });
    vi.stubGlobal('fetch', fetchMock);

    render(<MantineProvider env="test"><MemoryRouter><InventoryPage /></MemoryRouter></MantineProvider>);
    await screen.findByText('Cut green beans');
    const before = inventoryCalls;
    FakeEventSource.instances[0]?.dispatch('inventory', gv);
    await waitFor(() => expect(inventoryCalls).toBeGreaterThan(before));
  });

  it('adds a barcodeless product at zero on hand from the sheet', async () => {
    let created = '';
    const fetchMock = vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url === '/api/products' && init?.method === 'POST') {
        created = String(init.body);
        return Promise.resolve(jsonResponse({
          id: 'prod-jam',
          name: 'Homemade jam',
          category: 'Preserves',
          unitOfMeasure: '',
          itemId: 'item-jam',
          createdAt: '2026-01-01T00:00:00Z',
        }, 201));
      }
      if (url === '/api/inventory') {
        return Promise.resolve(jsonResponse([]));
      }
      if (url === '/api/settings/supply') {
        return Promise.resolve(jsonResponse({ months: 3, opening: false, wipePhrase: 'WIPE INVENTORY' }));
      }
      return Promise.resolve(jsonResponse([]));
    });
    vi.stubGlobal('fetch', fetchMock);

    render(<MantineProvider env="test"><Notifications /><MemoryRouter><InventoryPage /></MemoryRouter></MantineProvider>);

    fireEvent.click(screen.getByRole('button', { name: 'Add an item' }));
    await screen.findByRole('heading', { name: 'Add an item' });
    fireEvent.change(await screen.findByRole('textbox', { name: 'Name' }), { target: { value: 'Homemade jam' } });
    fireEvent.change(screen.getByRole('textbox', { name: 'Brand' }), { target: { value: 'Pantry' } });
    fireEvent.change(screen.getByRole('textbox', { name: 'Category' }), { target: { value: 'Preserves' } });
    fireEvent.click(screen.getByRole('button', { name: 'Add item' }));

    await waitFor(() => expect(created).toContain('Homemade jam'));
    expect(created).toContain('Pantry');
    await waitFor(() => expect(fetchMock).toHaveBeenCalledWith('/api/inventory', expect.any(Object)));
  });
});
