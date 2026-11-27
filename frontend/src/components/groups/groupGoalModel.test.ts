import { describe, expect, it } from 'vitest';
import { buildPutBody, coerceGroupTargetPut, resolveGroupGoal } from './groupGoalModel';
import type { ProductGroup } from '@stockbot/shared/types';

describe('coerceGroupTargetPut', () => {
  it('maps months and amount onto legacy fields', () => {
    expect(coerceGroupTargetPut({ kind: 'months', value: 6 }, { dimension: 'mass' })).toEqual({ windowMonths: 6 });
    expect(coerceGroupTargetPut({ kind: 'amount', value: 48 }, { dimension: 'mass' })).toEqual({
      quantity: 48,
      dimension: 'mass',
    });
  });
});

describe('buildPutBody', () => {
  it('sends kind and value without unit or dimension', () => {
    const body = buildPutBody('amount', 24, 'v1');
    expect(body).toEqual({ kind: 'amount', value: 24, expectedVersion: 'v1' });
    expect('unit' in body).toBe(false);
    expect('dimension' in body).toBe(false);
    expect('quantity' in body).toBe(false);
  });

  it('regression: first amount save is not mass default', () => {
    const group: ProductGroup = {
      id: 'g1',
      name: 'Juice',
      rule: 'same_as_ran_out',
      ruleConfirmed: true,
      members: [],
      runningLow: false,
      amountAvailable: true,
      amountUnit: 'fl oz',
      version: 'hash-1',
    };
    const body = buildPutBody('amount', 2, resolveGroupGoal(group).version);
    expect(body).toEqual({ kind: 'amount', value: 2, expectedVersion: 'hash-1' });
    expect(JSON.stringify(body)).not.toContain('mass');
    expect(JSON.stringify(body)).not.toContain('dimension');
  });
});

describe('resolveGroupGoal', () => {
  it('maps legacy windowMonths into months target', () => {
    const resolved = resolveGroupGoal({
      id: 'g1',
      name: 'Oats',
      rule: 'same_as_ran_out',
      ruleConfirmed: true,
      windowMonths: 3,
      members: [],
      runningLow: false,
    });
    expect(resolved.target?.kind).toBe('months');
    expect(resolved.target?.value).toBe(3);
  });
});