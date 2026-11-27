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

/** Maps legacy quantity/windowMonths into the §8.1 shape until the API sends `target`. */
export function resolveGroupGoal(group: ProductGroup): ResolvedGroupGoal {
  const defaultKind = group.defaultKind
    ?? (group.defaultMonths === 0 ? 'dont_restock' : 'months');
  const defaultMonths = group.defaultMonths
    ?? (defaultKind === 'amount' ? 3 : defaultKind === 'dont_restock' ? 0 : 3);

  if (group.target !== undefined) {
    return {
      target: group.target,
      defaultKind,
      defaultMonths,
      amountAvailable: group.amountAvailable ?? false,
      amountUnit: group.amountUnit ?? null,
      version: group.version ?? '',
    };
  }

  let target: GroupGoalTarget | null = null;
  if (group.windowMonths === 0) {
    target = {
      kind: 'dont_restock',
      value: 0,
      unit: null,
      status: 'ok',
    };
  } else if (group.windowMonths !== undefined && group.windowMonths > 0) {
    target = {
      kind: 'months',
      value: group.windowMonths,
      unit: 'months',
      status: 'ok',
    };
  } else if (group.quantity !== undefined && group.quantity > 0) {
    const volume = group.dimension === 'volume';
    target = {
      kind: 'amount',
      value: group.quantity,
      unit: volume ? 'fl oz' : 'oz',
      status: 'ok',
    };
  }

  return {
    target,
    defaultKind,
    defaultMonths,
    amountAvailable: group.amountAvailable ?? false,
    amountUnit: group.amountUnit ?? (group.dimension === 'volume' ? 'fl oz' : group.dimension === 'mass' ? 'oz' : null),
    version: group.version ?? '',
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

/** Maps §8.1 PUT bodies onto legacy group target fields until the API accepts `kind`/`value`. */
export function coerceGroupTargetPut(
  body: GroupTarget,
  group: Pick<ProductGroup, 'dimension' | 'amountUnit'>,
): GroupTarget {
  if (body.clear !== undefined || body.dontRestock !== undefined || body.windowMonths !== undefined) {
    return body;
  }
  if (body.kind === 'months' && body.value !== undefined) {
    const { kind, value, quantity, dimension, ...rest } = body;
    return { ...rest, windowMonths: value };
  }
  if (body.kind === 'amount' && body.value !== undefined) {
    const dimension = group.dimension
      ?? (group.amountUnit === 'fl oz' ? 'volume' : 'mass');
    const { kind, value, windowMonths, ...rest } = body;
    return { ...rest, quantity: value, dimension };
  }
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