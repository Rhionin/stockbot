import type {
  GroupGoalBlocker,
  InGroupErrorMembers,
  TargetConflictMember,
} from '@stockbot/shared/types';

export interface ParsedApiErrorBody {
  field: string;
  blockers: GroupGoalBlocker[];
  amountAvailable?: boolean;
  amountUnit?: 'oz' | 'fl oz' | null;
  conflictMembers: TargetConflictMember[];
  inGroup?: InGroupErrorMembers;
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return value !== null && typeof value === 'object' && !Array.isArray(value);
}

function readBlockers(body: Record<string, unknown>): GroupGoalBlocker[] {
  if (!Array.isArray(body.blockers)) return [];
  const blockers: GroupGoalBlocker[] = [];
  for (const row of body.blockers) {
    if (!isRecord(row)) continue;
    const productId = typeof row.productId === 'string' ? row.productId : '';
    const name = typeof row.name === 'string' ? row.name : '';
    const reason = row.reason === 'size_missing' || row.reason === 'units_mismatch' ? row.reason : null;
    if (productId === '' || name === '' || reason === null) continue;
    const sizeKind = row.sizeKind === 'weight' || row.sizeKind === 'volume' ? row.sizeKind : null;
    blockers.push({ productId, name, reason, sizeKind });
  }
  return blockers;
}

function readConflictMembers(value: unknown): TargetConflictMember[] {
  if (!Array.isArray(value)) return [];
  const members: TargetConflictMember[] = [];
  for (const row of value) {
    if (!isRecord(row)) continue;
    const productId = typeof row.productId === 'string' ? row.productId : '';
    const name = typeof row.name === 'string' ? row.name : '';
    if (productId === '' || name === '') continue;
    members.push({
      productId,
      name,
      windowMonths: typeof row.windowMonths === 'number' ? row.windowMonths : undefined,
      quantity: typeof row.quantity === 'number' ? row.quantity : undefined,
      amount: typeof row.amount === 'number' ? row.amount : undefined,
      dimension: row.dimension === 'mass' || row.dimension === 'volume' ? row.dimension : undefined,
    });
  }
  return members;
}

function readInGroup(value: unknown): InGroupErrorMembers | undefined {
  if (!isRecord(value)) return undefined;
  const groupId = typeof value.groupId === 'string' ? value.groupId : '';
  const groupName = typeof value.groupName === 'string' ? value.groupName : '';
  if (groupId === '') return undefined;
  return { groupId, groupName };
}

/** Parses #771 top-level error envelope fields (§6.2 / §8.3). */
export function parseApiErrorBody(body: unknown, code: string): ParsedApiErrorBody {
  const base: ParsedApiErrorBody = {
    field: '',
    blockers: [],
    conflictMembers: [],
  };
  if (!isRecord(body)) return base;

  base.field = typeof body.field === 'string' ? body.field : '';
  base.blockers = readBlockers(body);
  if (typeof body.amountAvailable === 'boolean') base.amountAvailable = body.amountAvailable;
  if (body.amountUnit === 'oz' || body.amountUnit === 'fl oz') base.amountUnit = body.amountUnit;
  else if (body.amountUnit === null) base.amountUnit = null;

  if (code === 'in_group') {
    base.inGroup = readInGroup(body.members);
    return base;
  }

  base.conflictMembers = readConflictMembers(body.members);
  return base;
}