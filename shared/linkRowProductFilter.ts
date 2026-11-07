// Scan link search: empty query matches every row; otherwise name + category as one
// case-insensitive substring. Optional excluded product id drops that row first.

export type LinkSearchProduct = {
  name: string;
  category: string;
};

export function inventoryItemMatchesScanLinkSearch(
  productId: string,
  product: LinkSearchProduct,
  query: string,
  excludeProductId: string,
): boolean {
  if (excludeProductId !== '' && productId === excludeProductId) return false;
  const normalized = query.trim().toLocaleLowerCase();
  if (normalized === '') return true;
  return `${product.name} ${product.category}`.toLocaleLowerCase().includes(normalized);
}
