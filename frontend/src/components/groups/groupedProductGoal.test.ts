import { describe, expect, it } from 'vitest';
import type { GroupGoalTarget } from '@stockbot/shared/types';
import {
  groupedD2Line,
  groupedGoalFromRestock,
  inventoryGroupGoalWarning,
  type GroupedGoalContext,
} from './groupedProductGoal';
import { d2a, groupGoalCopy } from './groupGoalCopy';
import { goalTarget } from './syntheticGroup';

const baseCtx = (overrides: Partial<GroupedGoalContext>): GroupedGoalContext => ({
  groupId: 'g1',
  groupName: 'Juice',
  target: null,
  defaultKind: 'months',
  defaultMonths: 3,
  ...overrides,
});

describe('D2a–f grouped product goal line', () => {
  it('D2a: items goal', () => {
    expect(groupedD2Line(baseCtx({ target: goalTarget({ kind: 'items', value: 2, unit: 'items', status: 'ok' }) }))).toBe(d2a(2));
  });

  it('D2e: household default is Dont restock', () => {
    expect(groupedD2Line(baseCtx({ defaultKind: 'dont_restock', defaultMonths: 0 }))).toBe(groupGoalCopy.d2e);
  });

  it('D2f: household default is amount', () => {
    expect(groupedD2Line(baseCtx({ defaultKind: 'amount', defaultMonths: 3 }))).toBe(groupGoalCopy.d2f);
  });
});

describe('groupedGoalFromRestock', () => {
  it('maps groupTarget from a grouped restock row', () => {
    const ctx = groupedGoalFromRestock({
      groupId: 'g-oats',
      groupName: 'Oats',
      groupTarget: { kind: 'items', value: 3, unit: 'items', status: 'ok' },
      defaultKind: 'months',
      defaultMonths: 2,
    });
    expect(ctx?.groupId).toBe('g-oats');
    expect(groupedD2Line(ctx!)).toBe(d2a(3));
  });
});

describe('C5 inventory group row warning', () => {
  const target: GroupGoalTarget = {
    kind: 'amount',
    value: 24,
    unit: 'oz',
    status: 'size_missing',
    blockers: [{ productId: 'p1', name: 'Frozen lemonade', reason: 'size_missing' }],
  };

  it('names one product without size', () => {
    const warning = inventoryGroupGoalWarning({ target }, '24 oz');
    expect(warning?.text).toContain('Frozen lemonade');
    expect(warning?.singleBlocker?.productId).toBe('p1');
  });
});