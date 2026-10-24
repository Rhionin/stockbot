import type { InventoryItem, Product, ProductGroup } from '@stockbot/shared/types';

export interface AddCandidate {
  productId: string;
  name: string;
  onHand?: number;
}

// Products that are not already a member of any group. Inventory rows supply
// the on-hand count; catalog products with no stock are still addable.
export function ungroupedProducts(
  products: Pick<Product, 'id' | 'name'>[],
  inventory: InventoryItem[],
  groups: Pick<ProductGroup, 'members'>[],
): AddCandidate[] {
  const grouped = new Set<string>();
  for (const group of groups) {
    for (const member of group.members) grouped.add(member.productId);
  }

  const onHand = new Map<string, number>();
  const inventoryName = new Map<string, string>();
  for (const row of inventory) {
    const productId = row.item.productId;
    if (row.group) grouped.add(productId);
    onHand.set(productId, row.instanceCount);
    inventoryName.set(productId, row.item.product.name);
  }

  const byId = new Map<string, AddCandidate>();
  const add = (productId: string, name: string) => {
    const trimmed = name.trim();
    if (trimmed === '' || grouped.has(productId) || byId.has(productId)) return;
    byId.set(productId, {
      productId,
      name: trimmed,
      ...(onHand.has(productId) ? { onHand: onHand.get(productId) } : {}),
    });
  };

  for (const product of products) add(product.id, product.name);
  for (const [productId, name] of inventoryName) add(productId, name);

  return [...byId.values()].sort((left, right) => left.name.localeCompare(right.name));
}
