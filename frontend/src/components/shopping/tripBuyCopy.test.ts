import { describe, expect, it } from 'vitest';
import {
  TRIP_BUY_BARCODE_ELLIPSIS,
  TRIP_BUY_BOUGHT_CURRENT_YEAR_EXAMPLE,
  TRIP_BUY_BOUGHT_OTHER_YEAR_EXAMPLE,
  TRIP_BUY_SAME_NAME_SENTENCE,
  TRIP_BUY_SWAP_ERROR,
  tripBuyBarcodeTail,
} from './tripBuyCopy';
import { barcodeTail, buildTripBuyViews, formatBought, pickDifferentiatorField } from './tripBuyDifferentiator';
import type { ShoppingListGroupMember } from '@stockbot/shared/types';

const member = (overrides: Partial<ShoppingListGroupMember> & Pick<ShoppingListGroupMember, 'itemId' | 'name'>): ShoppingListGroupMember => ({
  productId: `prod-${overrides.itemId}`,
  onHand: 0,
  ...overrides,
});

describe('tripBuyCopy (CJ-approved #664 literals)', () => {
  it('matches copy-approvals-2026-11-13_1527.md items 2–6 character for character', () => {
    expect(TRIP_BUY_BOUGHT_CURRENT_YEAR_EXAMPLE).toBe('Bought Oct 3');
    expect(TRIP_BUY_BOUGHT_OTHER_YEAR_EXAMPLE).toBe('Bought Oct 3, 2025');
    expect(TRIP_BUY_BARCODE_ELLIPSIS).toBe('…');
    expect(TRIP_BUY_BARCODE_ELLIPSIS).not.toBe('...');
    expect(TRIP_BUY_SAME_NAME_SENTENCE).toBe('This is a different product with the same name.');
    expect(TRIP_BUY_SWAP_ERROR).toBe("Couldn't change what to buy. Try again.");
  });

  it('formats bought dates using the approved examples', () => {
    const now = new Date('2026-11-01T00:00:00Z');
    expect(formatBought('2026-10-03T12:00:00Z', now)).toBe(TRIP_BUY_BOUGHT_CURRENT_YEAR_EXAMPLE);
    expect(formatBought('2025-10-03T12:00:00Z', now)).toBe(TRIP_BUY_BOUGHT_OTHER_YEAR_EXAMPLE);
  });

  it('builds barcode tails with the approved ellipsis', () => {
    expect(tripBuyBarcodeTail('4821')).toBe('…4821');
    expect(barcodeTail(['000000004821'])).toBe('…4821');
  });

  it('shows the approved fallback sentence on the option', () => {
    const members = [
      member({ itemId: 'a', name: 'Same Label' }),
      member({ itemId: 'b', name: 'SAME LABEL' }),
    ];
    expect(pickDifferentiatorField(members)).toBe('fallback');
    expect(buildTripBuyViews(members)[0]?.differentiator).toBe(TRIP_BUY_SAME_NAME_SENTENCE);
  });
});