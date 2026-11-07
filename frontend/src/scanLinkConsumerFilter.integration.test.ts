import { describe, expect, it } from 'vitest';
import type { InventoryItem } from '@stockbot/shared/types';
import { filterInventoryForScanLinkSearch } from './components/queue/scanLinkInventoryFilter';

function item(productId: string, name: string, category: string): InventoryItem {
  return {
    item: {
      id: `item-${productId}`,
      userId: 'user-1',
      productId,
      product: { id: productId, name, category, unitOfMeasure: 'unit' },
      targetQuantity: null,
      createdAt: '2026-01-01T00:00:00Z',
    },
    instanceCount: 1,
    nearExpiryCount: 0,
    expiredCount: 0,
    needsAttention: false,
  };
}

const HOUSEHOLD = [
  item('prod-empty', 'Empty Oats', 'Grain'),
  item('prod-rice', 'Plain Rice', 'Pantry'),
];

/** Pre–Step 3 LinkProductSearch ordering and membership. */
function baselineFilter(items: InventoryItem[], query: string, exclude: string): string[] {
  return items
    .filter((row) => {
      if (exclude !== '' && row.item.productId === exclude) return false;
      const normalized = query.trim().toLocaleLowerCase();
      if (normalized === '') return true;
      const product = row.item.product;
      return `${product.name} ${product.category}`.toLocaleLowerCase().includes(normalized);
    })
    .map((row) => row.item.product.name);
}

describe('ScanEntryCard scan link consumer filter', () => {
  it('lists the same household rows and order as pre-refactor LinkProductSearch', () => {
    expect(
      filterInventoryForScanLinkSearch(HOUSEHOLD, '', '').map((row) => row.item.product.name),
    ).toEqual(baselineFilter(HOUSEHOLD, '', ''));

    expect(
      filterInventoryForScanLinkSearch(HOUSEHOLD, 'oats', '').map((row) => row.item.product.name),
    ).toEqual(['Empty Oats']);

    expect(
      filterInventoryForScanLinkSearch(HOUSEHOLD, 'grain', '').map((row) => row.item.product.name),
    ).toEqual(['Empty Oats']);

    expect(
      filterInventoryForScanLinkSearch(HOUSEHOLD, '  oats  ', '').map((row) => row.item.product.name),
    ).toEqual(['Empty Oats']);

    expect(
      filterInventoryForScanLinkSearch(HOUSEHOLD, 'zzzz', '').map((row) => row.item.product.name),
    ).toEqual([]);
  });

  it('honors excludeProductId on the wired export', () => {
    const names = filterInventoryForScanLinkSearch(HOUSEHOLD, 'rice', 'prod-rice').map(
      (row) => row.item.product.name,
    );
    expect(names).toEqual([]);
  });
});
