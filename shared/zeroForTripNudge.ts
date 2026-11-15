import type { RestockTarget } from './restockTarget';
import {
  shoppingGroupHasRestockTarget,
  shoppingGroupRestockTargetIsZero,
  shoppingListHasRestockRow,
  type ShoppingGroupTargetLike,
} from './shoppingRestockGate';
import {
  ZERO_FOR_TRIP_DONT_RESTOCK_BUTTON,
  ZERO_FOR_TRIP_NUDGE_PRODUCT,
  ZERO_FOR_TRIP_NUDGE_GROUP,
  zeroForTripDisplayName,
  zeroForTripDontRestockAriaLabel,
  zeroForTripDontRestockGroupAriaLabel,
} from './zeroForTripCopy';

export type RestockNudgeTarget = {
  itemId?: string;
  groupId?: string;
  productName: string;
  groupName?: string;
  target?: RestockTarget;
};

export type RestockSnapshotLike = {
  items: Array<{ itemId: string; target: RestockTarget }>;
  categories: Array<{ id: string; target?: RestockTarget }>;
};

/** Same gate as the shopping list ⋯ menu (`shoppingListHasRestockRow`). */
export function canChangeRestockTarget(
  line: RestockNudgeTarget,
  restock: RestockSnapshotLike | null | undefined,
): boolean {
  return shoppingListHasRestockRow({ itemId: line.itemId }, restock);
}

export function canShowGroupZeroForTripNudge(
  line: RestockNudgeTarget,
  groups: ShoppingGroupTargetLike[] | null | undefined,
): boolean {
  const groupName = line.groupName?.trim();
  if (line.groupId == null || line.groupId === '' || groupName == null || groupName === '') return false;
  if (!shoppingGroupHasRestockTarget(line.groupId, groups)) return false;
  return !shoppingGroupRestockTargetIsZero(line.groupId, groups);
}

export function restockTargetAlreadyZero(target: RestockTarget | undefined): boolean {
  return target?.value === 0;
}

export function shouldShowZeroForTripNudge(
  line: RestockNudgeTarget,
  restock: RestockSnapshotLike | null | undefined,
  groups?: ShoppingGroupTargetLike[] | null | undefined,
): boolean {
  const groupName = line.groupName?.trim();
  if (line.groupId != null && line.groupId !== '' && groupName) {
    return canShowGroupZeroForTripNudge(line, groups);
  }
  if (!canChangeRestockTarget(line, restock)) return false;
  const target = line.target
    ?? restock?.items.find((row) => row.itemId === line.itemId)?.target;
  return !restockTargetAlreadyZero(target);
}

export function zeroForTripNudgePrompt(line: RestockNudgeTarget): string {
  const group = line.groupName?.trim();
  if (group) return ZERO_FOR_TRIP_NUDGE_GROUP(group);
  return ZERO_FOR_TRIP_NUDGE_PRODUCT;
}

export function zeroForTripNudgeButtonLabel(): string {
  return ZERO_FOR_TRIP_DONT_RESTOCK_BUTTON;
}

export function zeroForTripNudgeButtonAria(line: RestockNudgeTarget): string {
  const group = line.groupName?.trim();
  if (group) return zeroForTripDontRestockGroupAriaLabel(group);
  return zeroForTripDontRestockAriaLabel(zeroForTripDisplayName(line.productName));
}