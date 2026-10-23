// Request paths and body shapes shared by the web client and the Expo app.
// The phone sends HTTP Basic auth instead of the browser session cookie, but
// both call these same routes.
import type { PantryLinkSuggestion, ScanDirection, ScanStatus, TargetConflictMember } from '../types';

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
export function inventoryListPath(query?: string, userId?: string): string {
  return `/api/inventory${toQueryString({ q: query, userId })}`;
}

// Commit outcomes for the web queue and the phone. Appended after the shared
// routes so both apps import this module and neither keeps a second copy.

export const NO_CLOSE_MATCH = 'No close match found.';

export const BATCH_FAILURE_FALLBACK = 'Some scans could not be confirmed.';

export const CODE_BUSY = 'busy';
export const CODE_NO_SPACE = 'no_space';

export const BUSY_SENTENCE = 'Stockbot is busy right now. Try again in a moment.';
export const NO_SPACE_SENTENCE = 'Stockbot is out of storage space, so this wasn\'t saved.';

// Codes for #395. The server sends these on the routes in docs/api/errors.md.
// auth_not_configured belongs to #393 and is not declared here. The sentences
// and extra fields are specified in docs/api/errors.md.

export const CODE_UPDATING = 'updating';
export const UPDATING_SENTENCE = 'Updating. Try again in a minute.';
export const UPDATING_STATUS = 'migrating';

export const CODE_PROVIDER_UNAVAILABLE = 'provider_unavailable';
export const PROVIDER_FIELD = 'provider';
export const PROVIDER_UNAVAILABLE_SENTENCE = "{provider} isn't responding. Try again later.";

// Names shared with the hosted contract (#364) where the meaning is the same.
// The sentence on these routes stays the one the server already returns.

export const CODE_UNAUTHORIZED = 'unauthorized';
export const SIGN_IN_REQUIRED_SENTENCE = 'Sign in required.';
export const INVALID_CREDENTIALS_SENTENCE = 'The username or password is incorrect.';
export const SYNC_TOKEN_INVALID_SENTENCE = 'The sync token is not valid.';

export const CODE_VALIDATION = 'validation';
export const MISSING_CREDENTIALS_SENTENCE = 'Enter your username and password.';
export const INVALID_BODY_PREFIX = 'invalid request body: ';
export const INVALID_PATH_PREFIX = 'invalid path parameters: ';
export const INVALID_REPORT_SENTENCE = 'invalid report';

export const CODE_CONFIRMATION_MISMATCH = 'confirmation_mismatch';

export const CODE_RATE_LIMITED = 'rate_limited';
export const RATE_LIMITED_SENTENCE = 'Too many sign-in attempts. Try again later.';

export const CODE_PAYLOAD_TOO_LARGE = 'payload_too_large';
export const BODY_TOO_LARGE_SENTENCE = 'request body is too large';
export const REPORT_TOO_LARGE_SENTENCE = 'report is too large';

export const CODE_NOT_FOUND = 'not_found';
export const NOT_FOUND_SENTENCE = 'Not found.';
export const SIGN_IN_NOT_USED_SENTENCE = 'Sign-in is not used on this address.';

export const CODE_CONFLICT = 'conflict';
export const CODE_METHOD_NOT_ALLOWED = 'method_not_allowed';
export const METHOD_NOT_ALLOWED_SENTENCE = 'Method not allowed.';

// Unexpected server failure, including a commit that already uses this code.
// The sentence is SERVER_ERROR_FALLBACK.
export const CODE_INTERNAL = 'internal';

export const CODE_SYNC_READ_ONLY = 'sync_read_only';
export const SYNC_READ_ONLY_SENTENCE = 'This token only reads the sync snapshot.';

// JSON body of an error the web or mobile app reads. `provider` is the
// display name on provider_unavailable. `status` is "migrating" on updating.
export interface ApiErrorJSON {
  error: string;
  code: string;
  provider?: string;
  status?: string;
  members?: TargetConflictMember[] | { groupId: string };
  suggestions?: PantryLinkSuggestion[];
}

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

// A lock or a full disk is the banner sentence. The card does not repeat it.
// Amber never does. An item failure stays on its card.
export function batchCardCommitNotice(code: string, message: string): string {
  if (code === CODE_NONE_ON_HAND || code === CODE_BUSY || code === CODE_NO_SPACE) return '';
  return message;
}

export function isStorageFailureCode(code: string): boolean {
  return code === CODE_BUSY || code === CODE_NO_SPACE;
}

// A confirm that finishes, batch or single, drops the lock and disk notices.
// The card just confirmed drops with them. Item-specific codes stay until that
// card is resolved. Amber is not in this set.
export function failuresAfterConfirm<T extends { code: string }>(
  notices: readonly T[],
  confirmedId: string,
): T[] {
  return notices.filter((notice) => {
    const id = 'id' in notice && typeof notice.id === 'string' ? notice.id : undefined;
    if (id === confirmedId) return false;
    return !isStorageFailureCode(notice.code);
  });
}

function storageSentence(notice: Pick<BatchFailureNotice, 'code' | 'message'>): string {
  const message = notice.message.trim();
  if (message !== '') return message;
  return notice.code === CODE_NO_SPACE ? NO_SPACE_SENTENCE : BUSY_SENTENCE;
}

// While an item failure remains, the banner is the fallback, whether or not
// those codes match. The item sentence stays on the card. A lock or a full
// disk is the banner only when no item failure is left, with the confirmed
// count when some scans saved.
export function batchFailureBannerText(
  notices: readonly Pick<BatchFailureNotice, 'code' | 'message'>[],
  committedCount: number,
): string | null {
  const blocking = notices.filter((notice) => notice.code !== CODE_NONE_ON_HAND);
  if (blocking.length === 0) return null;
  if (blocking.some((notice) => !isStorageFailureCode(notice.code))) return BATCH_FAILURE_FALLBACK;
  const storage = blocking.find((notice) => isStorageFailureCode(notice.code));
  if (storage === undefined) return null;
  const sentence = storageSentence(storage);
  const count = Number.isInteger(committedCount) && committedCount > 0 ? committedCount : 0;
  if (count === 0) return sentence;
  return `${count} confirmed. ${sentence}`;
}

// A listed count of zero is nothing on hand. Absence is not: the server decides shelved and unknown products.
export function shelfHasNothingOnHand(onHand: number | undefined): boolean {
  return onHand !== undefined && onHand <= 0;
}

export function nothingOnHandPill(count: number): string | null {
  if (!Number.isInteger(count) || count < 1) return null;
  return `${count} with nothing on hand`;
}

// The count is the digit, including 1.
export function confirmSkipsNothingOnHand(count: number): string | null {
  if (!Number.isInteger(count) || count < 1) return null;
  return `Confirm skips the ${count} with nothing on hand.`;
}
