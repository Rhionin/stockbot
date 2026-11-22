import { readFileSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { StrictMode, useState } from 'react';
import { fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { MantineProvider } from '@mantine/core';
import { MemoryRouter, Route, Routes, useLocation } from 'react-router-dom';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import type { AuthSession } from '@stockbot/shared/contract';
import type { InventoryItem } from '@stockbot/shared/types';
import { HouseholdSessionContext } from '../auth/householdSession';
import type { HouseholdSessionValue } from '../auth/householdSession';
import { GroupRow } from './GroupRow';
import { InventoryPage } from './InventoryPage';
import { ItemDetailSheet } from './ItemDetailSheet';
import { ListViewProvider } from './ListViewProvider';
import { useListView } from './listViewContext';
import { ProductPhoto } from './ProductPhoto';
import type { ListViewSnapshot } from './listViewState';
import type { ShelfGroupRow } from './shelf';

class FakeEventSource {
  static instances: FakeEventSource[] = [];
  readonly url: string;
  constructor(url: string) {
    this.url = url;
    FakeEventSource.instances.push(this);
  }
  addEventListener() {}
  close() {}
}

const inventoryItem = (id: string, name: string, group?: InventoryItem['group']): InventoryItem => ({
  item: {
    id,
    userId: 'user-1',
    productId: `product-${id}`,
    product: { id: `product-${id}`, name, category: 'Pantry', unitOfMeasure: 'unit' },
    targetQuantity: null,
    createdAt: '2026-01-01T00:00:00Z',
  },
  instanceCount: 1,
  nearExpiryCount: 0,
  expiredCount: 0,
  needsAttention: false,
  group,
});

const json = (body: unknown, status = 200) =>
  new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  });

const restockFor = (id: string, name: string, months: number) => ({
  household: { kind: 'months', value: 3 },
  householdLabel: '3 months',
  appliesTo: 1,
  itemCount: 1,
  notRestocked: 0,
  suggestionCount: 0,
  categories: [],
  items: [{
    itemId: id,
    productId: `product-${id}`,
    name,
    unit: 'unit',
    target: { kind: 'months', value: months },
    level: 'item',
    sourceLabel: 'For this item',
    valueLabel: `${months} months`,
    rowSource: 'For this item',
    chain: [],
    categories: [],
    setLabel: 'Change',
  }],
});

const productBody = (id: string, name: string) => ({
  id: `product-${id}`,
  name,
  category: 'Pantry',
  unitOfMeasure: 'unit',
  createdAt: '2026-01-01T00:00:00Z',
  barcodes: ['000111222333'],
});

function installFetch(items: InventoryItem[], options?: { restockMonths?: number; fail?: boolean }) {
  const fetchMock = vi.fn((input: RequestInfo | URL) => {
    const url = String(input);
    if (options?.fail) return Promise.resolve(json({ error: 'Unable to load inventory.' }, 500));
    if (url === '/api/inventory') return Promise.resolve(json(items));
    if (url === '/api/restock' && items[0] && options?.restockMonths !== undefined) {
      return Promise.resolve(json(restockFor(items[0].item.id, items[0].item.product.name, options.restockMonths)));
    }
    const product = url.match(/^\/api\/products\/product-(.+)$/);
    if (product) {
      const item = items.find((entry) => entry.item.id === product[1]);
      return Promise.resolve(json(productBody(product[1], item?.item.product.name ?? 'Gone')));
    }
    if (/\/instances$/.test(url)) return Promise.resolve(json([]));
    if (url === '/api/settings/supply') return Promise.resolve(json({ months: 3, opening: false, wipePhrase: 'WIPE INVENTORY' }));
    return Promise.resolve(json([]));
  });
  vi.stubGlobal('fetch', fetchMock);
  return fetchMock;
}

function LocationProbe() {
  const location = useLocation();
  return <output data-testid="loc">{`${location.pathname}${location.search}`}</output>;
}

function renderInventory(path = '/inventory') {
  return render(
    <MantineProvider env="test">
      <MemoryRouter initialEntries={[path]}>
        <ListViewProvider>
          <LocationProbe />
          <Routes>
            <Route path="/inventory" element={<InventoryPage />} />
            <Route path="/inventory/:itemId/history" element={<p>Item history</p>} />
            <Route path="/history" element={<p>Ledger</p>} />
          </Routes>
        </ListViewProvider>
      </MemoryRouter>
    </MantineProvider>,
  );
}

const groupRow = (withItem: boolean): ShelfGroupRow => ({
  kind: 'group',
  id: 'group-1',
    group: {
    id: 'group-1',
    name: 'Beans',
    rule: 'same_as_ran_out',
    ruleConfirmed: true,
    members: [],
    runningLow: false,
  },
  members: [{
    productId: 'product-loose',
    name: 'Loose beans',
    onHand: 0,
    nearExpiryCount: 0,
    expiredCount: 0,
    itemId: withItem ? 'item-loose' : undefined,
  }],
  photoName: 'Beans',
  status: '0 on hand',
  rulePill: null,
  pinLabel: '',
  meter: { now: 0, max: 1, text: '0' },
});

beforeEach(() => {
  FakeEventSource.instances = [];
  vi.stubGlobal('EventSource', FakeEventSource);
});

describe('item detail sheet', () => {
  it('spec 01: a row body click opens the sheet at ?item=', async () => {
    installFetch([inventoryItem('bread', 'Sourdough')]);
    renderInventory();
    fireEvent.click(await screen.findByRole('button', { name: 'Details for Sourdough' }));
    expect(screen.getByTestId('loc')).toHaveTextContent('/inventory?item=bread');
    expect(await screen.findByRole('dialog', { name: 'Sourdough' })).toBeInTheDocument();
  });

  it('spec 02: the open control focus style is defined for keyboard users', () => {
    const css = readFileSync(join(dirname(fileURLToPath(import.meta.url)), 'shelf.css'), 'utf8');
    expect(css).toMatch(/\.shelf-open:focus-visible/);
  });

  it('spec 08: Close from a deep link removes item without a history entry', async () => {
    installFetch([inventoryItem('bread', 'Sourdough')]);
    renderInventory('/inventory?item=bread');
    const dialog = await screen.findByRole('dialog', { name: 'Sourdough' });
    fireEvent.click(within(dialog).getByRole('button', { name: 'Close' }));
    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument());
    expect(screen.getByTestId('loc')).toHaveTextContent('/inventory');
    expect(screen.getByTestId('loc').textContent).not.toContain('item=');
  });

  it('spec 09: an unknown item shows the gone message only after the list loads', async () => {
    let release: (value: Response) => void = () => {};
    const pending = new Promise<Response>((resolve) => {
      release = resolve;
    });
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL) => {
      const url = String(input);
      if (url === '/api/inventory') return pending;
      return Promise.resolve(json([]));
    }));
    renderInventory('/inventory?item=missing');
    const dialog = await screen.findByRole('dialog');
    expect(within(dialog).queryByText('This product is no longer in your inventory.')).not.toBeInTheDocument();
    expect(dialog.querySelector('.item-detail-title-skeleton')).not.toBeNull();
    release(json([]));
    expect(await screen.findByText('This product is no longer in your inventory.')).toHaveAttribute('role', 'status');
  });

  it('spec 10: a deep-link close focuses the Inventory heading', async () => {
    installFetch([inventoryItem('bread', 'Sourdough')]);
    renderInventory('/inventory?item=bread');
    const dialog = await screen.findByRole('dialog', { name: 'Sourdough' });
    await waitFor(() => expect(dialog.querySelector('.item-detail-title')).toHaveFocus());
    fireEvent.click(within(dialog).getByRole('button', { name: 'Close' }));
    await waitFor(() => expect(screen.getByRole('heading', { name: 'Inventory' })).toHaveFocus());
  });

  it('spec 11: the sheet reuses the product facts, instances and edit product', async () => {
    installFetch([inventoryItem('bread', 'Sourdough', {
      id: 'group-1',
      name: 'Bakery',
      rule: 'same_as_ran_out',
      ruleConfirmed: true,
      onHand: 1,
      memberCount: 1,
      members: [],
    })], { restockMonths: 3 });
    renderInventory('/inventory?item=bread');
    const dialog = await screen.findByRole('dialog', { name: 'Sourdough' });
    expect(within(dialog).getByText('This product is part of the Bakery group.')).toBeInTheDocument();
    expect(within(dialog).getByRole('link', { name: 'Open group settings ›' })).toHaveAttribute('href', '/groups/group-1');
    expect(await within(dialog).findByText('Barcode: 000111222333')).toBeInTheDocument();
    expect(within(dialog).getByRole('heading', { name: 'Sourdough instances' })).toBeInTheDocument();
    expect(within(dialog).getByRole('button', { name: 'Edit product' })).toBeInTheDocument();
    expect(within(dialog).getByRole('link', { name: 'History' })).toHaveAttribute('href', '/inventory/bread/history');
  });

  it('spec 12: a detail photo with no image says No photo', () => {
    render(
      <MantineProvider>
        <ProductPhoto name="Sourdough" detail />
      </MantineProvider>,
    );
    expect(screen.getByText('No photo')).toBeInTheDocument();
    expect(screen.getByLabelText('No photo of Sourdough')).toBeInTheDocument();
  });

  it('spec 15: the known-at line shows only when this mount already loaded data', async () => {
    const item = inventoryItem('bread', 'Sourdough');
    const props = {
      itemId: 'bread',
      item,
      hand: '1 unit',
      loading: false,
      loadError: '',
      knownAt: '3:04 PM',
      groupName: null,
      groupId: null,
      restockItem: null,
      section: null,
      child: null,
      onClose: () => {},
      onEditTarget: () => {},
      onOpenCategory: () => {},
      onRemoveCategory: () => {},
      onAddToGroup: () => {},
      onHandChange: () => {},
      onQuietSaved: () => {},
    };
    const { rerender } = render(
      <MantineProvider env="test">
        <MemoryRouter>
          <ItemDetailSheet {...props} offlineWithData={false} />
        </MemoryRouter>
      </MantineProvider>,
    );
    expect(await screen.findByRole('dialog', { name: 'Sourdough' })).toBeInTheDocument();
    expect(screen.queryByText(/You're offline/)).not.toBeInTheDocument();
    rerender(
      <MantineProvider env="test">
        <MemoryRouter>
          <ItemDetailSheet {...props} offlineWithData />
        </MemoryRouter>
      </MantineProvider>,
    );
    expect(screen.getByRole('status')).toHaveTextContent("You're offline. This is what we knew at 3:04 PM.");
  });

  it('spec 13: nothing on hand disables Stock out', async () => {
    const empty = inventoryItem('bread', 'Sourdough');
    empty.instanceCount = 0;
    installFetch([empty]);
    renderInventory('/inventory?item=bread');
    const dialog = await screen.findByRole('dialog', { name: 'Sourdough' });
    expect(await within(dialog).findByText('There is nothing on hand.')).toBeInTheDocument();
    expect(within(dialog).getByRole('button', { name: 'Stock out' })).toBeDisabled();
  });

  it('spec 16: a sheet whose item disappeared shows the gone status and focuses it', async () => {
    const item = inventoryItem('bread', 'Sourdough');
    const props = {
      itemId: 'bread',
      hand: '1 unit',
      loading: false,
      loadError: '',
      offlineWithData: false,
      knownAt: '',
      groupName: null,
      groupId: null,
      restockItem: null,
      section: null,
      child: null,
      onClose: () => {},
      onEditTarget: () => {},
      onOpenCategory: () => {},
      onRemoveCategory: () => {},
      onAddToGroup: () => {},
      onHandChange: () => {},
      onQuietSaved: () => {},
    };
    const { rerender } = render(
      <MantineProvider env="test">
        <MemoryRouter>
          <ItemDetailSheet {...props} item={item} />
        </MemoryRouter>
      </MantineProvider>,
    );
    expect(await screen.findByRole('dialog', { name: 'Sourdough' })).toBeInTheDocument();
    rerender(
      <MantineProvider env="test">
        <MemoryRouter>
          <ItemDetailSheet {...props} item={null} />
        </MemoryRouter>
      </MantineProvider>,
    );
    const gone = await screen.findByText('This product is no longer in your inventory.');
    expect(gone).toHaveAttribute('role', 'status');
    await waitFor(() => expect(gone).toHaveFocus());
  });

  it('spec 18: the editor shows the opened product target', async () => {
    installFetch([inventoryItem('bread', 'Sourdough')], { restockMonths: 4 });
    renderInventory();
    fireEvent.click(await screen.findByRole('button', { name: 'Actions for Sourdough' }));
    fireEvent.click(screen.getByRole('menuitem', { name: 'Restock target…' }));
    const dialog = await screen.findByRole('dialog', { name: 'Restock target for Sourdough' });
    expect(within(dialog).getByLabelText('Months')).toHaveValue('4');
  });

  it('spec 36: opening an item drops review and suggestion, and a suggestion drops item', async () => {
    installFetch([inventoryItem('bread', 'Sourdough')]);
    renderInventory('/inventory?review=1&suggestion=sug-1');
    await screen.findByRole('button', { name: 'Details for Sourdough' });
    fireEvent.click(screen.getByRole('button', { name: 'Details for Sourdough' }));
    await waitFor(() => {
      const loc = screen.getByTestId('loc').textContent ?? '';
      expect(loc).toContain('item=bread');
      expect(loc).not.toContain('review=');
      expect(loc).not.toContain('suggestion=');
    });
  });
});

describe('group members without an item row', () => {
  it('spec 07 and spec 32: a member with no itemId has no open control and its menu still opens', () => {
    const onOpenMember = vi.fn();
    render(
      <MantineProvider env="test">
        <MemoryRouter>
          <GroupRow
            row={groupRow(false)}
            expanded
            onToggle={vi.fn()}
            onMove={vi.fn()}
            onOpenMember={onOpenMember}
            onChanged={vi.fn()}
          />
        </MemoryRouter>
      </MantineProvider>,
    );
    const member = document.querySelector('.shelf-member') as HTMLElement;
    expect(member.className).not.toContain('is-openable');
    expect(within(member).queryByRole('button', { name: /Details for/ })).not.toBeInTheDocument();
    expect(within(member).queryByRole('link')).not.toBeInTheDocument();
    fireEvent.click(member);
    expect(onOpenMember).not.toHaveBeenCalled();
    fireEvent.click(within(member).getByRole('button', { name: 'Actions for Loose beans' }));
    expect(screen.getByRole('menuitem', { name: 'Move to another group' })).toBeInTheDocument();
    expect(screen.queryByRole('menuitem', { name: 'View details' })).not.toBeInTheDocument();
  });

  it('spec 07: a member with an item row opens that item', () => {
    const onOpenMember = vi.fn();
    const onToggle = vi.fn();
    render(
      <MantineProvider env="test">
        <MemoryRouter>
          <GroupRow
            row={groupRow(true)}
            expanded={false}
            onToggle={onToggle}
            onMove={vi.fn()}
            onOpenMember={onOpenMember}
            onChanged={vi.fn()}
          />
        </MemoryRouter>
      </MantineProvider>,
    );
    fireEvent.click(screen.getByRole('button', { name: 'Beans' }));
    expect(onToggle).toHaveBeenCalled();
    expect(onOpenMember).not.toHaveBeenCalled();
  });
});

describe('list view provider', () => {
  const sample = (): ListViewSnapshot => ({
    scrollTop: 10,
    listHeight: 20,
    search: 'oats',
    filter: 'low',
    expandedGroupId: 'g',
    lastItemId: 'item-1',
    before: [],
    after: [],
    itemOffset: 0,
    savedAt: Date.now(),
  });

  it('spec 23: switching household clears the saved view and nothing is written to storage', () => {
    const setItem = vi.spyOn(Storage.prototype, 'setItem');
    const Harness = ({ householdId }: { householdId: string }) => {
      const session: HouseholdSessionValue = {
        required: true,
        logout: () => {},
        replaceAuth: () => {},
        auth: {
          user: { id: 'user-1', email: 'ada@example.com', name: 'Ada' },
          kind: 'account',
          memberships: [],
          households: [],
          activeHouseholdId: householdId,
          operator: false,
        } as AuthSession,
      };
      return (
        <HouseholdSessionContext.Provider value={session}>
          <ListViewProvider>
            <Probe />
          </ListViewProvider>
        </HouseholdSessionContext.Provider>
      );
    };
    function Probe() {
      const api = useListView();
      const [label, setLabel] = useState('empty');
      return (
        <>
          <button type="button" onClick={() => { api.save(sample()); setLabel('saved'); }}>Save view</button>
          <button type="button" onClick={() => setLabel(api.peek()?.search ?? 'empty')}>Read view</button>
          <output>{label}</output>
        </>
      );
    }
    const { rerender } = render(<Harness householdId="house-a" />);
    fireEvent.click(screen.getByRole('button', { name: 'Save view' }));
    fireEvent.click(screen.getByRole('button', { name: 'Read view' }));
    expect(screen.getByRole('status', { name: '' }) || screen.getByText('oats')).toBeTruthy();
    expect(screen.getByText('oats')).toBeInTheDocument();
    rerender(<Harness householdId="house-b" />);
    fireEvent.click(screen.getByRole('button', { name: 'Read view' }));
    expect(screen.getByText('empty')).toBeInTheDocument();
    expect(setItem).not.toHaveBeenCalled();
    setItem.mockRestore();
  });
});

describe('restore once', () => {
  it('spec 38: StrictMode keeps the snapshot until the load that replaces the placeholder', async () => {
    const scroller = document.createElement('main');
    scroller.className = 'mantine-AppShell-main';
    const shell = document.createElement('div');
    shell.className = 'app-shell';
    shell.appendChild(scroller);
    document.body.appendChild(shell);
    Object.defineProperty(scroller, 'scrollHeight', { value: 4000, configurable: true });
    Object.defineProperty(scroller, 'clientHeight', { value: 800, configurable: true });

    const session: HouseholdSessionValue = {
      required: true,
      logout: () => {},
      replaceAuth: () => {},
      auth: {
        user: { id: 'user-1', email: 'ada@example.com', name: 'Ada' },
        kind: 'account',
        memberships: [],
        households: [],
        activeHouseholdId: 'house-a',
        operator: false,
      } as AuthSession,
    };
    function Seed() {
      const api = useListView();
      return (
        <button
          type="button"
          onClick={() => api.save({
            scrollTop: 640,
            listHeight: 1800,
            search: 'Sour',
            filter: '',
            expandedGroupId: null,
            lastItemId: 'bread',
            before: [],
            after: [],
            itemOffset: 0,
            savedAt: Date.now(),
          })}
        >
          Seed
        </button>
      );
    }
    installFetch([inventoryItem('bread', 'Sourdough')]);
    const { rerender } = render(
      <MantineProvider env="test">
        <HouseholdSessionContext.Provider value={session}>
          <ListViewProvider>
            <Seed />
          </ListViewProvider>
        </HouseholdSessionContext.Provider>
      </MantineProvider>,
    );
    fireEvent.click(screen.getByRole('button', { name: 'Seed' }));
    rerender(
      <MantineProvider env="test">
        <HouseholdSessionContext.Provider value={session}>
          <ListViewProvider>
            <StrictMode>
              <MemoryRouter initialEntries={['/inventory']}>
                <InventoryPage />
              </MemoryRouter>
            </StrictMode>
          </ListViewProvider>
        </HouseholdSessionContext.Provider>
      </MantineProvider>,
    );
    expect(await screen.findByRole('button', { name: 'Details for Sourdough' })).toBeInTheDocument();
    expect(screen.getByDisplayValue('Sour')).toBeInTheDocument();
    expect(scroller.scrollTop).toBe(640);
    shell.remove();
  });

  it('spec 39: a failed return keeps the saved search for the retry', async () => {
    const session: HouseholdSessionValue = {
      required: true,
      logout: () => {},
      replaceAuth: () => {},
      auth: {
        user: { id: 'user-1', email: 'ada@example.com', name: 'Ada' },
        kind: 'account',
        memberships: [],
        households: [],
        activeHouseholdId: 'house-a',
        operator: false,
      } as AuthSession,
    };
    let fail = true;
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL) => {
      const url = String(input);
      if (url === '/api/inventory') {
        return fail
          ? Promise.resolve(json({ error: 'Unable to load inventory.' }, 500))
          : Promise.resolve(json([inventoryItem('bread', 'Sourdough')]));
      }
      if (url.startsWith('/api/products/product-')) return Promise.resolve(json(productBody('bread', 'Sourdough')));
      if (/\/instances$/.test(url)) return Promise.resolve(json([]));
      if (url === '/api/settings/supply') return Promise.resolve(json({ months: 3, opening: false, wipePhrase: 'WIPE INVENTORY' }));
      return Promise.resolve(json([]));
    }));
    function Seed({ onReady }: { onReady: () => void }) {
      const api = useListView();
      return (
        <button
          type="button"
          onClick={() => {
            api.save({
              scrollTop: 0,
              listHeight: 100,
              search: 'Sour',
              filter: '',
              expandedGroupId: null,
              lastItemId: null,
              before: [],
              after: [],
              itemOffset: 0,
              savedAt: Date.now(),
            });
            onReady();
          }}
        >
          Seed
        </button>
      );
    }
    const view = render(
      <MantineProvider env="test">
        <HouseholdSessionContext.Provider value={session}>
          <ListViewProvider>
            <Seed onReady={() => {}} />
          </ListViewProvider>
        </HouseholdSessionContext.Provider>
      </MantineProvider>,
    );
    fireEvent.click(screen.getByRole('button', { name: 'Seed' }));
    view.rerender(
      <MantineProvider env="test">
        <HouseholdSessionContext.Provider value={session}>
          <ListViewProvider>
            <MemoryRouter initialEntries={['/inventory']}>
              <InventoryPage />
            </MemoryRouter>
          </ListViewProvider>
        </HouseholdSessionContext.Provider>
      </MantineProvider>,
    );
    expect(await screen.findByText('Unable to load inventory.')).toBeInTheDocument();
    expect(screen.getByDisplayValue('Sour')).toBeInTheDocument();
    fail = false;
    view.rerender(
      <MantineProvider env="test">
        <HouseholdSessionContext.Provider value={session}>
          <ListViewProvider>
            <MemoryRouter initialEntries={['/history']}>
              <p>Away</p>
            </MemoryRouter>
          </ListViewProvider>
        </HouseholdSessionContext.Provider>
      </MantineProvider>,
    );
    view.rerender(
      <MantineProvider env="test">
        <HouseholdSessionContext.Provider value={session}>
          <ListViewProvider>
            <MemoryRouter initialEntries={['/inventory']}>
              <InventoryPage />
            </MemoryRouter>
          </ListViewProvider>
        </HouseholdSessionContext.Provider>
      </MantineProvider>,
    );
    expect(await screen.findByRole('button', { name: 'Details for Sourdough' })).toBeInTheDocument();
    expect(screen.getByDisplayValue('Sour')).toBeInTheDocument();
  });
});
