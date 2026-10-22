// Request paths and body shapes shared by the web client and the Expo app.
// The phone sends HTTP Basic auth instead of the browser session cookie, but
// both call these same routes.
import type { ScanDirection, ScanStatus } from '../types';

export interface HouseholdSession {
  required: boolean;
  username: string;
  error: string;
}

export interface CreateScanEntryInput {
  barcode: string;
  direction?: ScanDirection;
  unitCount?: number;
  expiresAt?: string;
  userId: string;
}

export interface BatchCommitInput {
  scanEntryIds: string[];
  direction?: ScanDirection;
  unitCount?: number;
  expiresAt?: string;
  commit: boolean;
}

export interface StockOutCount {
  removedCount: number;
  requestedCount: number;
}

export interface BatchCommitResponse {
  updatedCount: number;
  committedIds?: string[];
  failedIds?: string[];
  failedReasons?: string[];
  failedCodes?: string[];
  // One entry per committed stock-out, including a full removal. Omitted when
  // the batch commits no stock-out. Stock-ins and failed cards are absent.
  stockOutCounts?: Record<string, StockOutCount>;
}

export interface SetScannerModeResponse {
  mode: ScanDirection;
}

// Headless scans and the browser queue both use this household user unless
// HEADLESS_USER_ID is changed on the server. Session does not return it.
export const DEFAULT_SCAN_USER_ID = 'user-1';

export const SESSION_PATH = '/api/session';
export const SCANS_PATH = '/api/scans';
export const BATCH_COMMIT_PATH = '/api/scans/batch-commit';
export const SCANNER_CONFIG_PATH = '/api/scanner/config';
export const SCANNER_MODE_PATH = '/api/scanner/mode';

export function scansQuery(userId: string, status?: ScanStatus): string {
  return `${SCANS_PATH}${toQueryString({ userId, status })}`;
}

export function scanCommitPath(id: string): string {
  return `${SCANS_PATH}/${id}/commit`;
}

export function toQueryString(params: Record<string, string | undefined>): string {
  const entries = Object.entries(params).filter(
    (entry): entry is [string, string] => entry[1] !== undefined,
  );
  if (entries.length === 0) return '';
  return `?${new URLSearchParams(entries).toString()}`;
}

// Household items, including rows at zero. The inventory page and the link
// card both use this. It is not the product catalog.
export function inventoryListPath(query?: string): string {
  return `/api/inventory${toQueryString({ q: query })}`;
}

// Commit outcomes for the web queue and the phone. Appended after the shared
// routes so both apps import this module and neither keeps a second copy.

export const NO_CLOSE_MATCH = 'No close match found.';

export const BATCH_FAILURE_FALLBACK = 'Some scans could not be confirmed.';

// A 5xx response with no JSON error field. A body that includes error still wins.
export const SERVER_ERROR_FALLBACK = 'Something went wrong. Try again.';

// Both counts ride on every successful stock-out commit. Stock-in omits them.
export const REMOVED_COUNT_FIELD = 'removedCount';
export const REQUESTED_COUNT_FIELD = 'requestedCount';

export interface CommitScanResponse {
  removedCount?: number;
  requestedCount?: number;
}

// Appended beside the outcome helpers. A rename of either code or the batch
// key is this one edit; older batch bodies that omit the key keep working.
export const FAILED_CODES_FIELD = 'failedCodes';
export const CODE_PRODUCT_UNRESOLVED = 'product_unresolved';
export const CODE_LINK_TARGET_GONE = 'link_target_gone';

export interface CommitConflictBehavior {
  linkOffer: boolean;
  refreshInstances: boolean;
  refreshSuggestions: boolean;
  showMessage: boolean;
  hideEmptyMatch: boolean;
}

// Branch on the server's code, whatever the HTTP status was. An absent or
// unknown code shows the server text and leaves the pick lists alone.
export function commitConflictBehavior(code: string | undefined): CommitConflictBehavior {
  switch (code) {
    case 'instance_gone':
      return { linkOffer: false, refreshInstances: true, refreshSuggestions: false, showMessage: true, hideEmptyMatch: false };
    case 'no_item':
      return { linkOffer: true, refreshInstances: false, refreshSuggestions: false, showMessage: false, hideEmptyMatch: false };
    case CODE_PRODUCT_UNRESOLVED:
      return { linkOffer: true, refreshInstances: false, refreshSuggestions: false, showMessage: true, hideEmptyMatch: true };
    case CODE_LINK_TARGET_GONE:
      return { linkOffer: false, refreshInstances: false, refreshSuggestions: true, showMessage: true, hideEmptyMatch: false };
    default:
      return { linkOffer: false, refreshInstances: false, refreshSuggestions: false, showMessage: true, hideEmptyMatch: false };
  }
}

export interface BatchFailureNotice {
  id: string;
  message: string;
  code: string;
}

// Pair each failed id with the server's reason string. A missing or non-string
// reason stays blank; the queue banner is the one fallback sentence. A missing
// or short code list leaves that entry on the reason alone.
export function batchFailureNotices(response: {
  failedIds?: string[];
  failedReasons?: string[];
}): BatchFailureNotice[] {
  const ids = response.failedIds ?? [];
  const reasons = response.failedReasons ?? [];
  const rawCodes = (response as Record<string, unknown>)[FAILED_CODES_FIELD];
  const codes = Array.isArray(rawCodes) ? rawCodes : [];
  return ids.map((id, index) => {
    const reason = reasons[index];
    const code = codes[index];
    return {
      id,
      message: typeof reason === 'string' ? reason : '',
      code: typeof code === 'string' ? code : '',
    };
  });
}

function countField(value: unknown): number | null {
  if (typeof value !== 'number' || !Number.isInteger(value)) return null;
  return value;
}

// A batch names the one short card. Several short cards, or one short card
// with no name, use the count sentence. One card stays singular. Equal counts
// and a missing map say nothing.
function batchPartialLine(
  counts: Record<string, StockOutCount>,
  names: Readonly<Record<string, string>> | undefined,
): string | null {
  const partials: { removed: number; requested: number; name: string | null }[] = [];
  for (const [id, value] of Object.entries(counts)) {
    if (value === null || typeof value !== 'object') continue;
    const entry = value as unknown as Record<string, unknown>;
    const removed = countField(entry[REMOVED_COUNT_FIELD]);
    const requested = countField(entry[REQUESTED_COUNT_FIELD]);
    if (removed === null || requested === null) continue;
    if (removed < 1 || removed >= requested) continue;
    const rawName = names?.[id];
    partials.push({
      removed,
      requested,
      name: rawName !== undefined && rawName !== '' ? rawName : null,
    });
  }
  if (partials.length === 0) return null;
  const only = partials.length === 1 ? partials[0] : undefined;
  if (only !== undefined && only.name !== null) {
    const verb = only.removed === 1 ? 'was' : 'were';
    return `Only ${only.removed} of ${only.requested} ${only.name} ${verb} on hand, so ${only.removed} ${verb} taken out.`;
  }
  const noun = partials.length === 1 ? 'card' : 'cards';
  return `${partials.length} ${noun} had fewer on hand than scanned, so only what was on hand was taken out.`;
}

// Fewer units left than the card asked for. A single confirm keeps the short
// sentence. A batch with stockOutCounts adds the item, or a count of cards.
export function formatPartialRemoval(
  body: CommitScanResponse | BatchCommitResponse | null | undefined,
  names?: Readonly<Record<string, string>>,
): string | null {
  if (body === null || body === undefined) return null;
  if ('stockOutCounts' in body && body.stockOutCounts !== undefined) {
    return batchPartialLine(body.stockOutCounts, names);
  }
  const record = body as Record<string, unknown>;
  const removedCount = countField(record[REMOVED_COUNT_FIELD]);
  const requestedCount = countField(record[REQUESTED_COUNT_FIELD]);
  if (removedCount === null || requestedCount === null) return null;
  if (removedCount < 1 || removedCount >= requestedCount) return null;
  if (removedCount === 1) return 'Only 1 was on hand, so 1 was taken out.';
  return `Only ${removedCount} were on hand, so ${removedCount} were taken out.`;
}

export const NOTHING_ON_HAND_TITLE = 'Nothing on hand to take out.';

export const NOTHING_ON_HAND_BODY =
  "Stockbot thinks you have none, so this can't be confirmed yet. Stock it in first, or remove this scan.";

export const CODE_NONE_ON_HAND = 'none_on_hand';

// Shelved rows are absent from the household list, so a missing count is empty.
export function shelfHasNothingOnHand(onHand: number | undefined): boolean {
  return onHand === undefined || onHand <= 0;
}

export function nothingOnHandPill(count: number): string | null {
  if (!Number.isInteger(count) || count < 1) return null;
  return `${count} with nothing on hand`;
}

// "the 1" does not read as a sentence. One card says "one"; two or more use the digit.
export function confirmSkipsNothingOnHand(count: number): string | null {
  if (!Number.isInteger(count) || count < 1) return null;
  const amount = count === 1 ? 'one' : String(count);
  return `Confirm skips the ${amount} with nothing on hand.`;
}
