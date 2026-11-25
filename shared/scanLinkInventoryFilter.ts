import { inventoryItemMatchesScanLinkSearch } from './linkRowProductFilter';
import type { InventoryItem } from './types';

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
