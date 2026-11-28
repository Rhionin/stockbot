import { describe, expect, it } from 'vitest';
import { parseApiErrorBody } from './apiErrorExtras';

describe('parseApiErrorBody', () => {
  it('reads field, blockers, amount extras for goal saves', () => {
    const parsed = parseApiErrorBody({
      field: 'value',
      blockers: [{
        productId: 'p1',
        name: 'Apple juice',
        reason: 'size_missing',
      }],
      amountAvailable: false,
      amountUnit: 'fl oz',
    }, 'amount_unavailable');
    expect(parsed.field).toBe('value');
    expect(parsed.blockers).toHaveLength(1);
    expect(parsed.amountAvailable).toBe(false);
    expect(parsed.amountUnit).toBe('fl oz');
    expect(parsed.conflictMembers).toEqual([]);
  });

  it('maps in_group members object without treating it as an array', () => {
    const parsed = parseApiErrorBody({
      members: { groupId: 'g-juice', groupName: 'Juice' },
    }, 'in_group');
    expect(parsed.inGroup).toEqual({ groupId: 'g-juice', groupName: 'Juice' });
    expect(parsed.conflictMembers).toEqual([]);
  });

  it('keeps target_decision_required members as an array', () => {
    const parsed = parseApiErrorBody({
      members: [{ productId: 'p1', name: 'Rolled oats', quantity: 2 }],
    }, 'target_decision_required');
    expect(parsed.conflictMembers).toHaveLength(1);
    expect(parsed.inGroup).toBeUndefined();
  });
});