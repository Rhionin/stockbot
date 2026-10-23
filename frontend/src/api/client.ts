// Typed fetch wrappers for the Stockbot Management backend API.
// Endpoint list and request/response shapes verified against
// internal/server/server.go and the individual handler files.
import { reportApiResult } from '../telemetry/client';
import { applyHttpResponse, AUTH_NOT_CONFIGURED_SENTENCE, clearOfflineBanner, notifyApiSuccess, raiseOfflineBanner, SERVER_ERROR_FALLBACK, ServerNoticeError, stockbotCode, UnreachableError, unreachableFrom, unreadableResponse, type BatchCommitResponse, type CommitScanResponse } from './contract';
import {
  asRestock,
  asSuggestions as asCategorySuggestions,
  type CategorySummary,
  type DontRestockResult,
  type RestockItem,
  type RestockPreview,
  type RestockSnapshot,
  type RestockTarget,
  type SuggestionCard,
} from '../restock/types';
import type {
  BuildInfo,
  InventoryItem,
  ItemInstance,
  ItemInstanceWithStatus,
  LookupResult,
  Product,
  ProductDetail,
  ProductWriteInput,
  ProductWriteResult,
  ContributionSettings,
  ScanDirection,
  ScanEntry,
  ScannerConfig,
  ScanStatus,
  ProvisionReport,
  ProviderInfo,
  ShoppingListEntry,
  SupplyOverride,
  SupplySettings,
  ProductGroup,
  GroupSuggestion,
  GroupTarget,
  GroupPreview,
  HistoryView,
  HistoryLedger,
  PantryLinkSuggestion,
  TargetConflictMember,
} from '../types';
import {
  BATCH_COMMIT_PATH,
  type BatchCommitInput,
  type CreateScanEntryInput,
  type HouseholdSession,
  SCANNER_CONFIG_PATH,
  SCANNER_MODE_PATH,
  SCANS_PATH,
  SESSION_PATH,
  type SetScannerModeResponse,
  inventoryListPath,
  scanCommitPath,
  scansQuery,
  toQueryString,
} from './contract';

export { DEFAULT_SCAN_USER_ID, type HouseholdSession } from './contract';

const BASE_URL = import.meta.env.VITE_API_BASE_URL ?? '';

let onUnauthorized: (() => void) | null = null;

// The login gate registers this so an expired session returns to the login page.
export function setUnauthorizedHandler(handler: (() => void) | null) {
  onUnauthorized = handler;
}

// Thrown for any non-2xx response. message is read from the backend's
// {"error": "..."} body shape (see internal/server/handler_wrapper.go writeError).
export class ApiError extends Error {
  readonly status: number;
  readonly code: string;
  readonly members: TargetConflictMember[];
  readonly suggestions: PantryLinkSuggestion[];

  constructor(
    status: number,
    message: string,
    code = '',
    members: TargetConflictMember[] = [],
    suggestions: PantryLinkSuggestion[] = [],
  ) {
    super(message);
    this.name = 'ApiError';
    this.status = status;
    this.code = code;
    this.members = members;
    this.suggestions = suggestions;
  }
}

async function apiFetch<T>(path: string, options?: RequestInit): Promise<T> {
  const headers = new Headers(options?.headers);
  if (options?.body !== undefined) {
    headers.set('Content-Type', 'application/json');
  }

  const started = performance.now();
  const route = path.split('?')[0] ?? path;
  let res: Response;
  try {
    res = await fetch(`${BASE_URL}${path}`, { ...options, headers, credentials: 'same-origin' });
  } catch (error) {
    reportApiResult(route, 0, performance.now() - started, started);
    const unreachable = unreachableFrom(error);
    if (unreachable !== null) {
      raiseOfflineBanner();
      throw unreachable;
    }
    throw error;
  }

  if (!res.ok) {
    if (res.status === 401) {
      onUnauthorized?.();
    }
    let message = res.status >= 500 ? SERVER_ERROR_FALLBACK : res.statusText;
    let code = '';
    let members: TargetConflictMember[] = [];
    let suggestions: PantryLinkSuggestion[] = [];
    try {
      const body = (await res.json()) as {
        error?: string;
        code?: string;
        members?: TargetConflictMember[];
        suggestions?: PantryLinkSuggestion[];
      };
      if (body.error) message = body.error;
      code = stockbotCode(body);
      members = body.members ?? [];
      suggestions = body.suggestions ?? [];
    } catch {
      // Not JSON. A gateway status with no code is offline.
    }
    reportApiResult(route, res.status, performance.now() - started, started);
    throw classifiedError(res.status, message, code, members, suggestions);
  }

  // DELETE endpoints return `{}` with a 200 status (see handler_wrapper.go),
  // not an empty 204 body, but callers of those endpoints don't need the value.
  let text: string;
  try {
    text = await res.text();
  } catch (error) {
    reportApiResult(route, 0, performance.now() - started, started);
    throw unreadableResponse(error);
  }
  if (text === '') {
    reportApiResult(route, res.status, performance.now() - started, started);
    notifyApiSuccess();
    return undefined as T;
  }
  try {
    const body = JSON.parse(text) as T;
    reportApiResult(route, res.status, performance.now() - started, started);
    notifyApiSuccess();
    return body;
  } catch (error) {
    reportApiResult(route, 0, performance.now() - started, started);
    throw unreadableResponse(error);
  }
}

function classifiedError(
  status: number,
  message: string,
  code: string,
  members: TargetConflictMember[] = [],
  suggestions: PantryLinkSuggestion[] = [],
): Error {
  const disposition = applyHttpResponse(status, code);
  if (disposition === 'offline') return new UnreachableError();
  if (disposition === 'auth') return new ServerNoticeError('auth_not_configured', AUTH_NOT_CONFIGURED_SENTENCE);
  return new ApiError(status, message, code, members, suggestions);
}

// --- Products ---

export function lookupProduct(barcode: string): Promise<LookupResult> {
  return apiFetch(`/api/products/lookup${toQueryString({ barcode })}`);
}

export function listProducts(): Promise<Product[]> {
  return apiFetch('/api/products');
}

export function getProduct(id: string): Promise<ProductDetail> {
  return apiFetch(`/api/products/${id}`);
}

export function createProduct(input: ProductWriteInput): Promise<ProductWriteResult> {
  return apiFetch('/api/products', { method: 'POST', body: JSON.stringify(input) });
}

export function updateProduct(id: string, input: ProductWriteInput): Promise<ProductWriteResult> {
  return apiFetch(`/api/products/${id}`, { method: 'PUT', body: JSON.stringify(input) });
}

export function getContributionSettings(): Promise<ContributionSettings> {
  return apiFetch('/api/settings/contribution');
}

export function updateContributionSettings(enabled: boolean): Promise<ContributionSettings> {
  return apiFetch('/api/settings/contribution', {
    method: 'PUT',
    body: JSON.stringify({ enabled }),
  });
}

interface CreateProductOverrideInput {
  barcode: string;
  productId: string;
}

interface CreateProductOverrideResponse {
  barcode: string;
  productId: string;
  source: string;
}

export function createProductOverride(
  input: CreateProductOverrideInput,
): Promise<CreateProductOverrideResponse> {
  return apiFetch('/api/products/overrides', { method: 'POST', body: JSON.stringify(input) });
}

// --- Scan queue ---

export function createScanEntry(input: CreateScanEntryInput): Promise<ScanEntry> {
  return apiFetch(SCANS_PATH, { method: 'POST', body: JSON.stringify(input) });
}

export function listScanEntries(userId: string, status?: ScanStatus): Promise<ScanEntry[]> {
  return apiFetch(scansQuery(userId, status));
}

export function getScanHistory(userId: string): Promise<ScanEntry[]> {
  return apiFetch(`/api/scans/history${toQueryString({ userId })}`);
}

interface UpdateScanEntryInput {
  direction?: ScanDirection;
  unitCount?: number;
  expiresAt?: string;
  productId?: string;
  status?: ScanStatus;
}

export function updateScanEntry(id: string, input: UpdateScanEntryInput): Promise<ScanEntry> {
  return apiFetch(`/api/scans/${id}`, { method: 'PATCH', body: JSON.stringify(input) });
}

export function commitScanEntry(
  id: string,
  instanceId?: string,
  linkProductId?: string,
): Promise<ScanEntry & CommitScanResponse> {
  const body: { instanceId?: string; linkProductId?: string } = {};
  if (instanceId) body.instanceId = instanceId;
  if (linkProductId) body.linkProductId = linkProductId;
  return apiFetch(scanCommitPath(id), {
    method: 'POST',
    body: JSON.stringify(body),
  });
}

export function batchCommitScanEntries(input: BatchCommitInput): Promise<BatchCommitResponse> {
  return apiFetch(BATCH_COMMIT_PATH, { method: 'POST', body: JSON.stringify(input) });
}

// --- Scanner mode and config ---

// Switches the shared scanner mode and triggers a 'scanner_mode' SSE event so
// every connected browser converges on the same direction (matches the
// headless listener behavior). See internal/server/handler_scanner.go.
export function setScannerMode(mode: ScanDirection): Promise<SetScannerModeResponse> {
  return apiFetch(SCANNER_MODE_PATH, { method: 'POST', body: JSON.stringify({ mode }) });
}

// Returns the reserved control-barcode strings the backend classifies against
// (defaults 'STOCK_IN'/'STOCK_OUT'), so the browser matches the exact values.
export function getScannerConfig(): Promise<ScannerConfig> {
  return apiFetch(SCANNER_CONFIG_PATH);
}

// --- Inventory ---

export function getInventoryList(query?: string, userId?: string): Promise<InventoryItem[]> {
  return apiFetch(inventoryListPath(query, userId));
}

export function listItemInstances(itemId: string): Promise<ItemInstanceWithStatus[]> {
  return apiFetch(`/api/inventory/${itemId}/instances`);
}

export function addItemInstance(itemId: string, expiresAt?: string): Promise<ItemInstance> {
  return apiFetch(`/api/inventory/${itemId}/instances`, {
    method: 'POST',
    body: JSON.stringify(expiresAt ? { expiresAt } : {}),
  });
}

export function removeItemInstance(instanceId: string): Promise<void> {
  return apiFetch(`/api/inventory/instances/${instanceId}`, { method: 'DELETE' });
}

export function stockOutItem(itemId: string): Promise<void> {
  return apiFetch(`/api/inventory/${itemId}/stock-out`, { method: 'POST', body: '{}' });
}

export function getItemHistory(itemId: string): Promise<HistoryView> {
  return apiFetch(`/api/items/${itemId}/history`);
}

export function getGroupHistory(groupId: string): Promise<HistoryView> {
  return apiFetch(`/api/groups/${groupId}/history`);
}

export function getHouseholdHistory(filter?: { direction?: string; q?: string }): Promise<HistoryLedger> {
  return apiFetch(`/api/history${toQueryString({
    direction: filter?.direction,
    q: filter?.q,
  })}`);
}

export function setMoveQuantity(id: string, quantity: number): Promise<HistoryView> {
  return apiFetch(`/api/moves/${id}`, { method: 'PATCH', body: JSON.stringify({ quantity }) });
}

export function undoMove(id: string): Promise<HistoryView> {
  return apiFetch(`/api/moves/${id}`, { method: 'DELETE' });
}

// Exact phrase POST /api/inventory/wipe requires. Kept in sync with
// supply.WipePhrase.
export const WIPE_INVENTORY_CONFIRMATION = 'WIPE INVENTORY';

export function wipeInventory(confirmation: string): Promise<void> {
  return apiFetch('/api/inventory/wipe', {
    method: 'POST',
    body: JSON.stringify({ confirmation }),
  });
}

export function getSupplySettings(): Promise<SupplySettings> {
  return apiFetch('/api/settings/supply');
}

export function setSupplyMonths(months: number): Promise<SupplySettings> {
  return apiFetch('/api/settings/supply', {
    method: 'PUT',
    body: JSON.stringify({ months }),
  });
}

export function completeOnboarding(): Promise<{ startedAt: string }> {
  return apiFetch('/api/onboarding/complete', { method: 'POST' });
}

export function getSupplyOverride(productId: string): Promise<SupplyOverride> {
  return apiFetch(`/api/products/${productId}/supply-override`);
}

export function setSupplyOverride(productId: string, override: SupplyOverride): Promise<SupplyOverride> {
  return apiFetch(`/api/products/${productId}/supply-override`, {
    method: 'PUT',
    body: JSON.stringify(override),
  });
}

// --- Shopping list ---

export function getShoppingList(provider?: string): Promise<ShoppingListEntry[]> {
  return apiFetch(`/api/shopping-list${toQueryString({ provider })}`);
}

export function fillShoppingCart(): Promise<ShoppingListEntry[]> {
  return apiFetch('/api/shopping-list/fill', { method: 'POST' });
}

export function addShoppingListItem(itemId: string, quantity: number): Promise<ShoppingListEntry> {
  return apiFetch('/api/shopping-list/items', {
    method: 'POST',
    body: JSON.stringify({ itemId, quantity }),
  });
}

export function removeShoppingListItem(id: string): Promise<void> {
  return apiFetch(`/api/shopping-list/items/${id}`, { method: 'DELETE' });
}

export function swapShoppingLine(id: string, itemId: string): Promise<ShoppingListEntry> {
  return apiFetch(`/api/shopping-list/items/${id}/swap`, {
    method: 'POST',
    body: JSON.stringify({ itemId }),
  });
}

export function markShoppingListItemPurchased(id: string): Promise<ShoppingListEntry> {
  return apiFetch(`/api/shopping-list/items/${id}`, {
    method: 'PATCH',
    body: JSON.stringify({ purchased: true }),
  });
}

export function exportShoppingList(provider?: string): Promise<ProvisionReport> {
  const body: { provider?: string } = {};
  if (provider) body.provider = provider;
  return apiFetch('/api/shopping-list/export', {
    method: 'POST',
    body: Object.keys(body).length > 0 ? JSON.stringify(body) : undefined,
  });
}

export function listProviders(): Promise<ProviderInfo[]> {
  return apiFetch('/api/providers');
}

export function saveProviderCredentials(
  providerId: string,
  credentials: { clientId: string; clientSecret: string; redirectUri: string; modality: string },
): Promise<void> {
  return apiFetch(`/api/providers/${providerId}/credentials`, {
    method: 'PUT',
    body: JSON.stringify(credentials),
  });
}

export function clearProviderCredentials(providerId: string): Promise<void> {
  return apiFetch(`/api/providers/${providerId}/credentials`, { method: 'DELETE' });
}

export function authorizeProvider(providerId: string): Promise<{ authorizationUrl: string }> {
  return apiFetch(`/api/providers/${providerId}/authorize`);
}

export function disconnectProvider(providerId: string): Promise<void> {
  return apiFetch(`/api/providers/${providerId}/connection`, { method: 'DELETE' });
}

export function resetProviderLedger(providerId: string): Promise<void> {
  return apiFetch(`/api/providers/${providerId}/ledger/reset`, { method: 'POST' });
}

export function setShoppingListAdjustment(entryId: string, provider: string, adjustment: number): Promise<{ adjustment: number }> {
  return apiFetch(`/api/shopping-list/items/${entryId}/adjustment${toQueryString({ provider })}`, {
    method: 'PUT',
    body: JSON.stringify({ adjustment }),
  });
}

export function clearShoppingListAdjustment(entryId: string, provider: string): Promise<void> {
  return apiFetch(`/api/shopping-list/items/${entryId}/adjustment${toQueryString({ provider })}`, {
    method: 'DELETE',
  });
}

export function resolveUnknownProvision(entryId: string, provider: string, reachedProvider: boolean): Promise<{ entryId: string }> {
  return apiFetch(`/api/shopping-list/items/${entryId}/unknown-resolution`, {
    method: 'POST',
    body: JSON.stringify({ provider, reachedProvider }),
  });
}

export function saveItemDeal(itemId: string, priceCents: number, label: string): Promise<void> {
  return apiFetch('/api/shopping-list/deals', {
    method: 'PUT',
    body: JSON.stringify({ itemId, priceCents, label }),
  });
}

export function clearItemDeal(itemId: string): Promise<void> {
  return apiFetch(`/api/shopping-list/deals/${itemId}`, { method: 'DELETE' });
}

// --- Build identity ---

export function listGroups(): Promise<ProductGroup[]> {
  return apiFetch('/api/groups');
}

export function getGroup(id: string): Promise<ProductGroup> {
  return apiFetch(`/api/groups/${id}`);
}

export function createGroup(name: string, productIds: string[] = [], target?: GroupTarget): Promise<ProductGroup> {
  return apiFetch('/api/groups', {
    method: 'POST',
    body: JSON.stringify({ name, productIds, ...(target ? { target } : {}) }),
  });
}

export function renameGroup(id: string, name: string): Promise<ProductGroup> {
  return apiFetch(`/api/groups/${id}`, {
    method: 'PATCH',
    body: JSON.stringify({ name }),
  });
}

export function deleteGroup(id: string): Promise<void> {
  return apiFetch(`/api/groups/${id}`, { method: 'DELETE' });
}

export function addGroupMembers(id: string, productIds: string[], fromGroupId = '', target?: GroupTarget): Promise<ProductGroup> {
  return apiFetch(`/api/groups/${id}/members`, {
    method: 'POST',
    body: JSON.stringify({
      productIds,
      ...(fromGroupId ? { fromGroupId } : {}),
      ...(target ? { target } : {}),
    }),
  });
}

export function removeGroupMember(id: string, productId: string): Promise<{ deleted: boolean; group?: ProductGroup }> {
  return apiFetch(`/api/groups/${id}/members/${productId}`, { method: 'DELETE' });
}

export function setMemberRestock(id: string, productId: string, noRestock: boolean): Promise<ProductGroup> {
  return apiFetch(`/api/groups/${id}/members/${productId}/restock`, {
    method: 'PUT',
    body: JSON.stringify({ noRestock }),
  });
}

export function setGroupRule(id: string, rule: string, pinnedProductId = '', confirm = true): Promise<ProductGroup> {
  return apiFetch(`/api/groups/${id}/rule`, {
    method: 'PUT',
    body: JSON.stringify({ rule, pinnedProductId, confirm }),
  });
}

export function previewGroup(input: { groupId: string; rule: string; pinnedProductId?: string }): Promise<GroupPreview> {
  return apiFetch('/api/groups/preview', {
    method: 'POST',
    body: JSON.stringify({
      groupId: input.groupId,
      rule: input.rule,
      pinnedProductId: input.pinnedProductId ?? '',
    }),
  });
}

export function getDefaultGroupRule(): Promise<{ rule: string }> {
  return apiFetch('/api/settings/group-rule');
}

export function setDefaultGroupRule(rule: string): Promise<{ rule: string }> {
  return apiFetch('/api/settings/group-rule', {
    method: 'PUT',
    body: JSON.stringify({ rule }),
  });
}

export function setGroupTarget(id: string, target: GroupTarget): Promise<ProductGroup> {
  return apiFetch(`/api/groups/${id}/target`, {
    method: 'PUT',
    body: JSON.stringify(target),
  });
}

export function noteGroupFromScan(productId: string): Promise<void> {
  return apiFetch('/api/group-suggestions/from-scan', {
    method: 'POST',
    body: JSON.stringify({ productId }),
  });
}

export function listSuggestions(): Promise<GroupSuggestion[]> {
  return apiFetch('/api/group-suggestions');
}

export function rescanSuggestions(): Promise<GroupSuggestion[]> {
  return apiFetch('/api/group-suggestions/rescan', { method: 'POST' });
}

export function acceptSuggestion(id: string, productIds: string[], name?: string, target?: GroupTarget): Promise<ProductGroup> {
  return apiFetch(`/api/group-suggestions/${id}/accept`, {
    method: 'POST',
    body: JSON.stringify({
      productIds,
      ...(name ? { name } : {}),
      ...(target ? { target } : {}),
    }),
  });
}

export function dismissSuggestion(id: string): Promise<void> {
  return apiFetch(`/api/group-suggestions/${id}/dismiss`, { method: 'POST' });
}

export function skipSuggestion(id: string): Promise<void> {
  return apiFetch(`/api/group-suggestions/${id}/skip`, { method: 'POST' });
}

// --- Build identity ---

export function getBuildInfo(): Promise<BuildInfo> {
  return apiFetch('/api/build');
}

// getSession uses fetch directly so a 401 can mean "show the login page"
// without also tripping the signed-in handler.
async function fetchResponse(path: string, init?: RequestInit): Promise<Response> {
  try {
    return await fetch(`${BASE_URL}${path}`, init);
  } catch (error) {
    const unreachable = unreachableFrom(error);
    if (unreachable !== null) {
      raiseOfflineBanner();
      throw unreachable;
    }
    throw error;
  }
}

export async function getSession(): Promise<HouseholdSession> {
  const res = await fetchResponse(SESSION_PATH, { credentials: 'same-origin' });
  if (res.status === 401) {
    clearOfflineBanner();
    return { required: true, username: '', error: '' };
  }
  let text: string;
  try {
    text = await res.text();
  } catch (error) {
    if (!res.ok) return sessionFailure(res.status, '');
    throw unreadableResponse(error);
  }
  let body: { required?: boolean; username?: string; error?: string; code?: string } = {};
  if (text !== '') {
    try {
      body = JSON.parse(text) as { required?: boolean; username?: string; error?: string; code?: string };
    } catch (error) {
      if (!res.ok) return sessionFailure(res.status, '');
      throw unreadableResponse(error);
    }
  }
  if (!res.ok) return sessionFailure(res.status, stockbotCode(body), body.error);
  notifyApiSuccess();
  if (!body.required) {
    return { required: false, username: '', error: '' };
  }
  return { required: true, username: body.username ?? '', error: '' };
}

function sessionFailure(status: number, code: string, errorText?: string): HouseholdSession {
  const disposition = applyHttpResponse(status, code);
  if (disposition === 'offline') throw new UnreachableError();
  if (disposition === 'auth') return { required: true, username: '', error: '' };
  return { required: true, username: '', error: errorText || 'Sign-in is unavailable right now.' };
}

export async function login(username: string, password: string): Promise<void> {
  const res = await fetchResponse('/api/login', {
    method: 'POST',
    credentials: 'same-origin',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ username, password }),
  });
  if (!res.ok) {
    let message = 'The username or password is incorrect.';
    let code = '';
    try {
      const body = (await res.json()) as { error?: string; code?: string };
      if (body.error) message = body.error;
      code = stockbotCode(body);
    } catch {
      // The status is enough when the body is empty.
    }
    throw classifiedError(res.status, message, code);
  }
  notifyApiSuccess();
}

export async function logout(): Promise<void> {
  const res = await fetchResponse('/api/logout', { method: 'POST', credentials: 'same-origin' });
  if (!res.ok) {
    let code: string;
    try {
      code = stockbotCode(await res.json());
    } catch {
      code = '';
    }
    const failure = classifiedError(res.status, 'Could not sign out.', code);
    throw failure;
  }
  notifyApiSuccess();
}

export interface PushSubscriptionInput {
  endpoint: string;
  keys: { p256dh: string; auth: string };
  userId?: string;
  deviceId?: string;
}

export function getVapidPublicKey(): Promise<{ publicKey: string }> {
  return apiFetch('/api/push/vapid-public-key');
}

export function savePushSubscription(body: PushSubscriptionInput): Promise<{ endpoint: string }> {
  return apiFetch('/api/push/subscriptions', {
    method: 'POST',
    body: JSON.stringify(body),
  });
}

export function deletePushSubscription(endpoint: string): Promise<void> {
  return apiFetch('/api/push/subscriptions', {
    method: 'DELETE',
    body: JSON.stringify({ endpoint }),
  });
}

export async function getRestock(): Promise<RestockSnapshot | null> {
  return asRestock(await apiFetch<unknown>('/api/restock'));
}

export function setHouseholdTarget(target: RestockTarget): Promise<RestockSnapshot> {
  return apiFetch('/api/restock/household', { method: 'PUT', body: JSON.stringify(target) });
}

export function setItemTarget(itemId: string, target: RestockTarget): Promise<RestockItem> {
  return apiFetch(`/api/restock/items/${itemId}`, { method: 'PUT', body: JSON.stringify(target) });
}

export function clearItemTarget(itemId: string): Promise<RestockItem> {
  return apiFetch(`/api/restock/items/${itemId}`, { method: 'DELETE' });
}

export function dontRestockItem(itemId: string): Promise<DontRestockResult> {
  return apiFetch(`/api/restock/items/${itemId}/dont-restock`, { method: 'POST' });
}

export function addItemCategory(itemId: string, categoryId: string): Promise<RestockItem> {
  return apiFetch(`/api/restock/items/${itemId}/categories`, {
    method: 'POST',
    body: JSON.stringify({ categoryId }),
  });
}

export function removeItemCategory(itemId: string, categoryId: string): Promise<RestockItem> {
  return apiFetch(`/api/restock/items/${itemId}/categories/${categoryId}`, { method: 'DELETE' });
}

export function createCategory(name: string): Promise<CategorySummary> {
  return apiFetch('/api/restock/categories', { method: 'POST', body: JSON.stringify({ name }) });
}

export function setCategoryTarget(id: string, target: RestockTarget | null): Promise<RestockSnapshot> {
  return apiFetch(`/api/restock/categories/${id}/target`, {
    method: 'PUT',
    body: JSON.stringify(target ? target : { clear: true }),
  });
}

export function previewCategoryTarget(id: string, target: RestockTarget): Promise<RestockPreview> {
  return apiFetch(`/api/restock/categories/${id}/preview`, { method: 'POST', body: JSON.stringify(target) });
}

export function renameCategory(id: string, name: string): Promise<RestockSnapshot> {
  return apiFetch(`/api/restock/categories/${id}`, { method: 'PATCH', body: JSON.stringify({ name }) });
}

export function mergeCategory(id: string, intoId: string): Promise<RestockSnapshot> {
  return apiFetch(`/api/restock/categories/${id}/merge`, { method: 'POST', body: JSON.stringify({ intoId }) });
}

export function deleteCategory(id: string): Promise<RestockSnapshot> {
  return apiFetch(`/api/restock/categories/${id}`, { method: 'DELETE' });
}

export function previewGroupTarget(id: string, target: RestockTarget): Promise<RestockPreview> {
  return apiFetch(`/api/restock/groups/${id}/preview`, { method: 'POST', body: JSON.stringify(target) });
}

export async function getCategorySuggestions(): Promise<SuggestionCard[]> {
  return asCategorySuggestions(await apiFetch<unknown>('/api/restock/suggestions'));
}

export function applyCategorySuggestions(category: string, itemIds: string[]): Promise<RestockSnapshot> {
  return apiFetch('/api/restock/suggestions/apply', {
    method: 'POST',
    body: JSON.stringify({ category, itemIds }),
  });
}

export function dismissCategorySuggestions(category: string, itemIds: string[]): Promise<SuggestionCard[]> {
  return apiFetch('/api/restock/suggestions/dismiss', {
    method: 'POST',
    body: JSON.stringify({ category, itemIds }),
  });
}
