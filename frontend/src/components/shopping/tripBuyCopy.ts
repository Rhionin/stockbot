/**
 * CJ-approved #664 user-visible copy (copy-approvals-2026-11-13_1527.md items 2–6).
 * Do not rephrase; tests lock these literals.
 */
export const TRIP_BUY_BOUGHT_CURRENT_YEAR_EXAMPLE = 'Bought Oct 3';

export const TRIP_BUY_BOUGHT_OTHER_YEAR_EXAMPLE = 'Bought Oct 3, 2025';

/** Approved pattern uses the Unicode ellipsis (…), not three ASCII periods. */
export const TRIP_BUY_BARCODE_ELLIPSIS = '…';

export const TRIP_BUY_SAME_NAME_SENTENCE = 'This is a different product with the same name.';

export const TRIP_BUY_SWAP_ERROR = "Couldn't change what to buy. Try again.";

export function tripBuyBarcodeTail(lastFour: string): string {
  return `${TRIP_BUY_BARCODE_ELLIPSIS}${lastFour}`;
}