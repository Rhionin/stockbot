import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import {
  formatOperatorHouseholdCreated,
  formatOperatorHouseholdInventoryLine,
  formatOperatorHouseholdLastActivity,
  formatOperatorHouseholdRestockLabel,
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

describe('formatOperatorHouseholdRestockLabel', () => {
  it('maps each restock mode to household copy', () => {
    expect(formatOperatorHouseholdRestockLabel('auto')).toBe('New items restock automatically');
    expect(formatOperatorHouseholdRestockLabel('ask_restock')).toBe('Ask before restocking new items');
    expect(formatOperatorHouseholdRestockLabel('ask_hold')).toBe('Ask before restocking, and hold until you answer');
  });
});

describe('formatOperatorHouseholdCreated', () => {
  const now2026 = new Date(2026, 9, 30, 12, 0, 0);

  beforeEach(() => {
    vi.stubEnv('TZ', 'UTC');
  });

  afterEach(() => {
    vi.unstubAllEnvs();
  });

  it('uses short month and day in the current year', () => {
    expect(formatOperatorHouseholdCreated('2026-04-01T00:00:00Z', now2026)).toBe('Created Apr 1');
  });

  it('includes the year when created in a past year', () => {
    expect(formatOperatorHouseholdCreated('2025-03-15T12:00:00Z', now2026)).toBe('Created Mar 15, 2025');
  });

  it('uses UTC calendar date for a near-midnight UTC timestamp', () => {
    expect(formatOperatorHouseholdCreated('2026-04-01T03:00:00.000Z', now2026)).toBe('Created Apr 1');
  });

  it('uses Denver local calendar date for a near-midnight UTC timestamp', () => {
    vi.unstubAllEnvs();
    vi.stubEnv('TZ', 'America/Denver');
    expect(formatOperatorHouseholdCreated('2026-04-01T03:00:00.000Z', now2026)).toBe('Created Mar 31');
  });
});

describe('formatOperatorHouseholdLastActivity', () => {
  beforeEach(() => {
    vi.useFakeTimers();
    vi.stubEnv('TZ', 'UTC');
  });

  afterEach(() => {
    vi.useRealTimers();
    vi.unstubAllEnvs();
  });

  it('returns No activity yet when last activity is missing', () => {
    vi.setSystemTime(new Date(2026, 9, 30, 12, 0, 0));
    expect(formatOperatorHouseholdLastActivity(undefined)).toBe('No activity yet');
    expect(formatOperatorHouseholdLastActivity('')).toBe('No activity yet');
  });

  it('omits the year for activity in the current year', () => {
    const now = new Date(2026, 9, 30, 12, 0, 0);
    vi.setSystemTime(now);
    expect(formatOperatorHouseholdLastActivity('2026-09-29T06:00:00.000Z', now)).toBe('Last activity Sep 29, 6:00 AM');
    expect(formatOperatorHouseholdLastActivity('2026-09-29T06:00:00.000Z', now)).not.toMatch(/\b2026\b/);
  });

  it('includes the year for activity in a past year', () => {
    const now = new Date(2026, 9, 30, 12, 0, 0);
    vi.setSystemTime(now);
    expect(formatOperatorHouseholdLastActivity('2025-09-29T06:00:00.000Z', now)).toBe('Last activity Sep 29, 2025, 6:00 AM');
  });

  it('includes the prior calendar year near a UTC year boundary', () => {
    const now = new Date(2027, 0, 1, 12, 0, 0);
    vi.setSystemTime(now);
    expect(formatOperatorHouseholdLastActivity('2027-01-01T03:00:00.000Z', now)).toBe('Last activity Jan 1, 3:00 AM');
  });

  it('shows Denver local year-boundary activity with the year when now is the next calendar year', () => {
    vi.unstubAllEnvs();
    vi.stubEnv('TZ', 'America/Denver');
    const now = new Date(2027, 0, 1, 12, 0, 0);
    vi.setSystemTime(now);
    expect(formatOperatorHouseholdLastActivity('2027-01-01T03:00:00.000Z', now)).toBe('Last activity Dec 31, 2026, 8:00 PM');
  });
});

describe('formatOperatorHouseholdStatLines', () => {
  beforeEach(() => {
    vi.useFakeTimers();
    vi.stubEnv('TZ', 'UTC');
    vi.setSystemTime(new Date(2026, 9, 30, 12, 0, 0));
  });

  afterEach(() => {
    vi.useRealTimers();
    vi.unstubAllEnvs();
  });

  it('groups inventory, scanners, scans, and last activity', () => {
    const now = new Date(2026, 9, 30, 12, 0, 0);
    const lines = formatOperatorHouseholdStatLines({
      memberCount: 4,
      productCount: 12,
      itemsOnHand: 37,
      pendingScans: 2,
      scannersOnline: 2,
      scannersOffline: 3,
      scansThisWeek: 41,
      lastActivityAt: '2026-09-29T06:00:00.000Z',
    }, now);
    expect(lines.inventory).toBe('4 members · 12 products · 37 items on hand');
    expect(lines.scanners).toBe('Scanners: 2 online · 3 offline');
    expect(lines.scans).toBe('41 scans this week · 2 pending scans');
    expect(lines.lastActivity).toBe('Last activity Sep 29, 6:00 AM');
  });
});
