// Request paths and body shapes shared by the web client and the Expo app.
// The phone sends HTTP Basic auth instead of the browser session cookie, but
// both call these same routes.
import type { InventoryItem, Item, ProductSummary, ScanDirection, ScanStatus, ShoppingListEntry } from '../types';

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

// Hosted multi-household contract (docs/design/api-contract.md).
// Types only. The Pi client still uses the helpers above, including userId,
// until the hosted routes exist. Nothing here selects a tenant except the
// X-Household-Id header, which the server checks against membership, or, for
// device calls, the household bound to the device token.

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
  ;

export interface HostedErrorBody {
  error: string;
  code: HostedErrorCode;
  members?: unknown;
  suggestions?: unknown;
}

export interface AuthSession {
  user: { id: string; email: string };
  memberships: { householdId: string; name: string; role: HouseholdRole }[];
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

export interface CreateHouseholdRequest {
  name: string;
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
}

export interface InviteCreated {
  id: string;
  code: string;
  url: string;
  email: string | null;
  expiresAt: string;
}

export interface InviteSummary {
  id: string;
  email: string | null;
  createdBy: string;
  expiresAt: string;
  status: 'open' | 'revoked' | 'used' | 'expired';
}

export interface InviteListResponse {
  invites: InviteSummary[];
}

export interface AcceptInviteRequest {
  code: string;
}

export interface AcceptInviteResponse {
  householdId: string;
  name: string;
  role: 'member';
}

export interface PairingCode {
  code: string;
  qrPayload: string;
  expiresAt: string;
}

export interface CompletePairingRequest {
  code: string;
  label?: string;
}

export interface CompletePairingResponse {
  deviceId: string;
  token: string;
}

export interface Device {
  id: string;
  label: string | null;
  createdAt: string;
  lastSeenAt: string | null;
  revokedAt: string | null;
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
  status: 'accepted' | 'duplicate';
  cursor: string;
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
}

/** Proposed. GET and PATCH /api/household/settings. */
export interface HouseholdSettings {
  restockMode: RestockMode;
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
  status: 'pending';
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
  | { live: 'sse' | 'poll' }
  | { live: 'push'; appsync: { httpUrl: string; realtimeUrl: string } };

export interface LiveTicket {
  ticket: string;
  channel: `/hh/${string}/${string}`;
  expiresAt: string;
}

export type LiveHintType = 'scans' | 'stock' | 'list' | 'rekey';

export type LiveHint =
  | { type: 'scans' | 'stock' | 'list'; cursor: string }
  | { type: 'rekey' };

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
