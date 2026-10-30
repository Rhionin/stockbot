import { describe, expect, it } from 'vitest';
import { formatOperatorHouseholdStatsLine, formatOperatorHouseholdStatsParts } from './operatorHouseholdCopy';

describe('formatOperatorHouseholdStatsParts', () => {
  it('uses singular copy for counts of 1', () => {
    expect(formatOperatorHouseholdStatsParts({
      memberCount: 1,
      productCount: 1,
      itemsOnHand: 1,
      pendingScans: 1,
      scannersOnline: 1,
      scannersOffline: 1,
      scansThisWeek: 1,
    })).toEqual([
      '1 member',
      '1 product',
      '1 item on hand',
      '1 scanner online',
      '1 scanner offline',
      '1 scan this week',
      '1 pending scan',
    ]);
  });

  it('uses plural copy for zero and counts above 1', () => {
    expect(formatOperatorHouseholdStatsLine({
      memberCount: 0,
      productCount: 4,
      itemsOnHand: 0,
      pendingScans: 0,
      scannersOnline: 0,
      scannersOffline: 2,
      scansThisWeek: 0,
    })).toBe(
      '0 members · 4 products · 0 items on hand · 0 scanners online · 2 scanners offline · 0 scans this week · 0 pending scans',
    );
  });
});
