import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { setupSheetExpiryLine } from './kitCopy456';

describe('setupSheetExpiryLine weekday suffix', () => {
  beforeEach(() => {
    vi.useFakeTimers();
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  it('omits weekday when expiry is on the same calendar day (UTC)', () => {
    vi.setSystemTime(new Date('2026-06-15T10:00:00.000Z'));
    expect(setupSheetExpiryLine('2026-06-15T14:05:00.000Z', 'en-US', 'UTC')).toBe(
      'Use within 10 minutes · expires at 2:05 PM',
    );
  });

  it('adds weekday when expiry crosses midnight (UTC)', () => {
    vi.setSystemTime(new Date('2026-06-15T22:30:00.000Z'));
    expect(setupSheetExpiryLine('2026-06-16T01:04:00.000Z', 'en-US', 'UTC')).toBe(
      'Use within 10 minutes · expires at 1:04 AM (Tue)',
    );
  });

  it('adds weekday when expiry crosses midnight in America/Denver', () => {
    vi.setSystemTime(new Date('2026-06-15T13:00:00.000Z'));
    expect(setupSheetExpiryLine('2026-06-16T13:00:00.000Z', 'en-US', 'America/Denver')).toBe(
      'Use within 10 minutes · expires at 7:00 AM (Tue)',
    );
  });

  it('labels the weekday in the requested zone when UTC and local weekdays differ (Tokyo, 10-minute window)', () => {
    // 23:55 JST Mon -> 00:05 JST Tue; in UTC both instants are Mon 14:55Z/15:05Z
    vi.setSystemTime(new Date('2026-06-15T14:55:00.000Z'));
    expect(setupSheetExpiryLine('2026-06-15T15:05:00.000Z', 'en-US', 'Asia/Tokyo')).toBe(
      'Use within 10 minutes · expires at 12:05 AM (Tue)',
    );
  });

  it('labels Mon at 05:30Z (23:30 MDT) in Denver, where UTC already says Tue', () => {
    vi.setSystemTime(new Date('2026-06-15T05:00:00.000Z')); // Sun 23:00 MDT
    expect(setupSheetExpiryLine('2026-06-16T05:30:00.000Z', 'en-US', 'America/Denver')).toBe(
      'Use within 10 minutes · expires at 11:30 PM (Mon)',
    );
  });

  it('omits the weekday in Denver when only the UTC date rolls over (17:55 -> 18:05 MDT)', () => {
    vi.setSystemTime(new Date('2026-06-15T23:55:00.000Z'));
    expect(setupSheetExpiryLine('2026-06-16T00:05:00.000Z', 'en-US', 'America/Denver')).toBe(
      'Use within 10 minutes · expires at 6:05 PM',
    );
  });
});
