import { describe, expect, it } from 'vitest';
import { buildPutBody, groupForSeesaw, groupTargetForUndo, resolveGroupGoal } from './groupGoalModel';
import type { ProductGroup } from '@stockbot/shared/types';

const base: ProductGroup = {
  id: 'g1',
  name: 'Oats',
  rule: 'same_as_ran_out',
  ruleConfirmed: true,
  members: [],
  runningLow: false,
  target: null,
  amountAvailable: false,
  amountUnit: null,
  amountBlockers: [],
  defaultKind: 'months',
  defaultMonths: 3,
  version: 'v1',
};

describe('buildPutBody', () => {
  it('sends kind and value', () => {
    expect(buildPutBody('amount', 24, 'v1')).toEqual({ kind: 'amount', value: 24, expectedVersion: 'v1' });
  });
});

describe('resolveGroupGoal', () => {
  it('reads the §8.1 target from GET', () => {
    const resolved = resolveGroupGoal({
      ...base,
      target: { kind: 'months', value: 3, unit: 'months', status: 'ok', blockers: [] },
    });
    expect(resolved.target?.kind).toBe('months');
    expect(resolved.target?.value).toBe(3);
  });
});

describe('groupForSeesaw', () => {
  it('maps amount target to quantity', () => {
    expect(groupForSeesaw({
      ...base,
      target: { kind: 'amount', value: 48, unit: 'oz', status: 'ok', blockers: [] },
    })).toMatchObject({ quantity: 48, dimension: 'mass' });
  });
});

describe('groupTargetForUndo', () => {
  it('replays months goals on undo', () => {
    expect(groupTargetForUndo({
      ...base,
      target: { kind: 'months', value: 6, unit: 'months', status: 'ok', blockers: [] },
    })).toEqual({ kind: 'months', value: 6 });
  });
});