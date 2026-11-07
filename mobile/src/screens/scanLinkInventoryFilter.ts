import { inventoryItemMatchesScanLinkSearch } from '@stockbot/shared/linkRowProductFilter';
import type { InventoryItem } from '@stockbot/shared/types';

export function filterInventoryForScanLinkSearch(
  rows: InventoryItem[],
  query: string,
  excludeProductId: string,
): InventoryItem[] {
  return rows.filter((row) => inventoryItemMatchesScanLinkSearch(
    row.item.productId,
    row.item.product,
    query,
    excludeProductId,
  ));
}
