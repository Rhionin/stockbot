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

  it('adds weekday when expiry crosses Denver midnight near 05:30Z (23:30 MDT)', () => {
    vi.setSystemTime(new Date('2026-06-16T05:00:00.000Z'));
    expect(setupSheetExpiryLine('2026-06-16T06:30:00.000Z', 'en-US', 'America/Denver')).toBe(
      'Use within 10 minutes · expires at 12:30 AM (Tue)',
    );
  });
});
