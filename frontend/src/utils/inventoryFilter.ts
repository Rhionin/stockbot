import type { InventoryItem } from '@stockbot/shared/types';

export const filterInventoryItems = (
  inventoryItems: InventoryItem[],
  query: string,
): InventoryItem[] => {
  const normalizedQuery = query.trim().toLocaleLowerCase();
  if (normalizedQuery === '') return inventoryItems;

  return inventoryItems.filter(({ item }) => {
    const productName = item.product.name.toLocaleLowerCase();
    const category = item.product.category.toLocaleLowerCase();
    return productName.includes(normalizedQuery) || category.includes(normalizedQuery);
  });
};
