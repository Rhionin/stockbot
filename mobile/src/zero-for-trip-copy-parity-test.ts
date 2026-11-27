import {
  ZERO_FOR_TRIP_STATUS_LABEL,
  formatZeroForTripStatusLine,
  zeroForTripDontRestockAriaLabel,
} from '@stockbot/shared/zeroForTripCopy';

describe('zeroForTrip copy parity (mobile)', () => {
  it('locks A1 and A6', () => {
    expect(ZERO_FOR_TRIP_STATUS_LABEL).toBe('Skipped for this trip');
    expect(formatZeroForTripStatusLine({})).toBe('Skipped for this trip. It comes back next trip.');
  });

  it('locks accessible name', () => {
    expect(zeroForTripDontRestockAriaLabel('Oats')).toBe("Don't restock Oats");
  });
});