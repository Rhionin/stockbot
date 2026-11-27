import { describe, expect, it } from 'vitest';
import {
  ZERO_FOR_TRIP_STATUS_LABEL,
  ZERO_FOR_TRIP_INLINE_CONFIRM,
  ZERO_FOR_TRIP_CHANGE_AMOUNT_LABEL,
  ZERO_FOR_TRIP_NUDGE_PRODUCT,
  ZERO_FOR_TRIP_NUDGE_GROUP,
  ZERO_FOR_TRIP_DONT_RESTOCK_BUTTON,
  ZERO_FOR_TRIP_DONT_RESTOCK_ERROR,
  formatZeroForTripStatusLine,
  safeFormatZeroForTripStatusLine,
  zeroForTripDontRestockAriaLabel,
  zeroForTripDontRestockGroupAriaLabel,
} from './zeroForTripCopy';

const FIXED_NOW = new Date('2026-11-14T12:00:00.000Z');
const formatWhen = () => 'today';

describe('zeroForTripCopy A1–A6', () => {
  it('A1 status label', () => {
    expect(ZERO_FOR_TRIP_STATUS_LABEL).toBe('Skipped for this trip');
  });

  it('A2 who and when product', () => {
    expect(
      formatZeroForTripStatusLine({
        actorDisplayName: 'Ada',
        setAt: '2026-11-14T10:00:00.000Z',
      }, { formatWhen, now: FIXED_NOW }),
    ).toBe('Ada skipped it for this trip today. It comes back next trip.');
  });

  it('A3 who only product', () => {
    expect(formatZeroForTripStatusLine({ actorDisplayName: 'Ada' })).toBe(
      'Ada skipped it for this trip. It comes back next trip.',
    );
  });

  it('A4 when only product', () => {
    expect(
      formatZeroForTripStatusLine({ setAt: '2026-11-14T10:00:00.000Z' }, { formatWhen, now: FIXED_NOW }),
    ).toBe('Skipped for this trip today. It comes back next trip.');
  });

  it('A5 household admin and when', () => {
    expect(
      formatZeroForTripStatusLine({
        actorUserId: 'u1',
        actorRole: 'owner',
        setAt: '2026-11-14T10:00:00.000Z',
      }, { formatWhen, now: FIXED_NOW }),
    ).toBe('A household admin skipped it for this trip today. It comes back next trip.');
  });

  it('A6 legacy product', () => {
    expect(formatZeroForTripStatusLine({})).toBe(ZERO_FOR_TRIP_INLINE_CONFIRM);
  });

  it('A2 group who and when', () => {
    expect(
      formatZeroForTripStatusLine({
        groupName: 'Snacks',
        actorDisplayName: 'Ada',
        setAt: '2026-11-14T10:00:00.000Z',
      }, { formatWhen, now: FIXED_NOW }),
    ).toBe('Ada skipped the Snacks group for this trip today. It comes back next trip.');
  });

  it('A3 group who only', () => {
    expect(
      formatZeroForTripStatusLine({ groupName: 'Snacks', actorDisplayName: 'Ada' }),
    ).toBe('Ada skipped the Snacks group for this trip. It comes back next trip.');
  });

  it('A4 group when only', () => {
    expect(
      formatZeroForTripStatusLine({ groupName: 'Snacks', setAt: '2026-11-14T10:00:00.000Z' }, { formatWhen, now: FIXED_NOW }),
    ).toBe('Skipped the Snacks group for this trip today. It comes back next trip.');
  });

  it('A6 group legacy', () => {
    expect(formatZeroForTripStatusLine({ groupName: 'Snacks' })).toBe(
      'Skipped the Snacks group for this trip. It comes back next trip.',
    );
  });
});

describe('zeroForTripCopy B1–B3', () => {
  it('B1 nudge product', () => {
    expect(ZERO_FOR_TRIP_NUDGE_PRODUCT).toBe("Don't want it back next trip?");
  });

  it('B2 nudge group', () => {
    expect(ZERO_FOR_TRIP_NUDGE_GROUP('Snacks')).toBe("Don't want the Snacks group back next trip?");
  });

  it('B3 error', () => {
    expect(ZERO_FOR_TRIP_DONT_RESTOCK_ERROR).toBe("Couldn't change the restock target. Try again.");
  });

  it('Change amount label', () => {
    expect(ZERO_FOR_TRIP_CHANGE_AMOUNT_LABEL).toBe('Change amount');
  });

  it('existing Dont restock button', () => {
    expect(ZERO_FOR_TRIP_DONT_RESTOCK_BUTTON).toBe("Don't restock");
  });

  it('accessible names', () => {
    expect(zeroForTripDontRestockAriaLabel('Milk')).toBe("Don't restock Milk");
    expect(zeroForTripDontRestockGroupAriaLabel('Dairy')).toBe("Don't restock the Dairy group");
  });
});

describe('safeFormatZeroForTripStatusLine', () => {
  it('does not throw on unknown shape', () => {
    expect(safeFormatZeroForTripStatusLine(undefined)).toBe(ZERO_FOR_TRIP_INLINE_CONFIRM);
    expect(safeFormatZeroForTripStatusLine(null)).toBe(ZERO_FOR_TRIP_INLINE_CONFIRM);
  });
});