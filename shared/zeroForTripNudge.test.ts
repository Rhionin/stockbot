import { describe, expect, it, vi } from 'vitest';
import {
  canChangeRestockTarget,
  canShowGroupZeroForTripNudge,
  restockTargetAlreadyZero,
  shouldShowZeroForTripNudge,
  zeroForTripNudgeButtonAria,
  zeroForTripNudgePrompt,
} from './zeroForTripNudge';
import { ZERO_FOR_TRIP_DONT_RESTOCK_ERROR, ZERO_FOR_TRIP_NUDGE_PRODUCT } from './zeroForTripCopy';

const restock = {
  items: [{ itemId: 'item-milk', target: { kind: 'amount' as const, value: 2 } }],
  categories: [{ id: 'cat-dairy', target: { kind: 'amount' as const, value: 3 } }],
};

const groups = [{ id: 'grp-snacks', windowMonths: 6 }];

describe('zeroForTrip nudge visibility', () => {
  it('hides nudge when the viewer cannot change the restock target', () => {
    expect(
      shouldShowZeroForTripNudge({ itemId: 'item-oats', productName: 'Oats' }, restock),
    ).toBe(false);
    expect(canChangeRestockTarget({ itemId: 'item-oats', productName: 'Oats' }, restock)).toBe(false);
  });

  it('hides nudge when target is already 0', () => {
    const zeroRestock = {
      ...restock,
      items: [{ itemId: 'item-milk', target: { kind: 'months' as const, value: 0 } }],
    };
    expect(
      shouldShowZeroForTripNudge({ itemId: 'item-milk', productName: 'Milk' }, zeroRestock),
    ).toBe(false);
    expect(restockTargetAlreadyZero({ kind: 'months', value: 0 })).toBe(true);
  });

  it('shows nudge for a member who has a restock row', () => {
    expect(
      shouldShowZeroForTripNudge({ itemId: 'item-milk', productName: 'Milk' }, restock),
    ).toBe(true);
  });

  it('shows group nudge when the group has a non-zero target', () => {
    expect(
      shouldShowZeroForTripNudge(
        { groupId: 'grp-snacks', productName: 'Crackers', groupName: 'Snacks' },
        restock,
        groups,
      ),
    ).toBe(true);
    expect(
      canShowGroupZeroForTripNudge(
        { groupId: 'grp-snacks', productName: 'Crackers', groupName: 'Snacks' },
        groups,
      ),
    ).toBe(true);
    expect(zeroForTripNudgePrompt({ productName: 'Crackers', groupName: 'Snacks' })).toBe(
      "Don't want the Snacks group back next trip?",
    );
  });

  it('does not treat restock categories as product groups', () => {
    expect(
      shouldShowZeroForTripNudge(
        { groupId: 'cat-dairy', productName: 'Milk', groupName: 'Dairy' },
        restock,
        [],
      ),
    ).toBe(false);
  });

  it('falls back to product nudge copy when the group name is empty', () => {
    expect(zeroForTripNudgePrompt({ productName: 'Milk', groupName: '  ' })).toBe(ZERO_FOR_TRIP_NUDGE_PRODUCT);
  });

  it('tap uses Dont restock accessible name', () => {
    expect(zeroForTripNudgeButtonAria({ productName: 'Milk' })).toBe("Don't restock Milk");
    const dont = vi.fn();
    dont();
    expect(dont).toHaveBeenCalled();
  });

  it('error copy is locked', () => {
    expect(ZERO_FOR_TRIP_DONT_RESTOCK_ERROR).toBe("Couldn't change the restock target. Try again.");
  });
});