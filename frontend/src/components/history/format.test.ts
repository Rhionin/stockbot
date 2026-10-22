import { describe, expect, it } from 'vitest';
import { quantityLocked } from '../../api/contract';
import {
  dayHeading, daysLeftLabel, deltaName, deltaText, directionWord, formatMoveClock, formatMoveWhen, formatPaceNumber,
  groupMovesByDay, ledgerEmptyCopy, moveTitle, onHandLabel, orderBackfillPairs, quantityPrompt, trendLabel, undoCopy,
} from './format';

describe('history wording', () => {
  const now = new Date(2026, 9, 10, 15, 4, 0);

  it('rounds a pace to one decimal and keeps a whole number whole', () => {
    expect(formatPaceNumber(2.74)).toBe('2.7');
    expect(formatPaceNumber(3)).toBe('3');
  });

  it('names on hand, days left, and the comparison without shorthand', () => {
    expect(onHandLabel(0)).toBe('None on hand');
    expect(onHandLabel(1)).toBe('1 on hand');
    expect(onHandLabel(4)).toBe('4 on hand');
    expect(daysLeftLabel(1)).toBe('about 1 day left');
    expect(daysLeftLabel(11)).toBe('about 11 days left');
    expect(trendLabel('steady')).toBe('Steady compared with the previous 30 days');
    expect(trendLabel('faster')).toBe('Faster than the previous 30 days');
    expect(trendLabel('slower')).toBe('Slower than the previous 30 days');
    expect(trendLabel('')).toBe('');
  });

  it('dates a move so a weekday is not ambiguous', () => {
    expect(formatMoveWhen(new Date(2026, 9, 10, 8, 14, 0), now, 'scan')).toMatch(/^Today, .+scan$/);
    expect(formatMoveWhen(new Date(2026, 9, 9, 18, 42, 0), now, 'scan')).toMatch(/^Yesterday, .+scan$/);
    expect(formatMoveWhen(new Date(2026, 9, 10, 8, 14, 0), now, 'backfill')).toContain('Added when taking out');
    const at = '2026-10-18T16:40:00.000Z';
    const backfill = { source: 'backfill', direction: 'in', at, productId: 'prod-sour' };
    const pairedOut = { source: 'scan', direction: 'out', at, productId: 'prod-sour' };
    const otherProduct = { source: 'scan', direction: 'out', at, productId: 'prod-milk' };
    const otherTime = { source: 'scan', direction: 'out', at: '2026-10-17T16:40:00.000Z', productId: 'prod-sour' };
    const manualOut = { source: 'manual', direction: 'out', at, productId: 'prod-sour' };
    const scanIn = { source: 'scan', direction: 'in', at, productId: 'prod-sour' };
    const pair = [backfill, pairedOut, otherProduct, otherTime, manualOut, scanIn];
    expect(quantityLocked(backfill, [])).toBe(true);
    expect(quantityLocked(pairedOut, pair)).toBe(true);
    expect(quantityLocked(pairedOut, [pairedOut])).toBe(false);
    expect(quantityLocked(otherProduct, pair)).toBe(false);
    expect(quantityLocked(otherTime, pair)).toBe(false);
    expect(quantityLocked(manualOut, pair)).toBe(false);
    expect(quantityLocked(scanIn, pair)).toBe(false);
    expect(formatMoveClock(new Date(2026, 9, 10, 8, 14, 0), 'backfill')).toContain('Added when taking out');
    const older = formatMoveWhen(new Date(2026, 9, 8, 19, 20, 0), now, 'manual');
    expect(older).toContain('Oct');
    expect(older).toContain('8');
    expect(older).toContain('by hand');
  });

  it('groups a household ledger by calendar day, newest first', () => {
    const today = new Date(2026, 9, 10, 15, 0, 0);
    const earlier = new Date(2026, 9, 10, 8, 0, 0);
    const yesterday = new Date(2026, 9, 9, 18, 0, 0);
    const lastYear = new Date(2025, 11, 2, 9, 0, 0);
    const older = new Date(2026, 9, 8, 12, 0, 0);
    expect(dayHeading(today, now)).toBe('Today');
    expect(dayHeading(yesterday, now)).toBe('Yesterday');
    expect(dayHeading(older, now)).toBe(new Intl.DateTimeFormat(undefined, {
      weekday: 'long', month: 'long', day: 'numeric',
    }).format(older));
    expect(dayHeading(older, now)).not.toMatch(/2026/);
    expect(dayHeading(lastYear, now)).toContain('2025');
    expect(formatMoveClock(today, 'scan')).toMatch(/scan$/);
    expect(formatMoveClock(yesterday, 'manual')).toMatch(/by hand$/);
    expect(directionWord('in')).toBe('Stocked in');
    expect(directionWord('out')).toBe('Used');

    const groups = groupMovesByDay([
      { at: today.toISOString(), name: 'Milk' },
      { at: earlier.toISOString(), name: 'Oats' },
      { at: yesterday.toISOString(), name: 'Chickpeas' },
    ], now);
    expect(groups.map((group) => group.heading)).toEqual(['Today', 'Yesterday']);
    expect(groups[0].moves.map((move) => move.name)).toEqual(['Milk', 'Oats']);
    expect(groups[1].moves.map((move) => move.name)).toEqual(['Chickpeas']);
  });

  it('names an empty household history by the filter in use', () => {
    expect(ledgerEmptyCopy('all', '')).toBe('No stock moves yet. Stock something in or use something, and it shows up here.');
    expect(ledgerEmptyCopy('all', 'beans')).toBe('No moves match that search.');
    expect(ledgerEmptyCopy('in', '')).toBe('No stocked-in moves in this list.');
    expect(ledgerEmptyCopy('in', 'beans')).toBe('No stocked-in moves match that search.');
    expect(ledgerEmptyCopy('out', '')).toBe('No uses in this list.');
    expect(ledgerEmptyCopy('out', 'beans')).toBe('No uses match that search.');
  });

  it('labels the quantity change and the undo consequence', () => {
    expect(moveTitle('out', 2)).toBe('Used 2');
    expect(moveTitle('in', 6)).toBe('Stocked in 6');
    expect(deltaText('out', 2)).toBe('−2');
    expect(deltaName('in', 6)).toBe('6 stocked in');
    expect(quantityPrompt('out')).toBe('How many units were actually used?');
    expect(undoCopy('out', 2)).toEqual({
      title: 'Undo this use?',
      body: '2 units go back on the shelf. The pace is recalculated from the remaining history.',
      confirm: 'Undo this use',
    });
    expect(undoCopy('in', 1).body).toBe('1 unit leaves the shelf. The pace is recalculated from the remaining history.');
    const pairCopy = {
      title: 'Undo this add and take out?',
      body: 'This removes the added unit and the unit taken out. The pace is recalculated from the remaining history.',
      confirm: 'Undo add and take out',
    };
    expect(undoCopy('in', 1, true)).toEqual(pairCopy);
    expect(undoCopy('out', 1, true)).toEqual(pairCopy);
    expect(undoCopy('in', 1, true).body).not.toContain('leaves the shelf');
    expect(undoCopy('out', 2, true).body).toBe(
      'This removes the 2 units that were added and the 2 units that were taken out. The pace is recalculated from the remaining history.',
    );
  });

  it('keeps the added row immediately before its use', () => {
    const at = '2026-10-18T16:40:00.000Z';
    const added = { id: 'in', source: 'backfill', direction: 'in', at, productId: 'prod-sour' };
    const used = { id: 'out', source: 'scan', direction: 'out', at, productId: 'prod-sour' };
    const between = { id: 'oats', source: 'manual', direction: 'in', at: '2026-10-18T15:00:00.000Z', productId: 'prod-oats' };
    const other = { id: 'milk', source: 'scan', direction: 'out', at, productId: 'prod-milk' };
    expect(orderBackfillPairs([used, between, added, other]).map((move) => move.id)).toEqual(['in', 'oats', 'out', 'milk']);
    expect(orderBackfillPairs([added, used, other]).map((move) => move.id)).toEqual(['in', 'out', 'milk']);
    expect(orderBackfillPairs([other, between]).map((move) => move.id)).toEqual(['milk', 'oats']);
  });
});
