import { describe, expect, it } from 'vitest';
import fc from 'fast-check';
import type { InventoryItem } from '@stockbot/shared/types';
import { filterInventoryItems } from './inventoryFilter';

const inventoryItem = (id: string, name: string, category: string): InventoryItem => ({
  item: {
    id,
    userId: 'user-1',
    productId: `product-${id}`,
    product: { id: `product-${id}`, name, category, unitOfMeasure: 'unit' },
    targetQuantity: null,
    createdAt: '2026-01-01T00:00:00Z',
  },
  instanceCount: 1,
  nearExpiryCount: 0,
  expiredCount: 0,
  needsAttention: false,
});

describe('filterInventoryItems', () => {
  it('matches product names and categories case-insensitively', () => {
    const items = [
      inventoryItem('milk', 'Whole Milk', 'Dairy'),
      inventoryItem('bread', 'Sourdough', 'Bakery'),
    ];

    expect(filterInventoryItems(items, ' whole ')).toEqual([items[0]]);
    expect(filterInventoryItems(items, 'BAKERY')).toEqual([items[1]]);
    expect(filterInventoryItems(items, '')).toEqual(items);
  });

  // **Validates: Requirements 2.4**
  it('Property 11: returns exactly the name or category matches', () => {
    const itemArbitrary = fc.record({
      id: fc.uuid(),
      name: fc.string({ maxLength: 30 }),
      category: fc.string({ maxLength: 30 }),
    });

    fc.assert(fc.property(
      fc.array(itemArbitrary, { maxLength: 30 }),
      fc.string({ maxLength: 20 }),
      (generatedItems, query) => {
        const items = generatedItems.map(({ id, name, category }) =>
          inventoryItem(id, name, category));
        const normalizedQuery = query.trim().toLocaleLowerCase();
        const expected = normalizedQuery === ''
          ? items
          : items.filter(({ item }) =>
            item.product.name.toLocaleLowerCase().includes(normalizedQuery)
            || item.product.category.toLocaleLowerCase().includes(normalizedQuery));

        expect(filterInventoryItems(items, query)).toEqual(expected);
      },
    ));
  });
});
