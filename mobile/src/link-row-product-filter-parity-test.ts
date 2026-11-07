import type { InventoryItem } from '@stockbot/shared/types';
import { inventoryItemMatchesScanLinkSearch } from '@stockbot/shared/linkRowProductFilter';

/** Pre–Step 3 mobile householdRows filter (QueueScreen). */
function baselineMobileLinkRowMatch(
  productId: string,
  product: { name: string; category: string },
  query: string,
  excludeProductId: string,
): boolean {
  if (excludeProductId !== '' && productId === excludeProductId) return false;
  const normalized = query.trim().toLocaleLowerCase();
  if (normalized === '') return true;
  return `${product.name} ${product.category}`.toLocaleLowerCase().includes(normalized);
}

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
    instanceCount: 0,
    nearExpiryCount: 0,
    expiredCount: 0,
    needsAttention: false,
  };
}

const HOUSEHOLD = [
  row('prod-rice', 'Rice', 'Pantry'),
  row('prod-oats', 'Oats', 'Grain'),
];

describe('mobile scan link row filter parity', () => {
  it.each([
    { query: '', exclude: '', visible: ['Rice', 'Oats'] },
    { query: 'grain', exclude: '', visible: ['Oats'] },
    { query: 'oats grain', exclude: '', visible: ['Oats'] },
    { query: 'quinoa', exclude: '', visible: [] as string[] },
    { query: 'rice', exclude: 'prod-rice', visible: [] as string[] },
    { query: '  grain  ', exclude: '', visible: ['Oats'] },
  ])('query=$query exclude=$exclude', ({ query, exclude, visible }) => {
    const baselineNames = HOUSEHOLD.filter((r) =>
      baselineMobileLinkRowMatch(r.item.productId, r.item.product, query, exclude),
    ).map((r) => r.item.product.name);

    const wiredNames = HOUSEHOLD.filter((r) =>
      inventoryItemMatchesScanLinkSearch(r.item.productId, r.item.product, query, exclude),
    ).map((r) => r.item.product.name);

    expect(wiredNames).toEqual(baselineNames);
    expect(wiredNames).toEqual(visible);
  });
});
