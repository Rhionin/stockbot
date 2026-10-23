// Request paths and body shapes shared by the web client and the Expo app.
// The phone sends HTTP Basic auth instead of the browser session cookie, but
// both call these same routes.
import type { InventoryItem, Item, PantryLinkSuggestion, ProductSummary, ScanDirection, ScanGroupHint, ScanStatus, ShoppingListEntry, TargetConflictMember } from '../types';

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
export const AUTH_SESSION_PATH = '/api/auth/session';
/** Default `SESSION_COOKIE_NAME`. Host-only, Secure, so it is not set on plain-HTTP LAN. */
export const SESSION_COOKIE_NAME = '__Host-stockbot_session';
export const CLIENT_HEADER = 'X-Stockbot-Client';
export const EXPO_CLIENT_PREFIX = 'expo/';
export const UPGRADE_REQUIRED_SENTENCE = 'Update Stockbot to keep using it.';
export const EVENTS_PATH = '/api/events';
export const LIVE_HINT_EVENT = 'hint';
export const CONFIG_PATH = '/api/config';

export function expoClientHeader(version: string): string {
  return `${EXPO_CLIENT_PREFIX}${version}`;
}
export const SCANS_PATH = '/api/scans';

// One row of GET /api/scans. onHand is the current shelf count for a linked
// stock-out. A shelved item and a live item with nothing left are 0, and a
// removed unit is not counted. The field is omitted when the product is
// unresolved, this household has no item, or the scan is not a stock-out.
// Absence is not an empty shelf. product and groupHint are omitted the same way.
export interface ScanListEntry {
  id: string;
  userId: string;
  barcode: string;
  scannedAt: string;
  direction: ScanDirection | null;
  unitCount: number;
  expiresAt: string | null;
  status: ScanStatus;
  productId: string | null;
  product?: ProductSummary;
  groupHint?: ScanGroupHint;
  committedAt: string | null;
  createdAt: string;
  onHand?: number;
}
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
export const CODE_AUTH_NOT_CONFIGURED = 'auth_not_configured';

export const BUSY_SENTENCE = 'Stockbot is busy right now. Try again in a moment.';
export const NO_SPACE_SENTENCE = 'Stockbot is out of storage space, so this wasn\'t saved.';

// Codes for #395. The server sends these on the routes in docs/api/errors.md.
// auth_not_configured is CODE_AUTH_NOT_CONFIGURED above. The sentences
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

// Shown when fetch never received a response. Straight apostrophe, one sentence.
export const OFFLINE_SENTENCE = "Can't reach Stockbot. Check your connection and try again.";

export const AUTH_NOT_CONFIGURED_SENTENCE = "Sign-in isn't set up yet.";

export const AUTH_SETUP_SENTENCE = "Whoever runs this server needs to finish setting it up.";

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

// A Stockbot response, including an error, clears offline and the sign-in banner.
// A 502, 503, 504, or 530 with no code did not come from Stockbot, so it is offline.
// auth_not_configured is its own banner. busy and no_space stay with the caller
// and clear only on success.
export function applyHttpResponse(status: number, code: string): HttpDisposition {
  const trimmed = code.trim();
  if (GATEWAY_STATUSES.has(status) && trimmed === '') {
    raiseOfflineBanner();
    return 'offline';
  }
  if (trimmed === CODE_AUTH_NOT_CONFIGURED) {
    raiseAuthNotConfiguredBanner();
    return 'auth';
  }
  clearServerNotice();
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

// One shared banner. Offline replaces any other notice. auth_not_configured
// replaces offline. Any other Stockbot response clears the slot. Success does too.
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

export function clearServerNotice(): void {
  if (noticeKind !== '') publishNotice('');
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

// Hosted multi-household contract (docs/design/api-contract.md).
// Types only. The Pi client still uses the helpers above, including userId,
// until the hosted routes exist. Nothing here selects a tenant except the
// X-Household-Id header, which the server checks against membership, or, for
// device calls, the household bound to the device token. The one exception is
// the householdId body on POST /api/auth/session/household, which sets the
// session's active household after a membership check.

export type HouseholdHeaderName = 'X-Household-Id';

export type HouseholdRole = 'owner' | 'member';

export type HostedErrorCode =
  | 'busy'
  | 'no_space'
  | 'unauthorized'
  | 'forbidden_role'
  | 'not_found'
  | 'conflict'
  | 'invite_expired'
  | 'invite_required'
  | 'reset_expired'
  | 'account_exists'
  | 'validation'
  | 'not_pending'
  | 'product_unresolved'
  | 'none_on_hand'
  | 'instance_gone'
  | 'no_item'
  | 'link_target_gone'
  | 'scan_not_found'
  | 'direction_unset'
  | 'internal'
  | 'last_owner'
  | 'household_not_empty'
  | 'confirmation_mismatch'
  | 'rate_limited'
  | 'payload_too_large'
  | 'device_revoked'
  | 'upgrade_required'
  ;

export interface HostedErrorBody {
  error: string;
  code: HostedErrorCode;
  /** Present on `validation` when one input should show the message. */
  field?: string;
  members?: unknown;
  suggestions?: unknown;
}

export type SessionKind = 'signed_out' | 'account' | 'household_password' | 'lan';

export interface SessionHousehold {
  householdId: string;
  name: string;
  role: HouseholdRole;
  /** Set while the household is inside the restore window. */
  deletedAt: string | null;
}

export interface AuthSession {
  /** Null when nobody is signed in, and on a household-password or LAN session. */
  user: { id: string; email: string; name: string } | null;
  kind: SessionKind;
  /** #364 list. `householdId` is the membership id. */
  memberships: SessionHousehold[];
  /** Switcher list. Same rows as `memberships`, with `id` instead of `householdId`. */
  households: { id: string; name: string; role: HouseholdRole; deletedAt: string | null }[];
  activeHouseholdId: string | null;
  operator: boolean;
}

export interface LoginRequest {
  email?: string;
  password?: string;
  /** Only while household 1's shared password is on. */
  householdPassword?: string;
}

/** Web sets the session cookie. Expo also receives `token` (`sbs_…`). */
export interface LoginResponse extends AuthSession {
  token?: string;
}

export interface SwitchHouseholdRequest {
  householdId: string;
}

export interface ResetPasswordRequest {
  code: string;
  password: string;
}

export interface ResetLinkCreated {
  url: string;
  expiresAt: string;
}

export interface Household {
  id: string;
  name: string;
  role: HouseholdRole;
  deletedAt: string | null;
  purgeAt: string | null;
}

export interface HouseholdDeleted extends Household {
  message: 'Your household will be permanently deleted after 30 days. Backup copies are cleared within two weeks after that.';
}

export interface CreateHouseholdResponse {
  id: string;
  name: string;
  role: 'owner';
}

export interface RenameHouseholdRequest {
  name: string;
}

export interface DeleteHouseholdRequest {
  confirmation: string;
}

export interface HouseholdListResponse {
  households: AuthSession['memberships'];
}

export interface Member {
  userId: string;
  email: string;
  role: HouseholdRole;
}

export interface MemberListResponse {
  members: Member[];
}

export interface ChangeRoleRequest {
  role: HouseholdRole;
}

export interface InviteCreateRequest {
  email?: string;
  /** Stored at create. Omit for member. Only an owner may send owner. */
  role?: 'member' | 'owner';
}

export interface InviteCreated {
  id: string;
  code: string;
  /** Server-built `<APP_BASE_URL>/invite/<code>`. APP_BASE_URL is per-stage config. */
  url: string;
  email: string | null;
  /** The role stored at create. Accept echoes this and cannot change it. */
  role: 'member' | 'owner';
  expiresAt: string;
}

export interface InviteSummary {
  id: string;
  email: string | null;
  role: 'member' | 'owner';
  createdBy: string;
  expiresAt: string;
  status: 'open' | 'revoked' | 'used' | 'expired';
}

export interface InviteListResponse {
  invites: InviteSummary[];
}

/** No `role` field. The role was stored when the invite was created. */
export interface AcceptInviteRequest {
  code: string;
  account?: { name: string; email: string; password: string };
  household?: { name: string; restockMode?: RestockMode };
}

/** LoginResponse plus the #364 accept fields. `token` is set only when `account` was sent. */
export interface AcceptInviteResponse extends LoginResponse {
  householdId: string;
  name: string;
  /** Echo of the role stored on the invite. Accept cannot choose or raise it. */
  role: 'member' | 'owner';
}

export interface InvitePreview {
  kind: 'start_household' | 'join_household';
  /** Echo of the role stored at create. */
  role: 'member' | 'owner';
  householdName?: string;
  invitedBy?: string;
  expiresAt: string;
}

export interface PairingCode {
  code: string;
  qrPayload: string;
  expiresAt: string;
  /** Poll id for GET /api/devices/pair/{pairingId}. */
  pairingId: string;
  /** Same text as `code`, drawn as Code 128, and the typed-code fallback. */
  barcodeValue: string;
  /** Same text as `qrPayload`, drawn as QR. */
  qrValue: string;
}

export interface PairingLookup {
  pairingId: string;
  status: 'pending' | 'complete' | 'expired';
  expiresAt: string;
  deviceId?: string;
}

export interface CompletePairingRequest {
  code: string;
  label?: string;
  hub?: { kind: 'pi_hub' | 'box_hub'; version: string; bootId: string };
}

export interface DeviceHubConfig {
  controlBarcodes: { stockIn: string; stockOut: string };
  idleSeconds: number;
  configVersion: number;
}

export interface CompletePairingResponse {
  deviceId: string;
  token: string;
  config?: DeviceHubConfig;
  heartbeatSeconds?: 60;
}

export type DeviceKind = 'pi_hub' | 'box_hub' | 'direct';

export interface Device {
  id: string;
  label: string | null;
  createdAt: string;
  /** In-memory last contact. The database row is written on a status flip or every 5 minutes. */
  lastSeenAt: string | null;
  revokedAt: string | null;
  kind: DeviceKind;
  /** Server-computed from the in-memory lastSeenAt: within 300 seconds. */
  online: boolean;
}

export interface RenameDeviceRequest {
  label: string;
}

export interface DeviceListResponse {
  devices: Device[];
}

export interface DeviceScanRequest {
  id: string;
  barcode?: string;
  scannedAt?: string;
  direction?: ScanDirection;
  unitCount?: number;
  heartbeat?: boolean;
}

export interface DeviceScanResponse {
  id: string;
  /** `accepted` is the single-body form of a batch item's `created`. */
  status: 'accepted' | 'duplicate';
  cursor: string;
}

/** Stage 1 batch on POST /api/device/scans. A body is a batch when it has `scans`. Both `scans` and `id` is validation. */
export interface DeviceScanBatchRequest {
  scans: {
    id: string;
    barcode?: string;
    scannedAt?: string;
    direction?: ScanDirection;
    unitCount?: number;
    ageMs?: number;
    bootId?: string;
  }[];
  droppedCount?: number;
}

export interface DeviceScanBatchResponse {
  results: {
    id: string;
    status: 'created' | 'duplicate' | 'conflict' | 'rejected';
    /** Present only when status is `rejected`. */
    code?: 'validation';
  }[];
  config: DeviceHubConfig;
}

export interface DeviceHeartbeatRequest {
  version: string;
  bootId: string;
  uptimeS: number;
  queued: number;
  droppedCount?: number;
  clockSynced: boolean;
}

export interface DeviceHeartbeatResponse {
  serverTime: string;
  config: DeviceHubConfig;
  heartbeatSeconds: 60;
  rotateToken: boolean;
  update?: { version: string; url: string; sha256: string; sig: string };
}

export interface RotatedDeviceToken {
  token: string;
}

export interface HostedCreateScanRequest {
  /** Client UUID. Deduped on (household, user, id). A replay returns the original entry. */
  id?: string;
  barcode: string;
  direction?: ScanDirection;
  unitCount?: number;
  expiresAt?: string;
}

export interface HostedScanEntry {
  id: string;
  barcode: string;
  scannedAt: string;
  direction: ScanDirection | null;
  unitCount: number;
  expiresAt: string | null;
  status: ScanStatus;
  productId: string | null;
  product: ProductSummary | null;
  groupHint?: { groupId?: string; name: string };
  committedAt: string | null;
  createdAt: string;
}

export type ScanChanges =
  | { cursor: string; changed: false }
  | { cursor: string; changed: true; scans: HostedScanEntry[] };

/** Committed and cancelled history. `since` does not apply. `nextBefore` is the next `before` query. */
export interface ScanHistoryPage {
  scans: HostedScanEntry[];
  nextBefore: string | null;
}

export interface CommitScanRequest {
  instanceId?: string;
  linkProductId?: string;
}

export interface HostedInventoryItem extends Omit<InventoryItem, 'item'> {
  item: Omit<Item, 'userId'>;
}

export type InventoryChanges =
  | { cursor: string; changed: false }
  | { cursor: string; changed: true; items: HostedInventoryItem[] };

export type ShoppingListChanges =
  | { cursor: string; changed: false }
  | { cursor: string; changed: true; items: ShoppingListEntry[] };

export type RestockMode = 'auto' | 'ask_restock' | 'ask_hold';

export interface CreateHouseholdRequest {
  name: string;
  restockMode?: RestockMode;
  /** Stage 1 requires a start_household code. Later stages may omit it. */
  inviteCode?: string;
}

/** GET and PATCH /api/household/settings. State only: never the password or its hash. */
export interface HouseholdSettings {
  restockMode: RestockMode;
  householdPasswordEnabled: boolean;
  /** RFC3339, only while a retired password can be restored. */
  householdPasswordRestoreUntil?: string;
}

export interface ExportProductV1 {
  /** Source id inside the file. Import does not insert this id into the shared catalog. */
  id: string;
  name: string;
  category: string;
  unitOfMeasure: string;
  barcodes: string[];
}

export interface ExportBarcodeOverrideV1 {
  barcode: string;
  productId: string;
}

export interface ExportItemV1 {
  id: string;
  productId: string;
  category: string | null;
  group: string | null;
  /** Spreadsheet column "stock target" on items.csv. */
  stockTarget: number | null;
  createdAt: string;
}

export interface ExportItemInstanceV1 {
  id: string;
  itemId: string;
  stockInAt: string;
  expiresAt: string | null;
  removedAt: string | null;
  removalReason: 'consumed' | 'expired' | 'manual' | null;
  createdAt: string;
}

export interface ExportShoppingListItemV1 {
  id: string;
  itemId: string;
  quantity: number;
  source: 'manual' | 'auto';
  purchasedAt: string | null;
  createdAt: string;
}

export interface ExportStockMoveV1 {
  id: string;
  itemId: string;
  productId: string;
  direction: 'in' | 'out';
  quantity: number;
  at: string;
  source: 'scan' | 'manual';
  scanEntryId: string | null;
  createdAt: string;
}

export interface ExportGroupV1 {
  id: string;
  name: string;
  productIds: string[];
}

export interface ExportCategoryV1 {
  id: string;
  name: string;
  itemIds: string[];
}

/** Allowlist. channel_key is never a field. */
export interface ExportSettingsV1 {
  restockMode?: RestockMode;
  supplyDefault?: string;
}

/** Flat scan row. productId is the file's product id and is remapped on import. */
export interface ExportScanEntryV1 {
  id: string;
  barcode: string;
  scannedAt: string;
  direction: ScanDirection | null;
  unitCount: number;
  expiresAt: string | null;
  status: ScanStatus;
  productId: string | null;
  committedAt: string | null;
  createdAt: string;
}

export interface ExportDocumentV1 {
  schemaVersion: 1;
  importId: string;
  exportedAt: string;
  source: 'hosted' | 'pi';
  householdName: string;
  /** Optional. Absent on a Pi snapshot means the Pi user user-1. */
  legacyUserId?: string;
  products: ExportProductV1[];
  barcodeOverrides: ExportBarcodeOverrideV1[];
  items: ExportItemV1[];
  itemInstances: ExportItemInstanceV1[];
  scanEntries: ExportScanEntryV1[];
  shoppingListItems: ExportShoppingListItemV1[];
  stockMoves: ExportStockMoveV1[];
  groups: ExportGroupV1[];
  categories: ExportCategoryV1[];
  settings: ExportSettingsV1;
}

export interface ExportJobCreated {
  jobId: string;
  /** Pi returns `ready`. Stage 3 returns `pending`. */
  status: 'pending' | 'ready';
}

export interface ExportJob {
  jobId: string;
  status: 'pending' | 'ready' | 'failed';
  jsonUrl: string | null;
  csvUrl: string | null;
  expiresAt: string | null;
}

export interface ImportUploadRequest {
  contentLength: number;
}

export interface ImportUploadCreated {
  uploadUrl: string;
  key: string;
  maxBytes: 33554432;
}

export interface StartImportRequest {
  importId: string;
  key: string;
}

export interface ImportJobCreated {
  jobId: string;
  importId: string;
  status: 'pending';
}

export type ImportJobStatus = 'pending' | 'running' | 'ready' | 'failed';

export interface ImportJob {
  jobId: string;
  importId: string;
  status: ImportJobStatus;
  warnings: string[];
}

export interface DeleteAccountRequest {
  confirmation: string;
}

export type LiveTransport = 'sse' | 'poll' | 'push';

export type LiveConfig =
  | {
      live: 'sse' | 'poll';
      minClientVersion: { expo: string };
      householdPasswordSignIn: boolean;
      appBaseUrl?: string;
      entry?: 'lan' | 'public';
    }
  | {
      live: 'push';
      appsync: { httpUrl: string; realtimeUrl: string };
      minClientVersion: { expo: string };
      householdPasswordSignIn: boolean;
      appBaseUrl?: string;
      entry?: 'public';
    };

export interface LiveTicket {
  ticket: string;
  channel: `/hh/${string}/${string}`;
  expiresAt: string;
}

export type LiveHintType = 'scans' | 'stock' | 'list' | 'rekey' | 'devices';

export type LiveHint =
  | { type: 'scans' | 'stock' | 'list'; cursor: string }
  | { type: 'rekey' }
  | { type: 'devices' };

export interface LiveFallbackRules {
  scanQueuePollMs: 2000;
  otherVisiblePollMs: 10000;
  connectionTimeoutMs: 300000;
  ticketMaxTtlSec: 900;
}

/** JSON from the Pi's GET /api/sync/snapshot. Synthetic rows live in fixtures/pi-sync-snapshot.json. */
export interface PiSyncSnapshot {
  generatedAt: string;
  products: unknown[];
  groups: unknown[];
  instances: unknown[];
  removals: unknown[];
  moves: unknown[];
  shoppingList: unknown[];
  legacyUserId?: string;
}

export interface ImportClassification {
  ok: boolean;
  kind: 'hosted' | 'pi';
  warnings: string[];
}

const hostedImportKeys = new Set([
  'schemaVersion',
  'importId',
  'exportedAt',
  'source',
  'householdName',
  'legacyUserId',
  'products',
  'barcodeOverrides',
  'items',
  'itemInstances',
  'scanEntries',
  'shoppingListItems',
  'stockMoves',
  'groups',
  'categories',
  'settings',
]);

const piImportKeys = new Set([
  'generatedAt',
  'products',
  'groups',
  'instances',
  'removals',
  'moves',
  'shoppingList',
  'legacyUserId',
]);

const piArrays = ['products', 'groups', 'instances', 'removals', 'moves', 'shoppingList'] as const;

// Unknown top-level keys are warnings. A missing legacyUserId is valid.
// A schemaVersion other than 1 is not. A missing schemaVersion is the Pi snapshot.
export function classifyImportDocument(value: unknown): ImportClassification {
  if (value === null || typeof value !== 'object' || Array.isArray(value)) {
    return { ok: false, kind: 'pi', warnings: [] };
  }
  const doc = value as Record<string, unknown>;
  const keys = Object.keys(doc);
  if ('schemaVersion' in doc) {
    if (doc.schemaVersion !== 1) return { ok: false, kind: 'hosted', warnings: [] };
    return {
      ok: typeof doc.importId === 'string' && doc.importId !== '',
      kind: 'hosted',
      warnings: keys.filter((key) => !hostedImportKeys.has(key)).sort(),
    };
  }
  const arrays = piArrays.every((key) => Array.isArray(doc[key]));
  const legacy = doc.legacyUserId;
  const legacyOk = legacy === undefined || typeof legacy === 'string';
  return {
    ok: arrays && typeof doc.generatedAt === 'string' && legacyOk,
    kind: 'pi',
    warnings: keys.filter((key) => !piImportKeys.has(key)).sort(),
  };
}

/** Crockford base32 payload alphabet. Check symbols add `*~$=U` (mod 37). O, I, and L are aliases, not payload characters. */
const PAIRING_PAYLOAD = '0123456789ABCDEFGHJKMNPQRSTVWXYZ';
const PAIRING_CHECK = `${PAIRING_PAYLOAD}*~$=U`;

/** SBP-XXXX-XXXX-C. Anchors reject a prefix or a trailing suffix. Letters and hyphens, so it cannot be a digits-only barcode. */
export const PAIRING_CODE_GRAMMAR =
  /^SBP-[0-9A-HJKMNP-TV-Z]{4}-[0-9A-HJKMNP-TV-Z]{4}-[0-9A-HJKMNP-TV-Z*~$=U]$/;

/** Space, tab, LF, VT, FF, and CR. U+0085 and U+FEFF are left in place. */
function isAsciiWhitespace(code: number): boolean {
  return code === 32 || code === 9 || code === 10 || code === 11 || code === 12 || code === 13;
}

function trimAsciiWhitespace(input: string): string {
  let start = 0;
  let end = input.length;
  while (start < end && isAsciiWhitespace(input.charCodeAt(start))) start++;
  while (end > start && isAsciiWhitespace(input.charCodeAt(end - 1))) end--;
  return input.slice(start, end);
}

/**
 * Trim ASCII whitespace only. A non-ASCII character rejects the value.
 * Then uppercase ASCII a-z only, and map O to 0 and I or L to 1.
 * Unicode case folding is not used.
 * Returns null when the value contains a non-ASCII character.
 */
export function normalizePairingCode(input: string): string | null {
  const trimmed = trimAsciiWhitespace(input);
  let out = '';
  for (let i = 0; i < trimmed.length; i++) {
    const code = trimmed.charCodeAt(i);
    if (code > 127) return null;
    let ch = code >= 97 && code <= 122 ? String.fromCharCode(code - 32) : trimmed[i];
    if (ch === 'O') ch = '0';
    else if (ch === 'I' || ch === 'L') ch = '1';
    out += ch;
  }
  return out;
}

export function pairingCheckCharacter(payload: string): string {
  let value = 0n;
  for (const ch of payload) {
    const digit = PAIRING_PAYLOAD.indexOf(ch);
    if (digit < 0) {
      throw new Error('pairing payload is not Crockford base32');
    }
    value = value * 32n + BigInt(digit);
  }
  return PAIRING_CHECK[Number(value % 37n)] ?? '';
}

/** `payload` is 8 Crockford characters, without the SBP prefix or hyphens. */
export function formatPairingCode(payload: string): string {
  const check = pairingCheckCharacter(payload);
  return `SBP-${payload.slice(0, 4)}-${payload.slice(4, 8)}-${check}`;
}

/**
 * A product barcode, matching the server scan classifier: ASCII digits, length
 * 1 through 14, after the same ASCII-only trim. That includes 6- and 7-digit UPC-E.
 */
export function isProductBarcode(input: string): boolean {
  return /^\d{1,14}$/.test(trimAsciiWhitespace(input));
}

/** Grammar only, after normalization, including a wrong check character. Scan rejection uses this. */
export function looksLikePairingCode(input: string): boolean {
  const code = normalizePairingCode(input);
  return code !== null && PAIRING_CODE_GRAMMAR.test(code);
}

/** Grammar plus the check character. Pairing completion uses this. */
export function isPairingCode(input: string): boolean {
  const code = normalizePairingCode(input);
  if (code === null || !PAIRING_CODE_GRAMMAR.test(code)) return false;
  const payload = code.slice(4, 8) + code.slice(9, 13);
  return code[14] === pairingCheckCharacter(payload);
}
