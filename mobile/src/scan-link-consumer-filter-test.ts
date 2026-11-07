import type { InventoryItem } from '@stockbot/shared/types';
import { filterInventoryForScanLinkSearch } from './screens/scanLinkInventoryFilter';

function row(productId: string, name: string, category: string): InventoryItem {
  return {
    item: {
      id: `item-${productId}`,
      userId: 'user-1',
      productId,
      product: { id: productId, name, category, unitOfMeasure: 'unit' },
      targetQuantity: null,
      createdAt: '2026-01-01T00:00:00Z',
    },
    instanceCount: 2,
    nearExpiryCount: 0,
    expiredCount: 0,
    needsAttention: false,
  };
}

const HOUSEHOLD = [
  row('prod-rice', 'Rice', 'Pantry'),
  row('prod-oats', 'Oats', 'Grain'),
];

describe('QueueScreen scan link consumer filter', () => {
  it('matches pre-refactor householdRows filter membership and order', () => {
    const wired = (query: string, exclude: string) =>
      filterInventoryForScanLinkSearch(HOUSEHOLD, query, exclude).map((r) => r.item.product.name);

    expect(wired('', '')).toEqual(['Rice', 'Oats']);
    expect(wired('grain', '')).toEqual(['Oats']);
    expect(wired('oats grain', '')).toEqual(['Oats']);
    expect(wired('  grain  ', '')).toEqual(['Oats']);
    expect(wired('quinoa', '')).toEqual([]);
    expect(wired('rice', 'prod-rice')).toEqual([]);
  });
});
