import { inventoryItemMatchesScanLinkSearch } from '@stockbot/shared/linkRowProductFilter';
import type { InventoryItem } from '@stockbot/shared/types';

export function filterInventoryForScanLinkSearch(
  items: InventoryItem[],
  query: string,
  excludeProductId: string,
): InventoryItem[] {
  return items.filter((row) =>
    inventoryItemMatchesScanLinkSearch(
      row.item.productId,
      row.item.product,
      query,
      excludeProductId,
    ));
}
