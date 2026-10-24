import { fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { MantineProvider } from '@mantine/core';
import { MemoryRouter, Route, Routes } from 'react-router-dom';
import { describe, expect, it, vi } from 'vitest';
import type { InventoryItem, Product, ProductGroup } from '@stockbot/shared/types';
import { GroupDetailPage } from './GroupDetailPage';

const json = (body: unknown, status = 200) =>
  new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } });

const member = (productId: string, name: string, onHand: number) => ({ productId, name, onHand });

const baseGroup = (): ProductGroup => ({
  id: 'g1',
  name: 'Gatorade powder',
  rule: 'same_as_ran_out',
  ruleConfirmed: false,
  members: [
    member('punch', 'Fruit punch thirst quencher powder', 2),
    member('thirst', 'Thirst Quencher Powder', 4),
  ],
  runningLow: false,
});

const product = (id: string, name: string): Product => ({
  id,
  name,
  category: 'Drinks',
  unitOfMeasure: 'canister',
  createdAt: '2026-01-01T00:00:00Z',
});

const inventoryRow = (id: string, name: string, count: number): InventoryItem => ({
  item: {
    id: `item-${id}`,
    userId: 'user-1',
    productId: id,
    product: { id, name, category: 'Drinks', unitOfMeasure: 'canister' },
    targetQuantity: null,
    createdAt: '2026-01-01T00:00:00Z',
  },
  instanceCount: count,
  nearExpiryCount: 0,
  expiredCount: 0,
  needsAttention: false,
});

const renderDetail = (
  handler: (url: string, init?: RequestInit) => Response | Promise<Response>,
) => {
  vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL, init?: RequestInit) => Promise.resolve(handler(String(input), init))));
  return render(
    // env="test" disables Popover hideDetached. Mantine 8+ turns that on,
    // and jsdom elements have no box, so the rule sheet's options never open.
    <MantineProvider env="test">
      <MemoryRouter initialEntries={['/groups/g1']}>
        <Routes>
          <Route path="/groups/:id" element={<GroupDetailPage />} />
          <Route path="/inventory" element={<p>Inventory</p>} />
        </Routes>
      </MemoryRouter>
    </MantineProvider>,
  );
};

describe('GroupDetailPage', () => {
  it('opens the rule sheet from the badge and shows the saved rule', async () => {
    let current = baseGroup();
    const calls: { url: string; method: string; body?: string }[] = [];
    renderDetail((url, init) => {
      const method = init?.method ?? 'GET';
      calls.push({ url, method, body: typeof init?.body === 'string' ? init.body : undefined });
      if (url === '/api/groups/g1' && method === 'GET') return json(current);
      if (url === '/api/groups' && method === 'GET') return json([current]);
      if (url === '/api/products' && method === 'GET') return json([product('glacier', 'Gatorade Glacier Freeze')]);
      if (url === '/api/inventory') return json([inventoryRow('glacier', 'Gatorade Glacier Freeze', 1)]);
      if (url.startsWith('/api/products/')) return json({ barcodes: ['052000340075'] });
      if (url === '/api/groups/preview') {
        return json({ productId: 'punch', because: 'Same as what ran out', buy: 1, explain: 'Buy the powder that ran out.' });
      }
      if (url === '/api/groups/g1/rule' && method === 'PUT') {
        const body = JSON.parse(String(init?.body)) as { rule: string; pinnedProductId: string };
        current = { ...current, rule: body.rule, pinnedProductId: body.pinnedProductId, ruleConfirmed: true };
        return json(current);
      }
      if (url === '/api/groups/g1/target' && method === 'PUT') return json(current);
      throw new Error(`Unexpected ${method} ${url}`);
    });

    expect(await screen.findByRole('button', { name: 'Set time' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Set amount' })).toBeInTheDocument();
    expect(screen.getByText('Set an amount or time')).toBeInTheDocument();
    const emptyMeter = screen.getByRole('meter', { name: 'Stock on hand' });
    expect(within(emptyMeter).getByText('No sizes listed')).toBeInTheDocument();
    expect(emptyMeter).toHaveAttribute('aria-valuenow', '0');
    expect(emptyMeter).toHaveAttribute('aria-valuemax', '1');
    expect(emptyMeter).toHaveAttribute('aria-valuetext', 'No sizes listed');
    expect(screen.getByRole('button', { name: 'Pick a rule' })).toBeInTheDocument();
    expect(screen.queryByText('Still using Same product until you pick a rule.')).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Change target' })).not.toBeInTheDocument();
    expect(screen.queryByLabelText('Ounces')).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: 'Set time' }));
    expect(screen.getByLabelText('Months')).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Use the household default' })).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Use the account window' })).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: 'Cancel' }));

    fireEvent.click(screen.getByRole('button', { name: 'Pick a rule' }));
    const picker = await screen.findByRole('dialog', { name: 'What to buy next' });
    expect(within(picker).getByRole('radio', { name: 'Same product' })).toBeChecked();
    expect(within(picker).getByText('Rebuy the exact product you used last.')).toBeInTheDocument();
    expect(within(picker).getByText('Always buy one product.')).toBeInTheDocument();
    expect(within(picker).getByText('Lowest price per ounce.')).toBeInTheDocument();
    expect(within(picker).queryByText(/Next trip:/)).not.toBeInTheDocument();
    expect(within(picker).queryByText(/Now:/)).not.toBeInTheDocument();
    expect(within(picker).queryByRole('combobox', { name: 'Keep on hand' })).not.toBeInTheDocument();
    expect(within(picker).queryByText(/kind/)).not.toBeInTheDocument();
    expect(within(picker).queryByText(/ran out/)).not.toBeInTheDocument();

    fireEvent.click(within(picker).getByRole('radio', { name: 'Favorite' }));
    fireEvent.click(within(picker).getByRole('combobox', { name: 'Product' }));
    fireEvent.click(await screen.findByRole('option', { name: 'Fruit punch thirst quencher powder' }));
    fireEvent.click(within(picker).getByRole('button', { name: 'Save' }));

    expect(await screen.findByRole('button', { name: 'Change rule, Fruit punch thirst quencher powder' })).toBeInTheDocument();
    expect(screen.queryByText('When this runs out, always buy Fruit punch thirst quencher powder.')).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Pick a rule' })).not.toBeInTheDocument();
    const saved = calls.find((call) => call.url === '/api/groups/g1/rule');
    expect(JSON.parse(saved?.body ?? '{}')).toEqual({
      rule: 'favorite',
      pinnedProductId: 'punch',
      confirm: true,
    });
  });

  it('adds an ungrouped product and asks which target to keep when supply settings differ', async () => {
    let current = baseGroup();
    let membersPosts = 0;
    renderDetail((url, init) => {
      const method = init?.method ?? 'GET';
      if (url === '/api/groups/g1' && method === 'GET') return json(current);
      if (url === '/api/groups' && method === 'GET') {
        return json([current, {
          id: 'g2',
          name: 'Milk',
          rule: 'favorite',
          ruleConfirmed: true,
          members: [member('milk', 'Whole Milk', 1)],
          runningLow: false,
        }]);
      }
      if (url === '/api/products' && method === 'GET') {
        return json([
          product('punch', 'Fruit punch thirst quencher powder'),
          product('glacier', 'Gatorade Glacier Freeze'),
          product('lemon', 'Lemonade powder'),
          product('milk', 'Whole Milk'),
        ]);
      }
      if (url === '/api/inventory') {
        return json([
          inventoryRow('punch', 'Fruit punch thirst quencher powder', 2),
          inventoryRow('glacier', 'Gatorade Glacier Freeze', 1),
          inventoryRow('milk', 'Whole Milk', 1),
        ]);
      }
      if (url.startsWith('/api/products/')) return json({ barcodes: [] });
      if (url === '/api/groups/g1/members' && method === 'POST') {
        membersPosts += 1;
        const body = JSON.parse(String(init?.body)) as { productIds: string[]; target?: { clear?: boolean } };
        if (membersPosts === 1) {
          return json({
            error: 'Choose what this group should keep on hand.',
            code: 'target_decision_required',
            members: [{ productId: 'glacier', name: 'Gatorade Glacier Freeze', quantity: 12 }],
          }, 409);
        }
        expect(body.productIds).toEqual(['glacier']);
        expect(body.target).toEqual({ clear: true });
        current = {
          ...current,
          members: [...current.members, member('glacier', 'Gatorade Glacier Freeze', 1)],
        };
        return json(current);
      }
      throw new Error(`Unexpected ${method} ${url}`);
    });

    fireEvent.click(await screen.findByRole('button', { name: 'Add a product' }));
    expect(await screen.findByRole('checkbox', { name: 'Gatorade Glacier Freeze · 1 on hand' })).toBeInTheDocument();
    expect(screen.getByRole('checkbox', { name: 'Lemonade powder' })).toBeInTheDocument();
    expect(screen.queryByRole('checkbox', { name: /Fruit punch/ })).not.toBeInTheDocument();
    expect(screen.queryByRole('checkbox', { name: /Whole Milk/ })).not.toBeInTheDocument();

    fireEvent.change(screen.getByLabelText('Search products'), { target: { value: 'gatorade' } });
    expect(screen.queryByRole('checkbox', { name: 'Lemonade powder' })).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole('checkbox', { name: 'Gatorade Glacier Freeze · 1 on hand' }));
    fireEvent.click(screen.getByRole('button', { name: 'Add to this group' }));

    expect(await screen.findByText('These products have their own supply setting. Which should the whole group use?')).toBeInTheDocument();
    expect(screen.getByText('Gatorade Glacier Freeze: 12 on hand')).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Keep the account window' })).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Keep 24 ounces' })).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: 'Keep the household default' }));

    expect(await screen.findByText('Gatorade Glacier Freeze')).toBeInTheDocument();
    await waitFor(() => expect(screen.queryByRole('checkbox', { name: /Gatorade Glacier Freeze/ })).not.toBeInTheDocument());
    expect(membersPosts).toBe(2);
  });

  it('keeps a 6 month group target instead of clearing it', async () => {
    let current: ProductGroup = { ...baseGroup(), windowMonths: 6 };
    const bodies: { productIds: string[]; target?: { windowMonths?: number; clear?: boolean } }[] = [];
    renderDetail((url, init) => {
      const method = init?.method ?? 'GET';
      if (url === '/api/groups/g1' && method === 'GET') return json(current);
      if (url === '/api/groups' && method === 'GET') return json([current]);
      if (url === '/api/products' && method === 'GET') return json([product('glacier', 'Gatorade Glacier Freeze')]);
      if (url === '/api/inventory') return json([inventoryRow('glacier', 'Gatorade Glacier Freeze', 1)]);
      if (url.startsWith('/api/products/')) return json({ barcodes: [] });
      if (url === '/api/groups/g1/members' && method === 'POST') {
        const body = JSON.parse(String(init?.body)) as { productIds: string[]; target?: { windowMonths?: number } };
        bodies.push(body);
        if (!body.target) {
          return json({
            error: 'Choose what this group should keep on hand.',
            code: 'target_decision_required',
            members: [{ productId: 'glacier', name: 'Gatorade Glacier Freeze', quantity: 4, amount: 48, dimension: 'mass' }],
          }, 409);
        }
        current = {
          ...current,
          members: [...current.members, member('glacier', 'Gatorade Glacier Freeze', 1)],
        };
        return json(current);
      }
      throw new Error(`Unexpected ${method} ${url}`);
    });

    fireEvent.click(await screen.findByRole('button', { name: 'Add a product' }));
    fireEvent.click(await screen.findByRole('checkbox', { name: 'Gatorade Glacier Freeze · 1 on hand' }));
    fireEvent.click(screen.getByRole('button', { name: 'Add to this group' }));

    expect(await screen.findByRole('button', { name: "Keep the group's 6 months" })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Use 48 ounces (from Gatorade Glacier Freeze)' })).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: "Keep the group's 6 months" }));

    await waitFor(() => expect(bodies).toHaveLength(2));
    expect(bodies[1]?.target).toEqual({ windowMonths: 6 });
  });

  it('sends a member quantity and dimension, and cancel sends nothing', async () => {
    const current = baseGroup();
    const posts: { productIds: string[]; target?: { quantity?: number; dimension?: string } }[] = [];
    renderDetail((url, init) => {
      const method = init?.method ?? 'GET';
      if (url === '/api/groups/g1' && method === 'GET') return json(current);
      if (url === '/api/groups' && method === 'GET') return json([current]);
      if (url === '/api/products' && method === 'GET') return json([product('glacier', 'Gatorade Glacier Freeze')]);
      if (url === '/api/inventory') return json([inventoryRow('glacier', 'Gatorade Glacier Freeze', 1)]);
      if (url.startsWith('/api/products/')) return json({ barcodes: [] });
      if (url === '/api/groups/g1/members' && method === 'POST') {
        const body = JSON.parse(String(init?.body)) as { productIds: string[]; target?: { quantity?: number; dimension?: string } };
        posts.push(body);
        if (!body.target) {
          return json({
            error: 'Choose what this group should keep on hand.',
            code: 'target_decision_required',
            members: [{ productId: 'glacier', name: 'Gatorade Glacier Freeze', quantity: 4, amount: 48, dimension: 'mass' }],
          }, 409);
        }
        return json({
          ...current,
          quantity: body.target.quantity,
          dimension: body.target.dimension,
          members: [...current.members, member('glacier', 'Gatorade Glacier Freeze', 1)],
        });
      }
      throw new Error(`Unexpected ${method} ${url}`);
    });

    fireEvent.click(await screen.findByRole('button', { name: 'Add a product' }));
    const checkbox = await screen.findByRole('checkbox', { name: 'Gatorade Glacier Freeze · 1 on hand' });
    fireEvent.click(checkbox);
    fireEvent.click(screen.getByRole('button', { name: 'Add to this group' }));

    expect(await screen.findByText('Gatorade Glacier Freeze: 4 on hand (48 ounces)')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Use 48 ounces (from Gatorade Glacier Freeze)' })).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: 'Cancel' }));

    expect(screen.queryByText('These products have their own supply setting. Which should the whole group use?')).not.toBeInTheDocument();
    expect(screen.getByRole('checkbox', { name: 'Gatorade Glacier Freeze · 1 on hand' })).toBeChecked();
    expect(posts).toHaveLength(1);
    expect(posts[0]?.target).toBeUndefined();

    fireEvent.click(screen.getByRole('button', { name: 'Add to this group' }));
    fireEvent.click(await screen.findByRole('button', { name: 'Use 48 ounces (from Gatorade Glacier Freeze)' }));

    await waitFor(() => expect(posts).toHaveLength(3));
    expect(posts[0]?.target).toBeUndefined();
    expect(posts[1]?.target).toBeUndefined();
    expect(posts[2]?.target).toEqual({ quantity: 48, dimension: 'mass' });
  });

  it('still renames the group and moves a member', async () => {
    let current = baseGroup();
    const calls: { url: string; body?: string }[] = [];
    renderDetail((url, init) => {
      const method = init?.method ?? 'GET';
      if (method !== 'GET') calls.push({ url, body: typeof init?.body === 'string' ? init.body : undefined });
      if (url === '/api/groups/g1' && method === 'GET') return json(current);
      if (url === '/api/groups' && method === 'GET') {
        return json([current, {
          id: 'g2',
          name: 'Drink mixes',
          rule: 'same_as_ran_out',
          ruleConfirmed: false,
          members: [],
          runningLow: false,
        }]);
      }
      if (url === '/api/products' && method === 'GET') return json([]);
      if (url === '/api/inventory') return json([]);
      if (url.startsWith('/api/products/')) return json({ barcodes: ['052000340075'] });
      if (url === '/api/groups/g1' && method === 'PATCH') {
        current = { ...current, name: 'Sports powder' };
        return json(current);
      }
      if (url === '/api/groups/g2/members' && method === 'POST') {
        current = { ...current, members: current.members.filter((item) => item.productId !== 'punch') };
        return json(current);
      }
      throw new Error(`Unexpected ${method} ${url}`);
    });

    fireEvent.click(await screen.findByRole('button', { name: 'Rename' }));
    const renameDialog = await screen.findByRole('dialog', { name: 'Rename' });
    fireEvent.change(within(renameDialog).getByLabelText('Name'), { target: { value: 'Sports powder' } });
    fireEvent.click(within(renameDialog).getByRole('button', { name: 'Rename' }));
    expect(await screen.findByRole('heading', { name: 'Sports powder' })).toBeInTheDocument();

    fireEvent.click(screen.getByRole('button', { name: 'Remove or move Fruit punch thirst quencher powder' }));
    fireEvent.click(await screen.findByRole('menuitem', { name: 'Drink mixes' }));
    await waitFor(() => expect(calls.some((call) => call.url === '/api/groups/g2/members')).toBe(true));
    const move = calls.find((call) => call.url === '/api/groups/g2/members');
    expect(JSON.parse(move?.body ?? '{}')).toEqual({ productIds: ['punch'], fromGroupId: 'g1' });
    await waitFor(() => expect(screen.queryByText('Fruit punch thirst quencher powder')).not.toBeInTheDocument());
  });

  it('fills the bin from ounces on hand and leaves it empty for months', async () => {
    let current: ProductGroup = {
      ...baseGroup(),
      quantity: 48,
      dimension: 'mass',
      ruleConfirmed: true,
      members: [
        member('lemon', 'Gatorade Lemon-Lime', 1),
        member('glacier', 'Gatorade Glacier Freeze', 0),
      ],
    };
    renderDetail((url, init) => {
      const method = init?.method ?? 'GET';
      if (url === '/api/groups/g1' && method === 'GET') return json(current);
      if (url === '/api/groups' && method === 'GET') return json([current]);
      if (url === '/api/products' && method === 'GET') return json([]);
      if (url === '/api/inventory') return json([]);
      if (url === '/api/settings/supply') return json({ months: 3, opening: false, wipePhrase: 'WIPE INVENTORY' });
      if (url === '/api/products/lemon') {
        return json({ barcodes: ['052000338881'], unitOfMeasure: 'canister', netAmount: 18.3, netUnit: 'oz' });
      }
      if (url === '/api/products/glacier') {
        return json({ barcodes: [], unitOfMeasure: 'canister', netAmount: 50.9, netUnit: 'oz' });
      }
      if (url === '/api/groups/g1/target' && method === 'PUT') {
        const body = JSON.parse(String(init?.body)) as { windowMonths?: number; clear?: boolean };
        current = {
          ...current,
          quantity: body.windowMonths !== undefined || body.clear ? undefined : current.quantity,
          dimension: body.windowMonths !== undefined || body.clear ? undefined : current.dimension,
          windowMonths: body.windowMonths,
        };
        return json(current);
      }
      throw new Error(`Unexpected ${method} ${url}`);
    });

    const stock = await screen.findByRole('meter', { name: 'Stock on hand' });
    expect(within(stock).getByText('18.3 oz on hand')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Change rule, Same product' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: '48 oz' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Set time' })).toBeInTheDocument();
    expect(stock).toHaveAttribute('aria-valuenow', '18.3');
    expect(stock).toHaveAttribute('aria-valuemax', '48');
    expect(stock).toHaveAttribute('aria-valuetext', '18.3 oz on hand');
    expect(screen.queryByText(/≈/)).not.toBeInTheDocument();
    expect(document.querySelector('.bin-fill')).not.toBeNull();
    expect(screen.getByText('18.3 oz canister, 1 on hand')).toBeInTheDocument();
    expect(screen.getByText('Barcode: 052000338881')).toBeInTheDocument();
    expect(screen.getByText('50.9 oz canister, none on hand')).toBeInTheDocument();

    fireEvent.click(screen.getByRole('button', { name: 'Set time' }));
    expect(screen.getByRole('button', { name: 'Use the household default' })).toBeInTheDocument();
    fireEvent.change(screen.getByLabelText('Months'), { target: { value: '3' } });
    fireEvent.click(screen.getByRole('button', { name: 'Save' }));

    expect(await screen.findByRole('button', { name: '3 mo' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Set amount' })).toBeInTheDocument();
    expect(document.querySelector('.bin-fill')).toBeNull();
  });

  it('recomputes the other end from the consumption rate', async () => {
    let current: ProductGroup = {
      ...baseGroup(),
      quantity: 48,
      dimension: 'mass',
      usage: { perMonth: 16, unit: 'oz' },
      members: [member('lemon', 'Gatorade Lemon-Lime', 1)],
    };
    const saved: unknown[] = [];
    renderDetail((url, init) => {
      const method = init?.method ?? 'GET';
      if (url === '/api/groups/g1' && method === 'GET') return json(current);
      if (url === '/api/groups' && method === 'GET') return json([current]);
      if (url === '/api/products' && method === 'GET') return json([]);
      if (url === '/api/inventory') return json([]);
      if (url === '/api/settings/supply') return json({ months: 3, opening: false, wipePhrase: 'WIPE INVENTORY' });
      if (url === '/api/products/lemon') {
        return json({ barcodes: [], unitOfMeasure: 'canister', netAmount: 18.3, netUnit: 'oz' });
      }
      if (url === '/api/groups/g1/target' && method === 'PUT') {
        const body = JSON.parse(String(init?.body)) as { windowMonths?: number; quantity?: number; dimension?: string };
        saved.push(body);
        current = {
          ...current,
          quantity: body.quantity,
          dimension: body.dimension as ProductGroup['dimension'],
          windowMonths: body.windowMonths,
          usage: current.usage,
        };
        return json(current);
      }
      throw new Error(`Unexpected ${method} ${url}`);
    });

    const stock = await screen.findByRole('meter', { name: 'Stock on hand' });
    expect(within(stock).getByText('18.3 oz on hand · ≈ 5 wks')).toBeInTheDocument();
    expect(screen.getByText('at 16 oz / mo')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: '48 oz' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: '≈ 3 mo' })).toBeInTheDocument();
    expect(screen.getByText('Tap either end to set it; the other follows')).toBeInTheDocument();

    fireEvent.click(screen.getByRole('button', { name: '≈ 3 mo' }));
    fireEvent.change(screen.getByLabelText('Months'), { target: { value: '6' } });
    expect(screen.getByRole('button', { name: '≈ 96 oz' })).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: 'Save' }));

    await waitFor(() => expect(saved).toEqual([{ windowMonths: 6 }]));
    expect(screen.getByRole('button', { name: '6 mo' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: '≈ 96 oz' })).toBeInTheDocument();
    expect(document.querySelector('.bin-fill')).not.toBeNull();
  });

  it('splits don\'t-restock products out of the rotation and saves favor variety', async () => {
    let current: ProductGroup = {
      ...baseGroup(),
      rule: 'favor_variety',
      ruleConfirmed: true,
      quantity: 48,
      dimension: 'mass',
      members: [
        { ...member('plain', 'Kroger bran', 1), lastStockedAt: '2026-06-01T00:00:00Z' },
        { ...member('fancy', 'Lehi roller', 1), noRestock: true, lastStockedAt: '2024-01-01T00:00:00Z' },
        member('corn', 'Corn muffin', 0),
      ],
    };
    const calls: { url: string; body?: string }[] = [];
    renderDetail((url, init) => {
      const method = init?.method ?? 'GET';
      if (method !== 'GET') calls.push({ url, body: typeof init?.body === 'string' ? init.body : undefined });
      if (url === '/api/groups/g1' && method === 'GET') return json(current);
      if (url === '/api/groups' && method === 'GET') return json([current]);
      if (url === '/api/products' && method === 'GET') return json([]);
      if (url === '/api/inventory') return json([]);
      if (url === '/api/settings/supply') return json({ months: 3, opening: false, wipePhrase: 'WIPE INVENTORY' });
      if (url.startsWith('/api/products/')) {
        return json({ barcodes: [], unitOfMeasure: 'box', netAmount: 18, netUnit: 'oz' });
      }
      if (url === '/api/groups/g1/members/plain/restock' && method === 'PUT') {
        current = {
          ...current,
          members: current.members.map((item) => (item.productId === 'plain' ? { ...item, noRestock: true } : item)),
        };
        return json(current);
      }
      if (url === '/api/groups/g1/rule' && method === 'PUT') return json(current);
      throw new Error(`Unexpected ${method} ${url}`);
    });

    expect(await screen.findByRole('heading', { name: 'In rotation' })).toBeInTheDocument();
    expect(screen.getByRole('heading', { name: 'Not restocked' })).toBeInTheDocument();
    expect(screen.getByText('Next up: Corn muffin · rotates through 2')).toBeInTheDocument();
    expect(screen.getByText('⊘ no restock')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Change rule, Favor variety' })).toBeInTheDocument();
    const stock = screen.getByRole('meter', { name: 'Stock on hand' });
    expect(stock).toHaveAttribute('aria-valuenow', '36');

    fireEvent.click(screen.getByRole('button', { name: 'Remove or move Kroger bran' }));
    expect(await screen.findByRole('menuitem', { name: /Don't restock/ })).toBeInTheDocument();
    expect(screen.getByText('Stays in the group and counts toward stock')).toBeInTheDocument();
    fireEvent.click(screen.getByRole('menuitem', { name: /Don't restock/ }));
    await waitFor(() => expect(calls.some((call) => call.url.endsWith('/restock'))).toBe(true));
    expect(JSON.parse(calls.find((call) => call.url.endsWith('/restock'))?.body ?? '{}')).toEqual({ noRestock: true });

    fireEvent.click(screen.getByRole('button', { name: 'Change rule, Favor variety' }));
    const picker = await screen.findByRole('dialog', { name: 'What to buy next' });
    expect(within(picker).getByRole('radio', { name: 'Favor variety' })).toBeChecked();
    expect(within(picker).getByText("Rotate to the product you've had least recently.")).toBeInTheDocument();
    expect(within(picker).getByText('Products marked "Don\'t restock" are skipped.')).toBeInTheDocument();
  });
});
