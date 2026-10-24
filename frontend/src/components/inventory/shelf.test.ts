import { describe, expect, it } from 'vitest';
import type { GroupSuggestion, InventoryItem, Product, ProductGroup } from '@stockbot/shared/types';
import { buildShelf, filterShelf, groupsForShelf, suggestedDestinations } from './shelf';

const product = (id: string, name: string, extra: Partial<Product> = {}): Product => ({
  id,
  name,
  category: 'Drinks',
  unitOfMeasure: 'canister',
  createdAt: '2026-01-01T00:00:00Z',
  ...extra,
});

const item = (id: string, name: string, extra: Partial<InventoryItem> = {}): InventoryItem => ({
  item: {
    id,
    userId: 'user-1',
    productId: id,
    product: product(id, name),
    targetQuantity: null,
    createdAt: '2026-01-01T00:00:00Z',
  },
  instanceCount: 1,
  nearExpiryCount: 0,
  expiredCount: 0,
  needsAttention: false,
  ...extra,
});

const powder = (): ProductGroup => ({
  id: 'powder',
  name: 'Gatorade powder',
  rule: 'favorite',
  ruleConfirmed: true,
  pinnedProductId: 'lemon',
  quantity: 48,
  dimension: 'mass',
  usage: { perMonth: 16, unit: 'oz' },
  runningLow: true,
  members: [
    { productId: 'lemon', name: 'Lemon-Lime', onHand: 1 },
    { productId: 'glacier', name: 'Glacier Freeze', onHand: 0 },
  ],
});

describe('buildShelf', () => {
  const catalog = [
    product('lemon', 'Lemon-Lime', { imageUrl: 'https://images.openfoodfacts.org/lemon.jpg', netAmount: 18, netUnit: 'oz' }),
    product('glacier', 'Glacier Freeze', { imageUrl: 'https://images.openfoodfacts.org/glacier.jpg', netAmount: 50, netUnit: 'oz' }),
  ];
  const suggestions: GroupSuggestion[] = [{
    id: 'sug-oat',
    kind: 'from_old_plan',
    title: 'Plant milks',
    status: 'open',
    existingGroupId: 'milks',
    members: [
      { productId: 'oat', name: 'Oat milk', included: true, caution: '' },
      { productId: 'almond', name: 'Almond milk', included: true, caution: '' },
    ],
  }];

  const rows = () => buildShelf({
    items: [item('lemon', 'Lemon-Lime'), item('oat', 'Oat milk', { instanceCount: 0 })],
    groups: [powder(), {
      id: 'milks',
      name: 'Milks',
      rule: 'same_as_ran_out',
      ruleConfirmed: true,
      runningLow: false,
      members: [{ productId: 'almond', name: 'Almond milk', onHand: 1 }],
    }],
    suggestions,
    catalog,
    householdMonths: 3,
  });

  it('uses the favorite photo, the stock line, and the pinned target', () => {
    const group = rows().find((row) => row.kind === 'group' && row.id === 'powder');
    expect(group?.kind).toBe('group');
    if (group?.kind !== 'group') return;
    expect(group.photoSrc).toBe('https://images.openfoodfacts.org/lemon.jpg');
    expect(group.photoName).toBe('Lemon-Lime');
    expect(group.status).toBe('18 oz on hand · ≈ 5 wks');
    expect(group.rulePill).toBe('Lemon-Lime');
    expect(group.pinLabel).toBe('48 oz');
    expect(group.meter).toMatchObject({ now: 18, max: 48 });
  });

  it('falls back to any member photo when the favorite has none', () => {
    const bare = powder();
    const group = buildShelf({
      items: [],
      groups: [bare],
      suggestions: [],
      catalog: [product('glacier', 'Glacier Freeze', { imageUrl: 'https://images.openfoodfacts.org/glacier.jpg' })],
      householdMonths: 3,
    })[0];
    expect(group?.kind).toBe('group');
    if (group?.kind !== 'group') return;
    expect(group.photoSrc).toBe('https://images.openfoodfacts.org/glacier.jpg');
  });

  it('keeps a grouped product out of the ungrouped rows and links a suggestion', () => {
    const built = rows();
    expect(built.some((row) => row.kind === 'item' && row.item.item.productId === 'lemon')).toBe(false);
    const oat = built.find((row) => row.kind === 'item');
    expect(oat?.kind).toBe('item');
    if (oat?.kind !== 'item') return;
    expect(oat.suggestion?.title).toBe('Plant milks');
  });

  it('filters running low, groups, ungrouped, and search', () => {
    const built = rows();
    expect(filterShelf(built, '', 'low').map((row) => row.id)).toEqual(['powder', 'oat']);
    expect(filterShelf(built, '', 'groups').every((row) => row.kind === 'group')).toBe(true);
    expect(filterShelf(built, '', 'ungrouped').map((row) => row.id)).toEqual(['oat']);
    expect(filterShelf(built, 'glacier', 'all').map((row) => row.id)).toEqual(['powder']);
    expect(filterShelf(built, 'bakery', 'all')).toEqual([]);
  });

  it('hides the rule pill for the default rule and shows pick a rule when unset', () => {
    const confirmed = buildShelf({
      items: [],
      groups: [{ ...powder(), rule: 'same_as_ran_out', ruleConfirmed: true, pinnedProductId: undefined, quantity: undefined, usage: undefined }],
      suggestions: [],
      catalog: [],
    })[0];
    const open = buildShelf({
      items: [],
      groups: [{ ...powder(), ruleConfirmed: false }],
      suggestions: [],
      catalog: [],
    })[0];
    expect(confirmed?.kind === 'group' && confirmed.rulePill).toBe(null);
    expect(open?.kind === 'group' && open.rulePill).toBe('Pick a rule');
  });
});

describe('suggestedDestinations', () => {
  it('lists the same-need group before a plain search would', () => {
    const groups: ProductGroup[] = [
      { id: 'soup', name: 'Soup', rule: 'same_as_ran_out', ruleConfirmed: true, runningLow: false, members: [] },
      {
        id: 'milks',
        name: 'Milks',
        rule: 'same_as_ran_out',
        ruleConfirmed: true,
        runningLow: false,
        members: [{ productId: 'almond', name: 'Almond milk', onHand: 1 }],
      },
    ];
    const cards: GroupSuggestion[] = [{
      id: 'sug',
      kind: 'from_old_plan',
      title: 'Plant milks',
      existingGroupId: 'milks',
      status: 'open',
      members: [{ productId: 'oat', name: 'Oat milk', included: true, caution: '' }],
    }];
    expect(suggestedDestinations('oat', cards, groups)).toEqual([
      { id: 'milks', name: 'Milks', reason: 'Suggested · same need' },
    ]);
  });
});

describe('groupsForShelf', () => {
  it('keeps an inventory summary when the group list is missing', () => {
    const inventory = item('gv', 'Green beans');
    inventory.group = {
      id: 'beans',
      name: 'Cut green beans',
      rule: 'same_as_ran_out',
      ruleConfirmed: false,
      onHand: 3,
      memberCount: 1,
      members: [{ productId: 'gv', name: 'Green beans', onHand: 3 }],
    };
    const groups = groupsForShelf([inventory], [inventory]);
    expect(groups.map((group) => group.name)).toEqual(['Cut green beans']);
  });
});
