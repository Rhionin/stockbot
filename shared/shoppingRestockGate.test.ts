import { describe, expect, it } from 'vitest';
import { shoppingListHasRestockRow } from './shoppingRestockGate';
import { canChangeRestockTarget } from './zeroForTripNudge';

const restock = {
  items: [{ itemId: 'tea', target: { kind: 'amount' as const, value: 2 } }],
  categories: [{ id: 'cat-snacks', target: { kind: 'amount' as const, value: 1 } }],
};

describe('shoppingListHasRestockRow', () => {
  it('matches the nudge permission helper', () => {
    expect(shoppingListHasRestockRow({ itemId: 'tea' }, restock)).toBe(
      canChangeRestockTarget({ itemId: 'tea', productName: 'Tea' }, restock),
    );
    expect(shoppingListHasRestockRow({ itemId: 'oats' }, restock)).toBe(false);
  });
});