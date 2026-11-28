import { describe, expect, it } from 'vitest';
import {
  shoppingGroupHasRestockTarget,
  shoppingGroupRestockTargetIsZero,
  shoppingListHasRestockRow,
} from './shoppingRestockGate';
import { canChangeRestockTarget } from './zeroForTripNudge';

const restock = {
  items: [{ itemId: 'tea', target: { kind: 'amount' as const, value: 2 } }],
  categories: [{ id: 'cat-snacks', target: { kind: 'amount' as const, value: 1 } }],
};

describe('shoppingListHasRestockRow', () => {
  it('matches the menu permission helper and ignores group ids', () => {
    expect(shoppingListHasRestockRow({ itemId: 'tea' }, restock)).toBe(
      canChangeRestockTarget({ itemId: 'tea', productName: 'Tea' }, restock),
    );
    expect(shoppingListHasRestockRow({ itemId: 'oats' }, restock)).toBe(false);
    expect(shoppingListHasRestockRow({ itemId: 'tea' }, restock)).toBe(true);
  });

  it('groups use explicit group targets, not category ids', () => {
    expect(shoppingGroupHasRestockTarget('grp-1', [{ id: 'grp-1', windowMonths: 3 }])).toBe(true);
    expect(shoppingGroupHasRestockTarget('cat-snacks', [{ id: 'cat-snacks', windowMonths: 3 }])).toBe(true);
    expect(shoppingGroupRestockTargetIsZero('grp-1', [{ id: 'grp-1', windowMonths: 0 }])).toBe(true);
    expect(shoppingGroupRestockTargetIsZero('grp-1', [{ id: 'grp-1', windowMonths: 2 }])).toBe(false);
    const t1 = {
      id: 'grp-t1',
      target: { kind: 'months' as const, value: 6, unit: 'months' as const, status: 'ok' as const, blockers: [] as const },
    };
    expect(shoppingGroupHasRestockTarget('grp-t1', [t1])).toBe(true);
    expect(shoppingGroupRestockTargetIsZero('grp-t1', [t1])).toBe(false);
    expect(shoppingGroupRestockTargetIsZero('grp-t1', [{
      ...t1,
      target: { kind: 'dont_restock' as const, value: 0, unit: null, status: 'ok' as const, blockers: [] },
    }])).toBe(true);
  });
});