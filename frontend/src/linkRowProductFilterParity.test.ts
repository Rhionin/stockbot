import { describe, expect, it } from 'vitest';
import type { InventoryItem } from '@stockbot/shared/types';
import { inventoryItemMatchesScanLinkSearch } from '@stockbot/shared/linkRowProductFilter';

/** Pre–Step 3 web LinkProductSearch filter (ScanEntryCard). */
function baselineWebLinkRowMatch(
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

function item(id: string, productId: string, name: string, category: string): InventoryItem {
  return {
    item: {
      id,
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

const FIXTURES: InventoryItem[] = [
  item('i1', 'prod-oats', 'Oats', 'Grain'),
  item('i2', 'prod-rice', 'Rice', 'Pantry'),
  item('i3', 'prod-milk', 'Oat milk', 'Dairy'),
];

describe('web scan link row filter parity', () => {
  it.each([
    { query: '', exclude: '', expectIds: ['prod-oats', 'prod-rice', 'prod-milk'] },
    { query: 'grain', exclude: '', expectIds: ['prod-oats'] },
    { query: 'oats grain', exclude: '', expectIds: ['prod-oats'] },
    { query: 'oat', exclude: '', expectIds: ['prod-oats', 'prod-milk'] },
    { query: 'quinoa', exclude: '', expectIds: [] as string[] },
    { query: 'rice', exclude: 'prod-rice', expectIds: [] as string[] },
    { query: '', exclude: 'prod-milk', expectIds: ['prod-oats', 'prod-rice'] },
    { query: '  grain  ', exclude: '', expectIds: ['prod-oats'] },
  ])('query=$query exclude=$exclude', ({ query, exclude, expectIds }) => {
    const baseline = FIXTURES.filter((row) =>
      baselineWebLinkRowMatch(row.item.productId, row.item.product, query, exclude),
    ).map((row) => row.item.productId);

    const wired = FIXTURES.filter((row) =>
      inventoryItemMatchesScanLinkSearch(row.item.productId, row.item.product, query, exclude),
    ).map((row) => row.item.productId);

    expect(wired).toEqual(baseline);
    expect(wired).toEqual(expectIds);
  });
});
