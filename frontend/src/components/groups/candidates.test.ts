import { describe, expect, it } from 'vitest';
import type { InventoryItem } from '@stockbot/shared/types';
import { ungroupedProducts } from './candidates';

const row = (productId: string, name: string, instanceCount: number, grouped = false): InventoryItem => ({
  item: {
    id: `item-${productId}`,
    userId: 'user-1',
    productId,
    product: { id: productId, name, category: 'Drinks', unitOfMeasure: 'canister' },
    targetQuantity: null,
    createdAt: '2026-01-01T00:00:00Z',
  },
  instanceCount,
  nearExpiryCount: 0,
  expiredCount: 0,
  needsAttention: false,
  ...(grouped
    ? {
      group: {
        id: 'g',
        name: 'Grouped',
        rule: 'same_as_ran_out',
        ruleConfirmed: false,
        onHand: instanceCount,
        memberCount: 1,
        members: [],
      },
    }
    : {}),
});

describe('ungroupedProducts', () => {
  it('keeps catalog and inventory products that are not in a group', () => {
    const found = ungroupedProducts(
      [
        { id: 'punch', name: 'Fruit punch' },
        { id: 'glacier', name: 'Gatorade Glacier Freeze' },
        { id: 'lemon', name: 'Lemonade powder' },
        { id: 'blank', name: '   ' },
      ],
      [row('punch', 'Fruit punch', 2, true), row('glacier', 'Gatorade Glacier Freeze', 1), row('loose', 'Loose powder', 3)],
      [{ members: [{ productId: 'punch', name: 'Fruit punch', onHand: 2 }] }],
    );

    expect(found).toEqual([
      { productId: 'glacier', name: 'Gatorade Glacier Freeze', onHand: 1 },
      { productId: 'lemon', name: 'Lemonade powder' },
      { productId: 'loose', name: 'Loose powder', onHand: 3 },
    ]);
  });
});