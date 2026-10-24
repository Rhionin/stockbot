import { describe, expect, it } from 'vitest';
import fc from 'fast-check';
import type { ProcessingNotice, ScanEntry, ScanStatus } from '@stockbot/shared/types';
import { addProcessingNotice, batchTimeBounds, formatBatchScanCount, formatBatchTimeRange, formatReviewCount, getEntriesForView, groupScansIntoBatches, isValidUnitCount, mergeScanEvent, pruneSelection, removeProcessingNotice, settleProcessingNotice, sortScansNewestFirst, toggleSelectAll, isBatchEligible } from './queueUtils';

const DISPLAYABLE_SCAN_STATUSES: ScanStatus[] = ['pending', 'flagged', 'qr'];

const scanEntry = (overrides: Partial<ScanEntry>): ScanEntry => ({
  id: 'scan-1',
  userId: 'user-1',
  barcode: '111',
  scannedAt: '2026-03-20T10:00:00Z',
  direction: null,
  unitCount: 1,
  expiresAt: null,
  status: 'pending',
  productId: null,
  committedAt: null,
  createdAt: '2026-03-20T10:00:00Z',
  ...overrides,
});

describe('sortScansNewestFirst', () => {
  it('orders entries by scannedAt descending', () => {
    const older = scanEntry({ id: 'older', scannedAt: '2026-03-20T09:00:00Z' });
    const newer = scanEntry({ id: 'newer', scannedAt: '2026-03-20T11:00:00Z' });

    expect(sortScansNewestFirst([older, newer]).map((entry) => entry.id)).toEqual(['newer', 'older']);
  });
});

const atOffset = (baseMs: number, offsetMs: number) => new Date(baseMs + offsetMs).toISOString();

describe('groupScansIntoBatches', () => {
  const base = Date.parse('2026-03-20T16:00:00.000Z');
  const fiveMinutes = 5 * 60 * 1000;

  it('keeps consecutive scans under 5 minutes in one session', () => {
    const entries = [
      scanEntry({ id: 'c', scannedAt: atOffset(base, 8 * 60 * 1000) }),
      scanEntry({ id: 'a', scannedAt: atOffset(base, 0) }),
      scanEntry({ id: 'b', scannedAt: atOffset(base, 4 * 60 * 1000) }),
    ];

    const batches = groupScansIntoBatches(entries);
    expect(batches).toHaveLength(1);
    expect(batches[0].map((entry) => entry.id)).toEqual(['c', 'b', 'a']);
  });

  it('starts a new session at a gap of exactly 5 minutes', () => {
    const entries = [
      scanEntry({ id: 'early', scannedAt: atOffset(base, 0) }),
      scanEntry({ id: 'later', scannedAt: atOffset(base, fiveMinutes) }),
    ];

    const batches = groupScansIntoBatches(entries);
    expect(batches.map((batch) => batch.map((entry) => entry.id))).toEqual([['later'], ['early']]);
  });

  it('keeps a pair that is 1ms under 5 minutes together', () => {
    const entries = [
      scanEntry({ id: 'early', scannedAt: atOffset(base, 0) }),
      scanEntry({ id: 'later', scannedAt: atOffset(base, fiveMinutes - 1) }),
    ];

    expect(groupScansIntoBatches(entries)).toHaveLength(1);
  });

  it('allows one session to span more than 5 minutes when each step is shorter', () => {
    const entries = [0, 4, 8, 12].map((minutes, index) =>
      scanEntry({ id: `step-${index}`, scannedAt: atOffset(base, minutes * 60 * 1000) }),
    );

    const batches = groupScansIntoBatches(entries);
    expect(batches).toHaveLength(1);
    expect(batches[0].map((entry) => entry.id)).toEqual(['step-3', 'step-2', 'step-1', 'step-0']);
    const span = new Date(batches[0][0].scannedAt).getTime() - new Date(batches[0][3].scannedAt).getTime();
    expect(span).toBeGreaterThan(fiveMinutes);
  });

  it('shows the newest session first and the newest scan first inside it', () => {
    const entries = [
      scanEntry({ id: 'old-a', scannedAt: atOffset(base, 0) }),
      scanEntry({ id: 'old-b', scannedAt: atOffset(base, 2 * 60 * 1000) }),
      scanEntry({ id: 'new-a', scannedAt: atOffset(base, 20 * 60 * 1000) }),
      scanEntry({ id: 'new-b', scannedAt: atOffset(base, 22 * 60 * 1000) }),
    ];

    expect(groupScansIntoBatches(entries).map((batch) => batch.map((entry) => entry.id))).toEqual([
      ['new-b', 'new-a'],
      ['old-b', 'old-a'],
    ]);
  });

  it('moves a rescanned entry to the front of its session on a live event and a reload', () => {
    const oats = scanEntry({ id: 'oats', scannedAt: atOffset(base, 0), unitCount: 1 });
    const milk = scanEntry({ id: 'milk', scannedAt: atOffset(base, 2 * 60 * 1000) });
    const rescanned = scanEntry({ id: 'oats', scannedAt: atOffset(base, 4 * 60 * 1000), unitCount: 2 });

    const live = groupScansIntoBatches(sortScansNewestFirst(mergeScanEvent([oats, milk], rescanned)));
    const reloaded = groupScansIntoBatches([milk, rescanned]);

    expect(live.map((batch) => batch.map((entry) => entry.id))).toEqual([['oats', 'milk']]);
    expect(live[0][0]?.unitCount).toBe(2);
    expect(reloaded).toEqual(live);
  });

  it('pulls a rescanned card out of an older session to the top', () => {
    const stale = scanEntry({ id: 'stale', scannedAt: atOffset(base, 0) });
    const recent = scanEntry({ id: 'recent', scannedAt: atOffset(base, 20 * 60 * 1000) });
    const rescanned = scanEntry({
      id: 'stale',
      scannedAt: atOffset(base, 21 * 60 * 1000),
      unitCount: 2,
    });

    expect(groupScansIntoBatches(sortScansNewestFirst(mergeScanEvent([stale, recent], rescanned)))
      .map((batch) => batch.map((entry) => entry.id))).toEqual([['stale', 'recent']]);
  });

  it('moves a rescanned entry into the newest session when the new scan is 5 minutes later', () => {
    const oats = scanEntry({ id: 'oats', scannedAt: atOffset(base, 0) });
    const milk = scanEntry({ id: 'milk', scannedAt: atOffset(base, 2 * 60 * 1000) });
    const rescanned = scanEntry({ id: 'oats', scannedAt: atOffset(base, 20 * 60 * 1000), unitCount: 2 });

    const live = groupScansIntoBatches(sortScansNewestFirst(mergeScanEvent([oats, milk], rescanned)));

    expect(live.map((batch) => batch.map((entry) => entry.id))).toEqual([['oats'], ['milk']]);
  });

  it('orders a fresh oldest-first load the same as a newest-first live insert', () => {
    const older = scanEntry({ id: 'older', scannedAt: atOffset(base, 0) });
    const middle = scanEntry({ id: 'middle', scannedAt: atOffset(base, 2 * 60 * 1000) });
    const newest = scanEntry({ id: 'newest', scannedAt: atOffset(base, 4 * 60 * 1000) });
    const freshLoad = groupScansIntoBatches([older, middle, newest]);
    const live = groupScansIntoBatches(sortScansNewestFirst(mergeScanEvent([older, middle], newest)));

    expect(freshLoad.map((batch) => batch.map((entry) => entry.id))).toEqual([['newest', 'middle', 'older']]);
    expect(live).toEqual(freshLoad);
  });

  it('keeps an older session newest-first when a live scan opens a new session', () => {
    const older = scanEntry({ id: 'older', scannedAt: atOffset(base, 0) });
    const middle = scanEntry({ id: 'middle', scannedAt: atOffset(base, 2 * 60 * 1000) });
    const newest = scanEntry({ id: 'newest', scannedAt: atOffset(base, 20 * 60 * 1000) });
    const fromApi = groupScansIntoBatches([older, middle, newest]);
    const fromLive = groupScansIntoBatches(sortScansNewestFirst([middle, older, newest]));

    expect(fromApi.map((batch) => batch.map((entry) => entry.id))).toEqual([
      ['newest'],
      ['middle', 'older'],
    ]);
    expect(fromLive).toEqual(fromApi);
  });

  it('preserves input order when scannedAt ties', () => {
    const same = atOffset(base, 0);
    const first = scanEntry({ id: 'first', scannedAt: same });
    const second = scanEntry({ id: 'second', scannedAt: same });

    expect(groupScansIntoBatches([first, second])[0].map((entry) => entry.id)).toEqual(['first', 'second']);
    expect(groupScansIntoBatches([second, first])[0].map((entry) => entry.id)).toEqual(['second', 'first']);
  });

  it('returns no sessions for an empty queue', () => {
    expect(groupScansIntoBatches([])).toEqual([]);
  });

  it('groups only by consecutive scannedAt gaps', () => {
    fc.assert(fc.property(
      fc.array(fc.record({
        id: fc.uuid(),
        offsetMs: fc.integer({ min: 0, max: 60 * 60 * 1000 }),
      }), { maxLength: 25 }),
      (rows) => {
        const seen = new Set<string>();
        const entries = rows.flatMap((row) => {
          if (seen.has(row.id)) return [];
          seen.add(row.id);
          return [scanEntry({ id: row.id, scannedAt: atOffset(base, row.offsetMs) })];
        });
        const batches = groupScansIntoBatches(entries);
        const displayed = batches.flat();
        expect(displayed.map((entry) => entry.id).sort()).toEqual(entries.map((entry) => entry.id).sort());
        for (let index = 1; index < displayed.length; index += 1) {
          expect(new Date(displayed[index - 1].scannedAt).getTime())
            .toBeGreaterThanOrEqual(new Date(displayed[index].scannedAt).getTime());
        }

        for (const batch of batches) {
          for (let index = 1; index < batch.length; index += 1) {
            const gap = new Date(batch[index - 1].scannedAt).getTime() - new Date(batch[index].scannedAt).getTime();
            expect(gap).toBeGreaterThanOrEqual(0);
            expect(gap).toBeLessThan(fiveMinutes);
          }
        }

        for (let index = 0; index < batches.length - 1; index += 1) {
          const newer = batches[index];
          const older = batches[index + 1];
          const gap = new Date(newer[newer.length - 1].scannedAt).getTime() - new Date(older[0].scannedAt).getTime();
          expect(gap).toBeGreaterThanOrEqual(fiveMinutes);
        }
      },
    ), { numRuns: 100 });
  });
});

describe('formatBatchTimeRange', () => {
  it('uses one clock time when the session starts and ends in the same minute', () => {
    const label = formatBatchTimeRange('2026-03-20T16:02:10.000Z', '2026-03-20T16:02:50.000Z');
    expect(label).not.toContain('\u2013');
    expect(label.length).toBeGreaterThan(0);
  });

  it('joins the earliest and latest times on the same day', () => {
    const label = formatBatchTimeRange('2026-03-20T16:02:00.000Z', '2026-03-20T16:18:00.000Z');
    const [start, end] = label.split('\u2013');
    expect(start?.length).toBeGreaterThan(0);
    expect(end?.length).toBeGreaterThan(0);
    expect(start).not.toEqual(end);
  });

  it('includes the date when the session crosses local days', () => {
    const label = formatBatchTimeRange('2026-03-20T12:00:00.000Z', '2026-03-22T18:00:00.000Z');
    expect(label).toContain('\u2013');
    expect(label).toMatch(/\d/);
  });

  it('reads bounds from the earliest and latest scan, not array order', () => {
    const entries = [
      scanEntry({ id: 'late', scannedAt: '2026-03-20T16:18:00.000Z' }),
      scanEntry({ id: 'early', scannedAt: '2026-03-20T16:02:00.000Z' }),
    ];
    expect(batchTimeBounds(entries)).toEqual({
      earliest: '2026-03-20T16:02:00.000Z',
      latest: '2026-03-20T16:18:00.000Z',
    });
  });
});

describe('formatBatchScanCount', () => {
  it('singularizes a single scan', () => {
    expect(formatBatchScanCount(1)).toBe('1 scan');
    expect(formatBatchScanCount(0)).toBe('0 scans');
    expect(formatBatchScanCount(12)).toBe('12 scans');
  });
});

const processingNotice = (overrides: Partial<ProcessingNotice>): ProcessingNotice => ({
  id: 'lookup-1',
  userId: 'user-1',
  barcode: '111',
  direction: 'stock_in',
  scannedAt: '2026-03-20T12:00:00Z',
  ...overrides,
});

describe('processing notices', () => {
  it('prepends a notice and ignores a replay of the same id', () => {
    const first = processingNotice({ id: 'a', barcode: '111' });
    const second = processingNotice({ id: 'b', barcode: '222' });

    const added = addProcessingNotice(addProcessingNotice([], first), second);
    expect(added.map((notice) => notice.id)).toEqual(['b', 'a']);
    expect(addProcessingNotice(added, second)).toBe(added);
  });

  it('settles the oldest matching lookup and leaves a commit alone', () => {
    const older = processingNotice({ id: 'older', scannedAt: '2026-03-20T12:00:00Z' });
    const newer = processingNotice({ id: 'newer', scannedAt: '2026-03-20T12:01:00Z' });
    const notices = addProcessingNotice(addProcessingNotice([], older), newer);

    const settled = settleProcessingNotice(notices, scanEntry({ barcode: '111', direction: 'stock_in', status: 'pending' }));
    expect(settled.map((notice) => notice.id)).toEqual(['newer']);

    const committed = settleProcessingNotice(notices, scanEntry({ barcode: '111', direction: 'stock_in', status: 'committed' }));
    expect(committed).toEqual(notices);
  });

  it('removes a notice by id', () => {
    const notice = processingNotice({ id: 'gone' });
    expect(removeProcessingNotice([notice], 'gone')).toEqual([]);
  });
});

describe('mergeScanEvent', () => {
  it('appends a pending event for a new id', () => {
    const entries = [scanEntry({ id: 'existing' })];
    const event = scanEntry({ id: 'new', status: 'pending' });

    expect(mergeScanEvent(entries, event)).toEqual([entries[0], event]);
  });

  it('keeps a group hint when a live scan event does not include one', () => {
    const entries = [scanEntry({ id: 'a', groupHint: { groupId: 'beans', name: 'Cut green beans' } })];
    const event = scanEntry({ id: 'a', unitCount: 2 });

    expect(mergeScanEvent(entries, event)[0]?.groupHint).toEqual({ groupId: 'beans', name: 'Cut green beans' });
    expect(mergeScanEvent(entries, event)[0]?.unitCount).toBe(2);
  });

  it('replaces an existing entry in place with a flagged event', () => {
    const entries = [
      scanEntry({ id: 'a', barcode: '111' }),
      scanEntry({ id: 'b', barcode: '222' }),
      scanEntry({ id: 'c', barcode: '333' }),
    ];
    const event = scanEntry({ id: 'b', barcode: '222', status: 'flagged' });

    expect(mergeScanEvent(entries, event)).toEqual([entries[0], event, entries[2]]);
  });

  it('removes an existing entry when the event is committed', () => {
    const entries = [scanEntry({ id: 'a' }), scanEntry({ id: 'b' })];
    const event = scanEntry({ id: 'a', status: 'committed' });

    expect(mergeScanEvent(entries, event)).toEqual([entries[1]]);
  });

  it('removes an existing entry when the event is cancelled', () => {
    const entries = [scanEntry({ id: 'a' }), scanEntry({ id: 'b' })];
    const event = scanEntry({ id: 'b', status: 'cancelled' });

    expect(mergeScanEvent(entries, event)).toEqual([entries[0]]);
  });

  // Feature: realtime-scan-updates, Property 9: Merging a Scan_Event upserts
  // displayable entries and removes non-displayable ones
  //
  // **Validates: Requirements 4.2, 4.3**
  //
  // For any list of displayed scan entries and any incoming Scan_Event, mergeScanEvent
  // SHALL produce a list containing an entry with the event's id, equal to the event,
  // exactly once when the event's status is pending, flagged, or qr, and SHALL produce a list
  // containing no entry with the event's id when the event's status is committed or
  // cancelled; in both cases every other entry already in the list SHALL remain unchanged
  // and in the same relative order.
  it('Property 9: upserts displayable entries and removes non-displayable ones', () => {
    const statusArbitrary: fc.Arbitrary<ScanStatus> = fc.constantFrom(
      'pending',
      'flagged',
      'committed',
      'cancelled',
      'qr',
    );
    const entryArbitrary = fc.record({
      id: fc.uuid(),
      status: statusArbitrary,
    }).map(({ id, status }) => scanEntry({ id, status }));

    fc.assert(fc.property(
      fc.uniqueArray(entryArbitrary, { maxLength: 20, selector: (entry) => entry.id }),
      entryArbitrary,
      (entries, event) => {
        const result = mergeScanEvent(entries, event);
        const others = entries.filter((entry) => entry.id !== event.id);
        const matchesForEventId = result.filter((entry) => entry.id === event.id);

        if (DISPLAYABLE_SCAN_STATUSES.includes(event.status)) {
          expect(matchesForEventId).toEqual([event]);
        } else {
          expect(matchesForEventId).toEqual([]);
        }
        expect(result.filter((entry) => entry.id !== event.id)).toEqual(others);
      },
    ), { numRuns: 100 });
  });
});

describe('pruneSelection', () => {
  it('drops an id no longer present in the entries list and keeps the rest', () => {
    const entries = [scanEntry({ id: 'a' }), scanEntry({ id: 'c' })];

    expect(pruneSelection(['a', 'b', 'c'], entries)).toEqual(['a', 'c']);
  });

  // Feature: realtime-scan-updates, Property 10: Removing an entry from the displayed
  // list prunes it from the selection
  //
  // **Validates: Requirements 4.4**
  //
  // For any list of selected entry ids and any list of currently displayed entries,
  // pruneSelection SHALL produce a list containing exactly the ids from the input that
  // name an entry present in the displayed list, in the same relative order, and no others.
  it('Property 10: keeps exactly the selected ids present in the displayed list, in order', () => {
    fc.assert(fc.property(
      fc.array(fc.uuid(), { maxLength: 20 }),
      fc.array(fc.uuid(), { maxLength: 20 }).map((ids) => ids.map((id) => scanEntry({ id }))),
      (selectedIds, entries) => {
        const expected = selectedIds.filter((id) => entries.some((entry) => entry.id === id));

        expect(pruneSelection(selectedIds, entries)).toEqual(expected);
      },
    ), { numRuns: 100 });
  });
});
describe('isValidUnitCount', () => {
  it('returns false for zero', () => {
    expect(isValidUnitCount(0)).toBe(false);
  });

  it('returns true for one', () => {
    expect(isValidUnitCount(1)).toBe(true);
  });

  it('returns false for negative numbers', () => {
    expect(isValidUnitCount(-1)).toBe(false);
  });

  it('returns false for decimal numbers', () => {
    expect(isValidUnitCount(1.5)).toBe(false);
  });

  // Feature: scan-queue-polish, Property 1: Unit count validity
  //
  // **Validates: Requirements 2.6**
  //
  // For any number, isValidUnitCount SHALL return true if and only if that number
  // is an integer greater than or equal to one.
  it('Property 1: returns true for positive integers and false otherwise', () => {
    const numberArbitrary = fc.integer();

    fc.assert(fc.property(
      numberArbitrary,
      (value) => {
        const expected = Number.isInteger(value) && value >= 1;
        expect(isValidUnitCount(value)).toBe(expected);
      },
    ), { numRuns: 100 });
  });
});

describe('isBatchEligible', () => {
  it('returns true for pending entries with direction set', () => {
    const entry = scanEntry({ status: 'pending', direction: 'stock_in' });
    expect(isBatchEligible(entry)).toBe(true);
  });

  it('returns false for pending entries with direction null', () => {
    const entry = scanEntry({ status: 'pending', direction: null });
    expect(isBatchEligible(entry)).toBe(false);
  });

  it('returns false for flagged entries with direction set', () => {
    const entry = scanEntry({ status: 'flagged', direction: 'stock_out' });
    expect(isBatchEligible(entry)).toBe(false);
  });

  it('returns false for committed entries', () => {
    const entry = scanEntry({ status: 'committed', direction: 'stock_in' });
    expect(isBatchEligible(entry)).toBe(false);
  });

  it('returns false for cancelled entries', () => {
    const entry = scanEntry({ status: 'cancelled', direction: 'stock_out' });
    expect(isBatchEligible(entry)).toBe(false);
  });
});

describe('toggleSelectAll', () => {
  it('selects all eligible entries when none are selected', () => {
    const entries = [
      scanEntry({ id: 'a', status: 'pending', direction: 'stock_in' }),
      scanEntry({ id: 'b', status: 'pending', direction: 'stock_out' }),
      scanEntry({ id: 'c', status: 'pending', direction: null }),
      scanEntry({ id: 'd', status: 'flagged', direction: 'stock_in' }),
    ];

    expect(toggleSelectAll(entries, [])).toEqual(['a', 'b']);
  });

  it('deselects all eligible entries when all are selected', () => {
    const entries = [
      scanEntry({ id: 'a', status: 'pending', direction: 'stock_in' }),
      scanEntry({ id: 'b', status: 'pending', direction: 'stock_out' }),
    ];

    expect(toggleSelectAll(entries, ['a', 'b'])).toEqual([]);
  });

  it('preserves non-eligible entries that were already selected', () => {
    const entries = [
      scanEntry({ id: 'a', status: 'pending', direction: 'stock_in' }),
      scanEntry({ id: 'b', status: 'flagged', direction: null }),
      scanEntry({ id: 'c', status: 'pending', direction: 'stock_out' }),
    ];

    expect(toggleSelectAll(entries, ['b'])).toEqual(['b', 'a', 'c']);
  });

  it('adds all eligible entries when only some are already selected', () => {
    const entries = [
      scanEntry({ id: 'a', status: 'pending', direction: 'stock_in' }),
      scanEntry({ id: 'b', status: 'flagged', direction: null }),
      scanEntry({ id: 'c', status: 'pending', direction: 'stock_out' }),
    ];

    // When not all eligible are selected, all eligible are added while preserving selected non-eligible
    // selectedIds=['b','a'] means 'b' (non-eligible) is selected and 'a' (eligible) is selected
    // Result: 'b' preserved, 'a' already in result, 'c' added as eligible
    expect(toggleSelectAll(entries, ['b', 'a'])).toEqual(['b', 'a', 'c']);
  });

  it('preserves the order of non-eligible entries', () => {
    const entries = [
      scanEntry({ id: 'a', status: 'pending', direction: 'stock_in' }),
      scanEntry({ id: 'b', status: 'flagged', direction: null }),
      scanEntry({ id: 'c', status: 'pending', direction: 'stock_out' }),
      scanEntry({ id: 'd', status: 'flagged', direction: 'stock_out' }),
    ];

    expect(toggleSelectAll(entries, ['b', 'd'])).toEqual(['b', 'd', 'a', 'c']);
  });

  // Feature: scan-queue-polish, Property 2: Select-all toggles exactly the eligible
  // entries and preserves the rest
  //
  // **Validates: Requirements 3.2, 3.3, 3.4**
  //
  // For any list of displayed scan entries and any current batch selection,
  // toggleSelectAll SHALL produce a selection in which every displayed
  // Batch_Eligible_Entry is selected if and only if at least one Batch_Eligible_Entry
  // was unselected beforehand, and in which the selected/unselected state of every
  // entry that is not a Batch_Eligible_Entry is unchanged from the input selection.
  it('Property 2: toggles eligible entries and preserves non-eligible entries', () => {
    const directionArbitrary = fc.oneof(
      fc.constant('stock_in'),
      fc.constant('stock_out'),
      fc.constant(null),
    );
    const statusArbitrary = fc.oneof(
      fc.constant('pending'),
      fc.constant('flagged'),
      fc.constant('committed'),
      fc.constant('cancelled'),
    );
    const entryArbitrary = fc.record({
      id: fc.uuid(),
      status: statusArbitrary,
      direction: directionArbitrary,
    }).map(({ id, status, direction }) => scanEntry({ id, status, direction }));

    fc.assert(fc.property(
      fc.uniqueArray(entryArbitrary, { maxLength: 20, selector: (entry) => entry.id }),
      fc.array(fc.uuid(), { maxLength: 20 }),
      (entries, selectedIds) => {
        const eligibleIds = entries.filter(isBatchEligible).map((entry) => entry.id);
        const nonEligibleIds = entries.filter((entry) => !isBatchEligible(entry)).map((entry) => entry.id);

        const result = toggleSelectAll(entries, selectedIds);

        // Verify non-eligible entries are preserved in order
        const preservedNonEligible = selectedIds.filter((id) => nonEligibleIds.includes(id));
        const resultNonEligible = result.filter((id) => nonEligibleIds.includes(id));
        expect(resultNonEligible).toEqual(preservedNonEligible);

        // Verify eligible entries are all selected or none selected
        const selectedEligible = result.filter((id) => eligibleIds.includes(id));
        const previouslySelectedEligible = selectedIds.filter((id) => eligibleIds.includes(id));

        if (previouslySelectedEligible.length === eligibleIds.length && eligibleIds.length > 0) {
          // If all eligible were selected, none should be selected now
          expect(selectedEligible).toEqual([]);
        } else {
          // Otherwise, all eligible should be selected
          expect(selectedEligible.sort()).toEqual(eligibleIds.sort());
        }
      },
    ), { numRuns: 100 });
  });
});
describe('formatReviewCount', () => {
  it('renders a small count verbatim', () => {
    expect(formatReviewCount(0)).toBe('0');
    expect(formatReviewCount(7)).toBe('7');
  });

  it('renders exactly 99 at the cap boundary without the plus', () => {
    expect(formatReviewCount(99)).toBe('99');
  });

  it('caps anything above 99 as "99+"', () => {
    expect(formatReviewCount(100)).toBe('99+');
    expect(formatReviewCount(4321)).toBe('99+');
  });

  it('caps every count over 99 and shows the exact value otherwise', () => {
    fc.assert(fc.property(
      fc.integer({ min: 0, max: 10000 }),
      (count) => {
        const expected = count > 99 ? '99+' : String(count);
        expect(formatReviewCount(count)).toBe(expected);
      },
    ), { numRuns: 100 });
  });
});

describe('getEntriesForView', () => {
  // Feature: scan-queue-polish, Property 3: Every entry belongs to exactly one queue view
  //
  // **Validates: Requirements 9.1, 9.2, 9.3, 9.4**
  //
  // For any list of scan entries and either queue view, getEntriesForView partitions the entries
  // so that an entry appears in the Stock_In_View result if and only if its direction equals
  // 'stock_in', appears in the Stock_Out_View result if and only if its direction equals
  // 'stock_out' or is null, and appears in exactly one of the two results — never both,
  // never neither.

  it('Property 3: partitions entries so each belongs to exactly one view', () => {
    const directionArbitrary = fc.oneof(
      fc.constant('stock_in'),
      fc.constant('stock_out'),
      fc.constant(null),
    );
    const entryArbitrary = fc.record({
      id: fc.uuid(),
      direction: directionArbitrary,
    }).map(({ id, direction }) => scanEntry({ id, direction }));

    fc.assert(fc.property(
      fc.array(entryArbitrary, { maxLength: 20 }),
      (entries) => {
        const stockInEntries = getEntriesForView(entries, 'stock_in');
        const stockOutEntries = getEntriesForView(entries, 'stock_out');

        // Every entry appears in exactly one of the two results
        entries.forEach((entry) => {
          const inStockIn = stockInEntries.some((e) => e.id === entry.id);
          const inStockOut = stockOutEntries.some((e) => e.id === entry.id);

          if (entry.direction === 'stock_in') {
            expect(inStockIn).toBe(true);
            expect(inStockOut).toBe(false);
          } else {
            expect(inStockIn).toBe(false);
            expect(inStockOut).toBe(true);
          }
        });

        // The two results are disjoint
        stockInEntries.forEach((entry) => {
          expect(stockOutEntries.some((e) => e.id === entry.id)).toBe(false);
        });

        // Together they cover all input entries
        const allViewEntries = [...stockInEntries, ...stockOutEntries];
        expect(allViewEntries.length).toBe(entries.length);
        entries.forEach((entry) => {
          expect(allViewEntries.some((e) => e.id === entry.id)).toBe(true);
        });
      },
    ), { numRuns: 100 });
  });
});
