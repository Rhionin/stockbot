import type { RestockTarget } from './restockTarget';
import { shoppingListHasRestockRow } from './shoppingRestockGate';
import {
  ZERO_FOR_TRIP_DONT_RESTOCK_BUTTON,
  ZERO_FOR_TRIP_NUDGE_PRODUCT,
  ZERO_FOR_TRIP_NUDGE_GROUP,
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
  return shoppingListHasRestockRow(line, restock);
}

export function restockTargetAlreadyZero(target: RestockTarget | undefined): boolean {
  return target?.value === 0;
}

export function shouldShowZeroForTripNudge(
  line: RestockNudgeTarget,
  restock: RestockSnapshotLike | null | undefined,
): boolean {
  if (!canChangeRestockTarget(line, restock)) return false;
  const target = line.target
    ?? (line.groupId
      ? restock?.categories.find((row) => row.id === line.groupId)?.target
      : restock?.items.find((row) => row.itemId === line.itemId)?.target);
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
  return zeroForTripDontRestockAriaLabel(line.productName);
}