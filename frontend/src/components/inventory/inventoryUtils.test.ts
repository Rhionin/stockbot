import { describe, expect, it } from 'vitest';
import fc from 'fast-check';
import type { InventoryItem } from '@stockbot/shared/types';
import { mergeInventoryEvent, visibleCategory } from './inventoryUtils';

const inventoryItem = (id: string, overrides: Partial<InventoryItem> = {}): InventoryItem => ({
  item: {
    id,
    userId: 'user-1',
    productId: `product-${id}`,
    product: { id: `product-${id}`, name: 'Item', category: 'Category', unitOfMeasure: 'unit' },
    targetQuantity: null,
    createdAt: '2026-01-01T00:00:00Z',
  },
  instanceCount: 1,
  nearExpiryCount: 0,
  expiredCount: 0,
  needsAttention: false,
  ...overrides,
});

describe('visibleCategory', () => {
  it('keeps a real category and drops missing values', () => {
    expect(visibleCategory('Pastas, Past')).toBe('Pastas, Past');
    expect(visibleCategory('  Milks  ')).toBe('Milks');
    expect(visibleCategory('')).toBeNull();
    expect(visibleCategory('   ')).toBeNull();
    expect(visibleCategory('undefined')).toBeNull();
    expect(visibleCategory('UNDEFINED')).toBeNull();
    expect(visibleCategory('null')).toBeNull();
    expect(visibleCategory(' Null ')).toBeNull();
    expect(visibleCategory(undefined)).toBeNull();
    expect(visibleCategory(null)).toBeNull();
  });
});

describe('mergeInventoryEvent', () => {
  it('appends an event for a new item id', () => {
    const items = [inventoryItem('existing')];
    const event = inventoryItem('new');

    expect(mergeInventoryEvent(items, event)).toEqual([items[0], event]);
  });

  it('replaces an existing item id in place, keeping other items in order', () => {
    const items = [
      inventoryItem('a'),
      inventoryItem('b'),
      inventoryItem('c'),
    ];
    const event = inventoryItem('b', { instanceCount: 5 });

    expect(mergeInventoryEvent(items, event)).toEqual([items[0], event, items[2]]);
  });

  // Feature: realtime-scan-updates, Property 11: Merging an Inventory_Event
  // upserts the matching item
  //
  // **Validates: Requirements 5.2**
  //
  // For any list of displayed inventory items and any incoming Inventory_Event,
  // mergeInventoryEvent SHALL produce a list containing an item with the event's
  // item id, equal to the event, exactly once, and every other item already in
  // the list SHALL remain unchanged and in the same relative order.
  it('Property 11: upserts the item matching the event, keeping others unchanged and in order', () => {
    const itemArbitrary = fc.uuid().map((id) => inventoryItem(id));

    fc.assert(fc.property(
      fc.uniqueArray(itemArbitrary, { maxLength: 20, selector: (item) => item.item.id }),
      itemArbitrary,
      (items, event) => {
        const result = mergeInventoryEvent(items, event);
        const others = items.filter((item) => item.item.id !== event.item.id);
        const matchesForEventId = result.filter((item) => item.item.id === event.item.id);

        expect(matchesForEventId).toEqual([event]);
        expect(result.filter((item) => item.item.id !== event.item.id)).toEqual(others);
      },
    ), { numRuns: 100 });
  });
});
