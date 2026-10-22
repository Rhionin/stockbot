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

// Commit outcomes for the web queue. Appended after the shared routes so a
// merge with the Expo client keeps that half of the file and adds this half.

export const NO_CLOSE_MATCH = 'No close match found.';

export const BATCH_FAILURE_FALLBACK = 'Some scans could not be confirmed.';

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

// The short-shelf note only when fewer units left than the card asked for.
// Equal counts, a stock-in body, and a batch body (no counts) say nothing.
export function formatPartialRemoval(body: CommitScanResponse | null | undefined): string | null {
  if (body === null || body === undefined) return null;
  const record = body as Record<string, unknown>;
  const removedCount = record[REMOVED_COUNT_FIELD];
  const requestedCount = record[REQUESTED_COUNT_FIELD];
  if (typeof removedCount !== 'number' || !Number.isInteger(removedCount)) return null;
  if (typeof requestedCount !== 'number' || !Number.isInteger(requestedCount)) return null;
  if (removedCount < 1 || removedCount >= requestedCount) return null;
  if (removedCount === 1) return 'Only 1 was on hand, so 1 was taken out.';
  return `Only ${removedCount} were on hand, so ${removedCount} were taken out.`;
}

// A 5xx with no JSON error field: a migration 503, a proxy 502/504, or empty
// HTTP/2 status text. Appended so the Expo client can show the same sentence.
export const SERVER_ERROR_FALLBACK = 'Something went wrong. Try again.';

// Backfill on a stock-out. Appended for the web queue; the phone shares this
// file and does not have to send backfill until it grows the same card.
// Batch commit has no backfill field. The server ignores it there.

export const CODE_NONE_ON_HAND = 'none_on_hand';
export const CODE_UNITS_ON_HAND = 'units_on_hand';
export const CODE_NOT_STOCK_OUT = 'not_stock_out';
export const CODE_NOT_PENDING = 'not_pending';
export const CODE_DIRECTION_UNSET = 'direction_unset';
export const CODE_SCAN_NOT_FOUND = 'scan_not_found';
export const CODE_INTERNAL = 'internal';

export const BACKFILL_SOURCE = 'backfill';

export const NOTHING_ON_HAND_TO_TAKE_OUT = 'Nothing on hand to take out.';
export const ADD_IT_AND_TAKE_IT_OUT = 'Add it and take it out';
export const ADD_AS_A_NEW_ITEM = 'Add as a new item';
export const ADDED_AND_TAKEN_OUT = 'Added and taken out.';
export const ADDED_WHEN_TAKING_OUT = 'Added when taking out';

export interface BackfillCommitRequest {
  backfill: true;
}

// The pair created by this commit. DELETE /api/moves/{id} accepts either id.
export interface BackfillPairMoves {
  stockInMoveId: string;
  stockOutMoveId: string;
}

export interface BackfillCommitResponse {
  removed: number;
  backfilled: number;
  itemId: string;
  backfill: BackfillPairMoves;
}

// What the card does with a backfill commit's code. The sentence is always
// the server's error field. units_on_hand returns to the normal stock-out.
// product_unresolved returns to defining the barcode. A missing scan reloads.
export type BackfillFailureKind = 'define' | 'stock-out' | 'reload' | 'sentence';

export function backfillFailureKind(code: string | undefined): BackfillFailureKind {
  switch (code) {
    case CODE_PRODUCT_UNRESOLVED:
      return 'define';
    case CODE_UNITS_ON_HAND:
      return 'stock-out';
    case CODE_NOT_PENDING:
    case CODE_SCAN_NOT_FOUND:
      return 'reload';
    case CODE_NOT_STOCK_OUT:
    case CODE_DIRECTION_UNSET:
    case CODE_NONE_ON_HAND:
    case CODE_INTERNAL:
      return 'sentence';
    default:
      return 'sentence';
  }
}

export function readOnHand(body: object | null | undefined): number | undefined {
  if (body === null || body === undefined) return undefined;
  const onHand = (body as Record<string, unknown>).onHand;
  if (typeof onHand !== 'number' || !Number.isInteger(onHand)) return undefined;
  return onHand;
}

export function readItemId(body: object | null | undefined): string {
  if (body === null || body === undefined) return '';
  const itemId = (body as Record<string, unknown>).itemId;
  return typeof itemId === 'string' ? itemId : '';
}

export const CODE_CONFLICTING_FIELDS = 'conflicting_fields';
export const CODE_BACKFILL_LOCKED = 'backfill_locked';

// Pair undo confirmation, shared with the phone. The stock-in unshelved flag
// is not on the history or inventory JSON, so the extra line uses the live
// count the client already has: history onHand, or the inventory list.
export const PAIR_UNDO_TITLE = 'Undo "Add it and take it out"?';
export const PAIR_UNDO_CONFIRM = 'Undo';
export const PAIR_UNDO_CANCEL = 'Cancel';

export function pairUndoBody(itemName: string): string {
  return `This removes both the add and the take-out for ${itemName}, as if neither happened.`;
}

export function pairUndoHideLine(itemName: string): string {
  return `${itemName} will be hidden from your inventory again, since none are on hand.`;
}

export interface PairUndoCopy {
  title: string;
  body: string;
  hideLine: string;
  confirm: string;
  cancel: string;
}

// onHand is the item's current live units. The pair nets to zero, so 0 means
// undo leaves none on hand. Any other count omits the hide line.
export function pairUndoCopy(itemName: string, onHand: number): PairUndoCopy {
  return {
    title: PAIR_UNDO_TITLE,
    body: pairUndoBody(itemName),
    hideLine: onHand === 0 ? pairUndoHideLine(itemName) : '',
    confirm: PAIR_UNDO_CONFIRM,
    cancel: PAIR_UNDO_CANCEL,
  };
}

// GET /api/inventory has no top-level onHand. A group member carries onHand.
// A row without that member uses instanceCount, the live units on the shelf.
export function onHandByProduct(rows: {
  item: { productId: string };
  instanceCount: number;
  group?: { members: { productId: string; onHand: number }[] };
}[]): Map<string, number> {
  const counts = new Map<string, number>();
  if (!Array.isArray(rows)) return counts;
  for (const row of rows) {
    const member = row.group?.members.find((entry) => entry.productId === row.item.productId);
    counts.set(row.item.productId, member !== undefined ? member.onHand : row.instanceCount);
  }
  return counts;
}

export function linkSuggestionsPath(id: string): string {
  return `${SCANS_PATH}/${id}/link-suggestions`;
}

export interface LinkSuggestionsResponse {
  suggestions: { itemId: string; productId: string; name: string; onHand: number }[];
}

// A backfill stock-in, and the stock-out recorded with it, cannot change count.
export function quantityLocked(
  move: { source: string; direction: string; at: string; productId: string },
  moves: { source: string; direction: string; at: string; productId: string }[],
): boolean {
  if (move.source === BACKFILL_SOURCE) return true;
  return moves.some((other) => other.source === BACKFILL_SOURCE
    && other.productId === move.productId
    && other.at === move.at
    && move.source === 'scan'
    && move.direction === 'out');
}
