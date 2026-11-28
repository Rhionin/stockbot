import type { GroupGoalTarget } from './types';
import type { RestockSnapshotLike } from './zeroForTripNudge';

export type ShoppingRestockLineRef = {
  itemId?: string;
  groupId?: string;
};

export type ShoppingGroupTargetLike = {
  id: string;
  target?: GroupGoalTarget | null;
  /** @deprecated inventory list bridge; prefer `target`. */
  windowMonths?: number;
  /** @deprecated inventory list bridge; prefer `target`. */
  quantity?: number;
};

/**
 * Same visibility gate as the shopping list row actions menu (⋯ → Don't restock):
 * the signed-in member sees those actions when the item has a row in the restock
 * snapshot (`restock.items`), not from an owner-only role check.
 */
export function shoppingListHasRestockRow(
  line: Pick<ShoppingRestockLineRef, 'itemId'>,
  restock: RestockSnapshotLike | null | undefined,
): boolean {
  if (restock == null) return false;
  const itemId = line.itemId?.trim();
  if (itemId == null || itemId === '') return false;
  return restock.items.some((row) => row.itemId === itemId);
}

export function shoppingGroupHasRestockTarget(
  groupId: string | undefined,
  groups: ShoppingGroupTargetLike[] | null | undefined,
): boolean {
  const id = groupId?.trim();
  if (id == null || id === '' || groups == null) return false;
  const group = groups.find((row) => row.id === id);
  if (group == null) return false;
  if (group.target != null) return group.target.kind !== 'dont_restock';
  return group.windowMonths !== undefined || group.quantity !== undefined;
}

export function shoppingGroupRestockTargetIsZero(
  groupId: string | undefined,
  groups: ShoppingGroupTargetLike[] | null | undefined,
): boolean {
  const id = groupId?.trim();
  if (id == null || id === '' || groups == null) return true;
  const group = groups.find((row) => row.id === id);
  if (group == null) return true;
  if (group.target != null) {
    return group.target.kind === 'dont_restock' || group.target.value === 0;
  }
  if (group.windowMonths === 0) return true;
  if (group.quantity === 0) return true;
  return false;
}