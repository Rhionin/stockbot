import type {
  GroupDefaultKind,
  GroupGoalKind,
  GroupGoalTarget,
  GroupTarget,
  ProductGroup,
} from '@stockbot/shared/types';

export type EditorKind = 'items' | 'months' | 'amount';

export interface ResolvedGroupGoal {
  target: GroupGoalTarget | null;
  defaultKind: GroupDefaultKind;
  defaultMonths: number;
  amountAvailable: boolean;
  amountUnit: 'oz' | 'fl oz' | null;
  version: string;
}

export function resolveGroupGoal(group: ProductGroup): ResolvedGroupGoal {
  return {
    target: group.target,
    defaultKind: group.defaultKind,
    defaultMonths: group.defaultMonths,
    amountAvailable: group.amountAvailable,
    amountUnit: group.amountUnit,
    version: group.version,
  };
}

/** Maps §8.1 `target` onto quantity/windowMonths for bin/seesaw helpers. */
export function groupForSeesaw(
  group: Pick<ProductGroup, 'target' | 'defaultKind' | 'defaultMonths' | 'usage' | 'quantity' | 'dimension' | 'windowMonths'>,
): Pick<ProductGroup, 'quantity' | 'dimension' | 'windowMonths' | 'usage'> {
  const target = group.target;
  if (target?.kind === 'amount') {
    return {
      usage: group.usage ?? undefined,
      quantity: target.value,
      dimension: target.unit === 'fl oz' ? 'volume' : 'mass',
      windowMonths: undefined,
    };
  }
  if (target?.kind === 'months') {
    return {
      usage: group.usage ?? undefined,
      windowMonths: target.value,
      quantity: undefined,
      dimension: undefined,
    };
  }
  if (target?.kind === 'dont_restock') {
    return {
      usage: group.usage ?? undefined,
      windowMonths: 0,
      quantity: undefined,
      dimension: undefined,
    };
  }
  if (target?.kind === 'items') {
    return {
      usage: group.usage ?? undefined,
      quantity: undefined,
      windowMonths: undefined,
      dimension: undefined,
    };
  }
  if (group.defaultKind === 'dont_restock') {
    return {
      usage: group.usage ?? undefined,
      windowMonths: 0,
      quantity: undefined,
      dimension: undefined,
    };
  }
  return {
    usage: group.usage ?? undefined,
    windowMonths: group.defaultMonths,
    quantity: group.quantity ?? undefined,
    dimension: group.dimension,
  };
}

export function initialEditorKind(
  resolved: ResolvedGroupGoal,
): EditorKind {
  const kind = resolved.target?.kind;
  if (kind === 'items' || kind === 'months' || kind === 'amount') return kind;
  return 'months';
}

export function prefillValue(
  editorKind: EditorKind,
  resolved: ResolvedGroupGoal,
): number | '' {
  const target = resolved.target;
  if (target?.kind === editorKind) return target.value;
  if (editorKind === 'months' && resolved.defaultKind !== 'dont_restock' && resolved.defaultMonths > 0) {
    return resolved.defaultMonths;
  }
  return '';
}

export function buildPutBody(
  kind: EditorKind,
  value: number,
  version: string,
): GroupTarget {
  const body: GroupTarget = { kind, value };
  if (version !== '') body.expectedVersion = version;
  return body;
}

export function buildDontRestockPut(version: string): GroupTarget {
  const body: GroupTarget = { dontRestock: true };
  if (version !== '') body.expectedVersion = version;
  return body;
}

export function buildClearPut(version: string): GroupTarget {
  const body: GroupTarget = { clear: true };
  if (version !== '') body.expectedVersion = version;
  return body;
}

export function isGroupDontRestock(resolved: ResolvedGroupGoal): boolean {
  return resolved.target?.kind === 'dont_restock';
}

export function isHouseholdDontDefault(resolved: ResolvedGroupGoal): boolean {
  return resolved.target === null && resolved.defaultKind === 'dont_restock';
}

export function savedGoalKind(resolved: ResolvedGroupGoal): GroupGoalKind | null {
  const kind = resolved.target?.kind;
  return kind ?? null;
}

export function groupTargetForUndo(
  group: ProductGroup,
): GroupTarget {
  const target = group.target;
  if (!target) return { clear: true };
  if (target.kind === 'dont_restock') return { dontRestock: true };
  if (target.kind === 'items' || target.kind === 'months' || target.kind === 'amount') {
    return { kind: target.kind, value: target.value };
  }
  return { clear: true };
}