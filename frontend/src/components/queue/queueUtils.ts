import type { ItemInstanceWithStatus, ProcessingNotice, ScanDirection, ScanEntry, ScanStatus } from '@stockbot/shared/types';

const DISPLAYABLE_SCAN_STATUSES: ScanStatus[] = ['pending', 'flagged', 'qr'];

export const sortScansNewestFirst = (entries: ScanEntry[]): ScanEntry[] =>
  [...entries].sort(
    (left, right) =>
      new Date(right.scannedAt).getTime() - new Date(left.scannedAt).getTime(),
  );

// A session is a shopping trip, not a stored id. Consecutive scans belong
// together while each step is strictly under 5 minutes; a longer pause starts
// another session even when the whole trip runs past 5 minutes.
export const BATCH_GAP_MS = 5 * 60 * 1000;

const scannedAtMillis = (entry: ScanEntry): number => {
  const value = new Date(entry.scannedAt).getTime();
  return Number.isFinite(value) ? value : 0;
};

// Walk newest-first so the open session and the rows inside it stay in the
// same order on a fresh load, a refresh, and a live insert. Equal timestamps
// keep their input order; the stable sort does not reshuffle a tie.
export const groupScansIntoBatches = (entries: ScanEntry[]): ScanEntry[][] => {
  const newestFirst = [...entries].sort(
    (left, right) => scannedAtMillis(right) - scannedAtMillis(left),
  );
  const batches: ScanEntry[][] = [];
  for (const entry of newestFirst) {
    const current = batches[batches.length - 1];
    if (current === undefined) {
      batches.push([entry]);
      continue;
    }
    const previous = current[current.length - 1];
    const gap = scannedAtMillis(previous) - scannedAtMillis(entry);
    if (gap < BATCH_GAP_MS) current.push(entry);
    else batches.push([entry]);
  }
  return batches;
};

export const formatBatchScanCount = (count: number): string =>
  count === 1 ? '1 scan' : `${count} scans`;

// first–last scannedAt for a session header. Same local minute collapses to
// one time; a later day includes the date so "Yesterday" still has a range.
export const formatBatchTimeRange = (earliestIso: string, latestIso: string): string => {
  const start = new Date(earliestIso);
  const end = new Date(latestIso);
  const timeOnly: Intl.DateTimeFormatOptions = { hour: 'numeric', minute: '2-digit' };
  const sameDay = start.toDateString() === end.toDateString();
  const sameMinute = sameDay
    && start.getHours() === end.getHours()
    && start.getMinutes() === end.getMinutes();
  if (sameMinute) return start.toLocaleTimeString(undefined, timeOnly);
  if (sameDay) {
    return `${start.toLocaleTimeString(undefined, timeOnly)}\u2013${end.toLocaleTimeString(undefined, timeOnly)}`;
  }
  const dateTime: Intl.DateTimeFormatOptions = {
    month: 'short',
    day: 'numeric',
    hour: 'numeric',
    minute: '2-digit',
  };
  return `${start.toLocaleString(undefined, dateTime)}\u2013${end.toLocaleString(undefined, dateTime)}`;
};

export const batchTimeBounds = (entries: ScanEntry[]): { earliest: string; latest: string } => {
  if (entries.length === 0) return { earliest: '', latest: '' };
  let earliest = entries[0];
  let latest = entries[0];
  for (const entry of entries.slice(1)) {
    if (scannedAtMillis(entry) < scannedAtMillis(earliest)) earliest = entry;
    if (scannedAtMillis(entry) > scannedAtMillis(latest)) latest = entry;
  }
  return { earliest: earliest.scannedAt, latest: latest.scannedAt };
};

export const sortInstancesUseOldestFirst = (
  instances: ItemInstanceWithStatus[],
): ItemInstanceWithStatus[] =>
  [...instances].sort((left, right) => {
    if (left.expiresAt === null) return right.expiresAt === null ? left.id.localeCompare(right.id) : 1;
    if (right.expiresAt === null) return -1;
    const dateOrder = new Date(left.expiresAt).getTime() - new Date(right.expiresAt).getTime();
    return dateOrder === 0 ? left.id.localeCompare(right.id) : dateOrder;
  });

export const formatExpiryDate = (value: string): string =>
  new Intl.DateTimeFormat(undefined, { dateStyle: 'medium', timeZone: 'UTC' }).format(
    new Date(value),
  );

export const expiryDateToISOString = (expiryDate: string): string | undefined =>
  expiryDate === '' ? undefined : new Date(`${expiryDate}T00:00:00.000Z`).toISOString();

// A Batch_Eligible_Entry is a pending entry whose direction has been
// determined. Requirement 3.
export const isBatchEligible = (entry: ScanEntry): boolean =>
  entry.status === 'pending' && entry.direction !== null;

// Computes the next batch selection when the Select_All_Control is
// activated. If every displayed Batch_Eligible_Entry is currently selected,
// deselects all of them; otherwise selects all of them. Ids belonging to
// entries that are not Batch_Eligible_Entry are left untouched either way.
// Requirements 3.2, 3.3, 3.4.
export const toggleSelectAll = (entries: ScanEntry[], selectedIds: string[]): string[] => {
  const eligibleIds = entries.filter(isBatchEligible).map((entry) => entry.id);
  const allEligibleSelected = eligibleIds.length > 0 && eligibleIds.every((id) => selectedIds.includes(id));
  const preserved = selectedIds.filter((id) => !eligibleIds.includes(id));
  return allEligibleSelected ? preserved : [...new Set([...preserved, ...eligibleIds])];
};

// A confirmed unit-count entry is only accepted when it is a positive whole
// number. Requirement 2.6.
export const isValidUnitCount = (value: number): boolean =>
  Number.isInteger(value) && value >= 1;

// Partitions displayed entries into the two Queue_Direction_Tabs views.
// Stock_In_View is exactly `direction === 'stock_in'`; Stock_Out_View is the
// catchall for `direction === 'stock_out'` and `direction === null`, so a
// flagged/undirected entry always has somewhere to be reviewed. Every entry
// belongs to exactly one view — this is a total partition, not a filter that
// can drop entries. Requirements 9.1, 9.2, 9.3, 9.4.
export type QueueView = 'stock_in' | 'stock_out';

export const entryMatchesView = (direction: ScanDirection | null, view: QueueView): boolean =>
  view === 'stock_in' ? direction === 'stock_in' : direction === 'stock_out' || direction === null;

export const getEntriesForView = (entries: ScanEntry[], view: QueueView): ScanEntry[] =>
  entries.filter((entry) => entryMatchesView(entry.direction, view));

// Newest notice first. A repeated id is ignored so a replayed event does not
// stack a second card for the same lookup.
export const addProcessingNotice = (
  notices: ProcessingNotice[],
  notice: ProcessingNotice,
): ProcessingNotice[] =>
  notices.some((existing) => existing.id === notice.id) ? notices : [notice, ...notices];

export const removeProcessingNotice = (notices: ProcessingNotice[], id: string): ProcessingNotice[] =>
  notices.filter((notice) => notice.id !== id);

// Drops the oldest in-flight notice for a lookup that just became a visible
// scan. Commits and cancellations leave notices alone so approving an older
// card does not hide a lookup that is still running.
export const settleProcessingNotice = (
  notices: ProcessingNotice[],
  event: Pick<ScanEntry, 'barcode' | 'direction' | 'status'>,
): ProcessingNotice[] => {
  if (!DISPLAYABLE_SCAN_STATUSES.includes(event.status)) return notices;
  for (let index = notices.length - 1; index >= 0; index -= 1) {
    const notice = notices[index];
    if (notice.barcode === event.barcode && notice.direction === event.direction) {
      return notices.filter((_, noticeIndex) => noticeIndex !== index);
    }
  }
  return notices;
};

// Formats a review count for the queue tab badge, capping anything above 99 as
// '99+' so the badge stays a single compact token.
export const formatReviewCount = (count: number): string => (count > 99 ? '99+' : String(count));

// Applies one incoming Scan_Event to the currently displayed list: upserts it
// if its status is still displayable (pending, flagged, or a QR misread), or
// removes any entry with the same id if it isn't (committed/cancelled).
export const mergeScanEvent = (entries: ScanEntry[], event: ScanEntry): ScanEntry[] => {
  if (!DISPLAYABLE_SCAN_STATUSES.includes(event.status)) {
    return entries.filter((entry) => entry.id !== event.id);
  }
  const index = entries.findIndex((entry) => entry.id === event.id);
  if (index === -1) return [...entries, event];
  return entries.map((entry) => {
    if (entry.id !== event.id) return entry;
    // Live scan events are the stored row. The list is what attaches a group
    // hint, so a later event must not wipe a hint the list already showed.
    if (event.groupHint === undefined && entry.groupHint !== undefined) {
      return { ...event, groupHint: entry.groupHint };
    }
    return event;
  });
};

// Drops any id from a selection that no longer names a displayed entry.
// Requirement 4.4.
export const pruneSelection = (selectedIds: string[], entries: ScanEntry[]): string[] =>
  selectedIds.filter((id) => entries.some((entry) => entry.id === id));
