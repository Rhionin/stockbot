import type {
  GroupDefaultKind,
  GroupGoalBlocker,
  GroupGoalTarget,
  ProductGroup,
} from '@stockbot/shared/types';
import type { RestockGroupView } from '../../restock/types';
import { resolveGroupGoal } from './groupGoalModel';
import {
  c5Many,
  c5One,
  c6Many,
  c6One,
  d2a,
  d2b,
  d2c,
  d2d,
  groupGoalCopy,
} from './groupGoalCopy';

export interface GroupedGoalContext {
  groupId: string;
  groupName: string;
  target: GroupGoalTarget | null;
  defaultKind: GroupDefaultKind;
  defaultMonths: number;
}

export type GroupedGoalSource = Pick<
  ProductGroup,
  'id' | 'name' | 'target' | 'defaultKind' | 'defaultMonths' | 'windowMonths' | 'quantity' | 'dimension' | 'amountAvailable' | 'amountUnit' | 'version'
>;

export function groupedGoalFromProductGroup(group: GroupedGoalSource): GroupedGoalContext {
  const resolved = resolveGroupGoal(group as ProductGroup);
  return {
    groupId: group.id,
    groupName: group.name,
    target: resolved.target,
    defaultKind: resolved.defaultKind,
    defaultMonths: resolved.defaultMonths,
  };
}

export function groupedGoalFromRestock(
  item: Pick<RestockGroupView, 'groupId' | 'groupName' | 'groupTarget' | 'defaultKind' | 'defaultMonths'>,
): GroupedGoalContext | null {
  if (!item.groupId || !item.groupName) return null;
  const defaultKind = item.defaultKind ?? 'months';
  const defaultMonths = item.defaultMonths ?? (defaultKind === 'amount' ? 3 : defaultKind === 'dont_restock' ? 0 : 3);
  return {
    groupId: item.groupId,
    groupName: item.groupName,
    target: item.groupTarget ?? null,
    defaultKind,
    defaultMonths,
  };
}

function formatAmount(value: number): string {
  const rounded = Math.round(value * 10) / 10;
  return Number.isInteger(rounded) ? String(rounded) : rounded.toFixed(1);
}

/** CJ copy ids D2a–f for grouped product settings. */
export function groupedD2Line(ctx: GroupedGoalContext): string {
  const target = ctx.target;
  if (target?.kind === 'dont_restock') return groupGoalCopy.groupDontRestock(ctx.groupName);
  if (target?.kind === 'items') return d2a(target.value);
  if (target?.kind === 'months') return d2b(Math.round(target.value));
  if (target?.kind === 'amount' && target.unit === 'fl oz') return d2c(formatAmount(target.value), 'fl oz');
  if (target?.kind === 'amount') return d2c(formatAmount(target.value), 'oz');
  if (ctx.defaultKind === 'dont_restock') return groupGoalCopy.d2e;
  if (ctx.defaultKind === 'amount') return groupGoalCopy.d2f;
  return d2d(ctx.defaultMonths);
}

export interface InventoryGoalWarning {
  text: string;
  singleBlocker?: GroupGoalBlocker;
}

/** CJ copy ids C5/C6 for inventory group rows. */
export function inventoryGroupGoalWarning(
  group: Pick<ProductGroup, 'target'>,
  goalLabel: string,
): InventoryGoalWarning | null {
  const target = group.target;
  if (!target || target.status === 'ok' || target.kind !== 'amount') return null;
  const blockers = target.blockers ?? [];
  if (blockers.length === 0) return null;

  if (target.status === 'size_missing') {
    if (blockers.length === 1) {
      return { text: c5One(blockers[0]!.name, goalLabel), singleBlocker: blockers[0] };
    }
    return { text: c5Many(blockers.length, goalLabel) };
  }

  if (target.status === 'units_mismatch') {
    const goalUnit = target.unit === 'fl oz' ? 'fl oz' : 'oz';
    if (blockers.length === 1) {
      return { text: c6One(blockers[0]!.name, goalLabel, goalUnit), singleBlocker: blockers[0] };
    }
    return { text: c6Many(blockers.length, goalLabel) };
  }

  return null;
}

export function goalLabelForWarning(target: GroupGoalTarget): string {
  if (target.kind === 'amount' && target.unit === 'fl oz') return `${formatAmount(target.value)} fl oz`;
  if (target.kind === 'amount') return `${formatAmount(target.value)} oz`;
  if (target.kind === 'items') return target.value === 1 ? '1 item' : `${target.value} items`;
  if (target.kind === 'months') return `${Math.round(target.value)} mo`;
  return '';
}

export function restockItemIsGrouped(item: { level: string; groupId?: string }): boolean {
  return item.level === 'group' && item.groupId !== undefined && item.groupId !== '';
}