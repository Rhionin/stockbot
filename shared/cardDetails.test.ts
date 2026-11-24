import { describe, expect, it } from 'vitest';
import {
  CLEARED_EXPIRY_ISO,
  expiryDatePart,
  formatInstanceLabel,
  isRealExpiryDate,
  parseExpiryDraft,
  parseUnitCountText,
  shouldApplyServerUnitCount,
} from './cardDetails';
import type { ItemInstanceWithStatus } from './types';

const instance = (expiresAt: string | null): ItemInstanceWithStatus => ({
  id: 'i',
  itemId: 'item',
  stockInAt: '2026-03-01T00:00:00Z',
  expiresAt,
  removedAt: null,
  removalReason: null,
  createdAt: '2026-03-01T00:00:00Z',
  expiryStatus: 'ok',
});

describe('shouldApplyServerUnitCount', () => {
  it('applies a server count when nothing is unsaved', () => {
    expect(shouldApplyServerUnitCount(2, 2, 5)).toBe(true);
  });
  it('applies the echo of the draft itself', () => {
    expect(shouldApplyServerUnitCount(3, 1, 3)).toBe(true);
  });
  it('keeps an unsaved draft against a different server count', () => {
    expect(shouldApplyServerUnitCount(3, 1, 2)).toBe(false);
  });
});

describe('parseUnitCountText', () => {
  it.each([['1', 1], ['12', 12], [' 4 ', 4]])('accepts %j', (text, value) => {
    expect(parseUnitCountText(text)).toBe(value);
  });
  it.each(['', '0', '-1', '2.5', '1e1', 'abc', ' '])('rejects %j', (text) => {
    expect(parseUnitCountText(text)).toBeNull();
  });
});

describe('expiry dates', () => {
  it('knows real days', () => {
    expect(isRealExpiryDate('2028-02-29')).toBe(true);
    expect(isRealExpiryDate('2027-02-29')).toBe(false);
    expect(isRealExpiryDate('2026-13-01')).toBe(false);
    expect(isRealExpiryDate('2026-1-01')).toBe(false);
    expect(isRealExpiryDate('0000-01-01')).toBe(false);
  });
  it('sends the zero timestamp for an empty draft and a UTC midnight for a date', () => {
    expect(parseExpiryDraft('')).toEqual({ kind: 'clear' });
    expect(parseExpiryDraft('2027-03-04')).toEqual({ kind: 'date', iso: '2027-03-04T00:00:00.000Z' });
    expect(parseExpiryDraft('2027-02-30')).toEqual({ kind: 'invalid' });
    expect(CLEARED_EXPIRY_ISO).toBe('0001-01-01T00:00:00.000Z');
  });
  it('shows the date part of a stored timestamp', () => {
    expect(expiryDatePart(null)).toBe('');
    expect(expiryDatePart('2027-03-04T00:00:00Z')).toBe('2027-03-04');
  });
});

describe('formatInstanceLabel', () => {
  it('names an undated unit', () => {
    expect(formatInstanceLabel(instance(null))).toBe('No expiration date');
  });
  it('names the expiry of a dated unit', () => {
    expect(formatInstanceLabel(instance('2027-03-04T00:00:00Z'))).toMatch(/^Expires .*2027/);
  });
});
