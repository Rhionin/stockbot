import type { RestockSnapshotLike } from './zeroForTripNudge';

export type ShoppingRestockLineRef = {
  itemId?: string;
  groupId?: string;
};

/**
 * Same visibility gate as the shopping list row actions menu (⋯ → Don't restock):
 * the signed-in member sees those actions when the item has a row in the restock
 * snapshot (`restock.items`), not from an owner-only role check.
 */
export function shoppingListHasRestockRow(
  line: ShoppingRestockLineRef,
  restock: RestockSnapshotLike | null | undefined,
): boolean {
  if (restock == null) return false;
  if (line.groupId) {
    return restock.categories.some((row) => row.id === line.groupId);
  }
  if (line.itemId) {
    return restock.items.some((row) => row.itemId === line.itemId);
  }
  return false;
}