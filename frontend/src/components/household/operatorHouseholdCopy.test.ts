import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import {
  formatOperatorHouseholdInventoryLine,
  formatOperatorHouseholdLastActivity,
  formatOperatorHouseholdScannersLine,
  formatOperatorHouseholdScansLine,
  formatOperatorHouseholdStatLines,
} from './operatorHouseholdCopy';

describe('formatOperatorHouseholdInventoryLine', () => {
  it('uses singular copy for counts of 1', () => {
    expect(formatOperatorHouseholdInventoryLine({
      memberCount: 1,
      productCount: 1,
      itemsOnHand: 1,
      pendingScans: 0,
      scannersOnline: 0,
      scannersOffline: 0,
      scansThisWeek: 0,
    })).toBe('1 member · 1 product · 1 item on hand');
  });

  it('uses plural copy for zero and counts above 1', () => {
    expect(formatOperatorHouseholdInventoryLine({
      memberCount: 4,
      productCount: 12,
      itemsOnHand: 37,
      pendingScans: 2,
      scannersOnline: 2,
      scannersOffline: 3,
      scansThisWeek: 41,
    })).toBe('4 members · 12 products · 37 items on hand');
  });
});

describe('formatOperatorHouseholdScannersLine', () => {
  it('never pluralizes online or offline', () => {
    expect(formatOperatorHouseholdScannersLine({
      memberCount: 0,
      productCount: 0,
      itemsOnHand: 0,
      pendingScans: 0,
      scannersOnline: 2,
      scannersOffline: 3,
      scansThisWeek: 0,
    })).toBe('Scanners: 2 online · 3 offline');
  });
});

describe('formatOperatorHouseholdScansLine', () => {
  it('uses singular copy for counts of 1', () => {
    expect(formatOperatorHouseholdScansLine({
      memberCount: 0,
      productCount: 0,
      itemsOnHand: 0,
      pendingScans: 1,
      scannersOnline: 0,
      scannersOffline: 0,
      scansThisWeek: 1,
    })).toBe('1 scan this week · 1 pending scan');
  });
});

describe('formatOperatorHouseholdLastActivity', () => {
  beforeEach(() => {
    vi.useFakeTimers();
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  it('returns No activity yet when last activity is missing', () => {
    vi.setSystemTime(new Date(2026, 9, 30, 12, 0, 0));
    expect(formatOperatorHouseholdLastActivity(undefined)).toBe('No activity yet');
    expect(formatOperatorHouseholdLastActivity('')).toBe('No activity yet');
  });

  it('omits the year for activity in the current year', () => {
    const now = new Date(2026, 9, 30, 12, 0, 0);
    vi.setSystemTime(now);
    const activity = new Date(2026, 9, 29, 6, 0, 0);
    const expectedTail = activity.toLocaleString(undefined, {
      month: 'short',
      day: 'numeric',
      hour: 'numeric',
      minute: '2-digit',
    });
    expect(formatOperatorHouseholdLastActivity(activity.toISOString(), now)).toBe(`Last activity ${expectedTail}`);
    expect(formatOperatorHouseholdLastActivity(activity.toISOString(), now)).not.toMatch(/\b2026\b/);
  });

  it('includes the year for activity in a past year', () => {
    const now = new Date(2026, 9, 30, 12, 0, 0);
    vi.setSystemTime(now);
    const activity = new Date(2025, 9, 29, 6, 0, 0);
    const expectedTail = activity.toLocaleString(undefined, {
      month: 'short',
      day: 'numeric',
      hour: 'numeric',
      minute: '2-digit',
      year: 'numeric',
    });
    expect(formatOperatorHouseholdLastActivity(activity.toISOString(), now)).toBe(`Last activity ${expectedTail}`);
  });
});

describe('formatOperatorHouseholdStatLines', () => {
  it('groups inventory, scanners, scans, and last activity', () => {
    const now = new Date(2026, 9, 30, 12, 0, 0);
    vi.useFakeTimers();
    vi.setSystemTime(now);
    const lines = formatOperatorHouseholdStatLines({
      memberCount: 4,
      productCount: 12,
      itemsOnHand: 37,
      pendingScans: 2,
      scannersOnline: 2,
      scannersOffline: 3,
      scansThisWeek: 41,
      lastActivityAt: new Date(2026, 9, 29, 6, 0, 0).toISOString(),
    }, now);
    expect(lines.inventory).toBe('4 members · 12 products · 37 items on hand');
    expect(lines.scanners).toBe('Scanners: 2 online · 3 offline');
    expect(lines.scans).toBe('41 scans this week · 2 pending scans');
    expect(lines.lastActivity).toMatch(/^Last activity /);
    vi.useRealTimers();
  });
});
