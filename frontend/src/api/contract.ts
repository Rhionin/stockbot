// Request paths and body shapes shared by the web client and the Expo app.
// The phone sends HTTP Basic auth instead of the browser session cookie, but
// both call these same routes.
import type { ProductSummary, ScanDirection, ScanGroupHint, ScanStatus } from '../types';

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

// `backfill` on this body is accepted and ignored. Omitting it is the request
// this client sends. The response never includes the backfill pair.
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

// Backfill on a stock-out. Appended for the web queue; the phone shares this
// file and does not have to send backfill until it grows the same card.
// Batch commit accepts `backfill` and ignores it. This client does not send
// the field. A batch never backfills, and its body is committedIds,
// stockOutCounts, and the parallel failedIds / failedReasons / failedCodes.

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

// stockInMoveId is the stock-in (source backfill). stockOutMoveId is the
// stock-out (source scan). DELETE /api/moves/{id} accepts either id and
// removes the pair. The delete body is the item history view, not this pair.
export interface BackfillPairMoves {
  stockInMoveId: string;
  stockOutMoveId: string;
}

// Every field is present on a successful backfill commit.
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

// Pair undo confirmation, shared with the phone.
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

// GET /api/moves/{id}/preview. Read-only. {id} is stockInMoveId or stockOutMoveId.
export interface UndoPreviewEffect {
  itemId: string;
  productId: string;
  name: string;
  stockInMoveId?: string;
  stockOutMoveId?: string;
  onHand: number;
  reshelvesOnUndo: boolean;
}

export interface UndoPreview {
  effects: UndoPreviewEffect[];
}

export function undoPreviewPath(id: string): string {
  return `/api/moves/${id}/preview`;
}

// The backfill stock-in id. Preview and DELETE both accept either id of the pair.
export function pairStockInMoveId(
  move: { id: string; source: string; at: string; productId: string },
  moves: readonly { id: string; source: string; at: string; productId: string }[],
): string {
  if (move.source === BACKFILL_SOURCE) return move.id;
  const stockIn = moves.find((other) => other.source === BACKFILL_SOURCE
    && other.productId === move.productId
    && other.at === move.at);
  return stockIn === undefined ? move.id : stockIn.id;
}

// True only when the effect for this move says the undo re-shelves.
// A missing preview, a different effect, and false all leave the line out.
export function previewReshelves(preview: UndoPreview | undefined, moveId: string): boolean {
  if (preview === undefined) return false;
  return preview.effects.some((effect) => effect.reshelvesOnUndo === true
    && (effect.stockInMoveId === moveId || effect.stockOutMoveId === moveId));
}

export function pairUndoCopy(itemName: string, reshelvesOnUndo: boolean | undefined): PairUndoCopy {
  return {
    title: PAIR_UNDO_TITLE,
    body: pairUndoBody(itemName),
    hideLine: reshelvesOnUndo === true ? pairUndoHideLine(itemName) : '',
    confirm: PAIR_UNDO_CONFIRM,
    cancel: PAIR_UNDO_CANCEL,
  };
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

export const NOTHING_ON_HAND_TITLE = 'Nothing on hand to take out.';

// The phone still shows this sentence. The web card uses nothingOnHandBody.
export const NOTHING_ON_HAND_BODY =
  "Stockbot thinks you have none, so this can't be confirmed yet. Stock it in first, or remove this scan.";

const NOTHING_ON_HAND_BODY_ONE =
  'Stockbot thinks you have none. If you had one anyway, this adds it and takes it out in one step.';

export function nothingOnHandBody(unitCount: number): string {
  if (Number.isInteger(unitCount) && unitCount > 1) {
    return `Stockbot thinks you have none. If you had ${unitCount} anyway, this adds them and takes them out in one step.`;
  }
  return NOTHING_ON_HAND_BODY_ONE;
}

// After a batch confirm that skipped nothing-on-hand cards. The lead is "{M} taken out."
export function skippedNothingOnHandBanner(takenOut: number, stillQueued: number): string | null {
  if (!Number.isInteger(takenOut) || takenOut < 1) return null;
  if (!Number.isInteger(stillQueued) || stillQueued < 1) return null;
  return `${takenOut} taken out. ${stillQueued} with nothing on hand are still in the queue. Each one has its own Add it and take it out button.`;
}

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

// A listed count of zero is nothing on hand. Absence is not an empty shelf.
export function shelfHasNothingOnHand(onHand: number | undefined): boolean {
  return onHand !== undefined && onHand <= 0;
}

// GET /api/scans sends onHand: 0 for a linked stock-out with nothing on the shelf.
// Omitted is not that case.
export function scanReportsNothingOnHand(entry: ScanListEntry): boolean {
  return entry.onHand === 0;
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
