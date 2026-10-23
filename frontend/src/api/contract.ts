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

// Shown when fetch never received a response. Straight apostrophe, one sentence.
export const OFFLINE_SENTENCE = "Can't reach Stockbot. Check your connection and try again.";

// TODO: switch to CODE_AUTH_NOT_CONFIGURED once #393 lands in this module.
const AUTH_NOT_CONFIGURED_CODE = 'auth_not_configured';

export const AUTH_NOT_CONFIGURED_SENTENCE = "Sign-in isn't set up yet.";

// A proxy or tunnel can answer with one of these and no Stockbot code.
const GATEWAY_STATUSES = new Set([502, 503, 504, 530]);

export interface NetworkErrorResult {
  kind: 'network';
  message: typeof OFFLINE_SENTENCE;
}

// The typed failure both clients throw. message is OFFLINE_SENTENCE.
export class UnreachableError extends Error {
  readonly kind = 'network' as const;
  readonly result: NetworkErrorResult;

  constructor() {
    super(OFFLINE_SENTENCE);
    this.name = 'UnreachableError';
    this.result = { kind: 'network', message: OFFLINE_SENTENCE };
  }
}

// Browser and device names for a request that ended before any HTTP response.
// TypeError is not in this set: fetch uses it for transport failures, and so does
// an invalid header raised before anything is sent. Those are told apart by message.
const TRANSPORT_NAMES = new Set(['AbortError', 'TimeoutError', 'NetworkError']);

// The raw strings those runtimes put on the error. Matched only here.
const TRANSPORT_MESSAGES = new Set([
  'Failed to fetch',
  'Network request failed',
  'Load failed',
  'NetworkError when attempting to fetch resource.',
  'The Internet connection appears to be offline.',
  'The operation was aborted.',
  'The user aborted a request.',
  'Aborted',
]);

const TRANSPORT_CODES = new Set([
  'ECONNREFUSED',
  'ECONNRESET',
  'ECONNABORTED',
  'ENOTFOUND',
  'ETIMEDOUT',
  'EAI_AGAIN',
  'EHOSTUNREACH',
  'ENETUNREACH',
  'EPIPE',
  'UND_ERR_CONNECT_TIMEOUT',
  'UND_ERR_HEADERS_TIMEOUT',
  'UND_ERR_BODY_TIMEOUT',
  'UND_ERR_SOCKET',
  'ABORT_ERR',
]);

function stringField(error: object, key: string): string {
  if (!(key in error)) return '';
  const value = (error as Record<string, unknown>)[key];
  return typeof value === 'string' ? value : '';
}

// True for a fetch transport failure, an abort or timeout, or a DNS / connection failure.
function isTransportFailure(error: unknown): boolean {
  if (typeof DOMException !== 'undefined' && error instanceof DOMException) {
    return TRANSPORT_NAMES.has(error.name);
  }
  if (typeof error !== 'object' || error === null) return false;
  if (TRANSPORT_NAMES.has(stringField(error, 'name'))) return true;
  if (TRANSPORT_CODES.has(stringField(error, 'code'))) return true;
  if (TRANSPORT_MESSAGES.has(stringField(error, 'message'))) return true;
  if ('cause' in error && error.cause !== undefined && error.cause !== error) {
    return isTransportFailure(error.cause);
  }
  return false;
}

export function networkErrorResult(error: unknown): NetworkErrorResult | null {
  if (error instanceof UnreachableError) return error.result;
  if (!isTransportFailure(error)) return null;
  return { kind: 'network', message: OFFLINE_SENTENCE };
}

// A server-state notice other than offline. The banner owns the sentence.
export class ServerNoticeError extends Error {
  readonly code: string;

  constructor(code: string, sentence: string) {
    super(sentence);
    this.name = 'ServerNoticeError';
    this.code = code;
  }
}

// Card and inline alerts stay quiet when the banner owns the failure.
// Any other error keeps its message, or the caller's fallback.
export function inlineFailure(error: unknown, fallback: string): string | null {
  if (error instanceof UnreachableError || error instanceof ServerNoticeError) return null;
  if (error instanceof Error) return error.message;
  return fallback;
}

export function stockbotCode(body: unknown): string {
  if (typeof body !== 'object' || body === null || !('code' in body)) return '';
  const code = (body as { code?: unknown }).code;
  return typeof code === 'string' ? code.trim() : '';
}

export type HttpDisposition = 'offline' | 'auth' | 'stockbot';

// A Stockbot response, including an error, clears offline. A 502, 503, 504, or
// 530 with no code did not come from Stockbot, so it is offline. auth_not_configured
// is its own banner. busy and no_space stay with the caller and clear only on success.
export function applyHttpResponse(status: number, code: string): HttpDisposition {
  const trimmed = code.trim();
  if (GATEWAY_STATUSES.has(status) && trimmed === '') {
    raiseOfflineBanner();
    return 'offline';
  }
  clearOfflineBanner();
  if (trimmed === AUTH_NOT_CONFIGURED_CODE) {
    raiseAuthNotConfiguredBanner();
    return 'auth';
  }
  return 'stockbot';
}

export function unreachableFrom(error: unknown): UnreachableError | null {
  if (error instanceof UnreachableError) return error;
  if (networkErrorResult(error) === null) return null;
  return new UnreachableError();
}

const apiSuccessListeners = new Set<() => void>();

// Both clients call this after a response arrives and is read. Screens subscribe
// so the offline sentence drops on the next success, including a load or refresh.
export function subscribeApiSuccess(listener: () => void): () => void {
  apiSuccessListeners.add(listener);
  return () => {
    apiSuccessListeners.delete(listener);
  };
}

export function notifyApiSuccess(): void {
  if (noticeKind !== '') publishNotice('');
  for (const listener of apiSuccessListeners) listener();
}

// One shared banner. Offline replaces any other notice. A Stockbot response
// clears only offline. Success clears the whole slot, including auth.
type NoticeKind = '' | 'offline' | 'auth';

let noticeKind: NoticeKind = '';
const offlineBannerListeners = new Set<() => void>();

function publishNotice(next: NoticeKind): void {
  if (noticeKind === next) return;
  noticeKind = next;
  for (const listener of offlineBannerListeners) listener();
}

export function serverNoticeText(): string {
  if (noticeKind === 'offline') return OFFLINE_SENTENCE;
  if (noticeKind === 'auth') return AUTH_NOT_CONFIGURED_SENTENCE;
  return '';
}

export function offlineBannerActive(): boolean {
  return noticeKind === 'offline';
}

// Sets the offline sentence. A later Stockbot response clears it, errors included.
// notifyApiSuccess clears it after a response is read successfully.
export function raiseOfflineBanner(): void {
  publishNotice('offline');
}

export function raiseAuthNotConfiguredBanner(): void {
  publishNotice('auth');
}

// A body that cannot be read, or a success that is not JSON, is not a Stockbot
// response. Throw this instead of the raw SyntaxError or TypeError.
export function unreadableResponse(error: unknown): UnreachableError {
  raiseOfflineBanner();
  return unreachableFrom(error) ?? new UnreachableError();
}

export function clearOfflineBanner(): void {
  if (noticeKind === 'offline') publishNotice('');
}

export function subscribeOfflineBanner(listener: () => void): () => void {
  offlineBannerListeners.add(listener);
  return () => {
    offlineBannerListeners.delete(listener);
  };
}
