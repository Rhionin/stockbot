// Shared TypeScript types mirroring the backend's camelCase JSON API shapes.
// Field-by-field verified against the Go domain structs and handler response
// types in internal/product, internal/scan, internal/inventory,
// internal/suggestion, internal/shopping, and internal/server.

export type ExternalSource = 'openfoodfacts' | 'openproductsfacts' | 'openbeautyfacts' | 'openpetfoodfacts';

export type NetDimension = 'mass' | 'volume';
export type NetSizeOrigin = 'off' | 'manual' | 'backfill';

export interface ProductSummary {
  id: string;
  name: string;
  category: string;
  unitOfMeasure: string;
  // Thumbnail sourced from Open Food Facts. Omitted from the JSON response
  // (via omitempty) when no image is available.
  imageUrl?: string;
  // Optional field indicating the external database source for this product.
  externalSource?: ExternalSource;
  // Grams or milliliters for one unit. Omitted when the size is unknown.
  netBaseValue?: number;
  netDimension?: NetDimension;
  netSizeOrigin?: NetSizeOrigin;
  // Ounces or fluid ounces, rounded for the editor.
  netAmount?: number;
  netUnit?: 'oz' | 'fl oz';
  // How many individual units one scan of this barcode adds. Omitted until remembered.
  packCount?: number;
}

export interface Product extends ProductSummary {
  createdAt: string; // ISO 8601
  source?: 'user' | 'external';
}

export interface ContributionSettings {
  enabled: boolean;
  configured: boolean;
}

export type ContributionStatus =
  | 'disabled'
  | 'already_upstream'
  | 'not_configured'
  | 'submitted'
  | 'failed';

export interface ContributionReceipt {
  status: ContributionStatus;
  database?: ExternalSource;
  barcode?: string;
  detail?: string;
}

export interface ProductWriteResult extends Product {
  contribution?: ContributionReceipt;
}

export interface ProductDetail extends Product {
  barcodes: string[];
}

export interface ProductWriteInput {
  name: string;
  category: string;
  unitOfMeasure: string;
  contribute?: boolean;
  contributeTo?: ExternalSource;
  barcode?: string;
  // Present only when the person changed the size. Null clears it.
  netAmount?: number | null;
  netUnit?: string | null;
  packCount?: number | null;
}

export type ScanDirection = 'stock_in' | 'stock_out';

export type ScanStatus = 'pending' | 'flagged' | 'committed' | 'cancelled' | 'qr';

// Reserved control-barcode strings the backend classifies against, exposed via
// GET /api/scanner/config so the browser recognizes the exact same values.
// Matches scannerConfigResponse in internal/server/handler_scanner.go.
export interface ScannerConfig {
  stockInBarcode: string;
  stockOutBarcode: string;
  currentMode: ScanDirection;
  // True while the headless capture device is open. False when it is missing
  // or no listener is configured.
  connected: boolean;
}

// A barcode the server has accepted and is still looking up. Not a stored
// scan entry. The following scan event replaces it.
export interface ProcessingNotice {
  id: string;
  userId: string;
  barcode: string;
  direction: ScanDirection | null;
  scannedAt: string;
}

export interface ProcessingFailure {
  id: string;
  barcode: string;
  message: string;
}

export interface ScanGroupHint {
  groupId?: string;
  name: string;
}

export interface ScanEntry {
  id: string;
  userId: string;
  barcode: string;
  scannedAt: string; // ISO 8601
  direction: ScanDirection | null;
  unitCount: number;
  expiresAt: string | null;
  status: ScanStatus;
  productId: string | null;
  product: ProductSummary | null;
  groupHint?: ScanGroupHint;
  committedAt: string | null;
  createdAt: string;
}

export interface Item {
  id: string;
  userId: string;
  productId: string;
  product: ProductSummary;
  targetQuantity: number | null;
  createdAt: string;
}

export interface ItemInstance {
  id: string;
  itemId: string;
  stockInAt: string; // ISO 8601
  expiresAt: string | null;
  removedAt: string | null;
  removalReason: string | null;
  createdAt: string;
}

export type ExpiryStatus = 'ok' | 'near_expiry' | 'expired';

export type ItemInstanceWithStatus = ItemInstance & { expiryStatus: ExpiryStatus };

export interface InventoryGroupMember {
  productId: string;
  name: string;
  onHand: number;
  itemId?: string;
  barcodes?: string[];
}

export interface InventoryGroup {
  id: string;
  name: string;
  rule: string;
  ruleConfirmed: boolean;
  pinnedProductId?: string;
  onHand: number;
  windowMonths?: number;
  quantity?: number;
  dimension?: NetDimension;
  memberCount: number;
  members: InventoryGroupMember[];
}

export interface InventoryItem {
  item: Item;
  instanceCount: number;
  nearExpiryCount: number;
  expiredCount: number;
  needsAttention: boolean;
  group?: InventoryGroup;
  // A wiped row. The household list is meant to omit these; the link search
  // still drops one if it arrives.
  shelved?: boolean;
}

export interface HistoryPace {
  known: boolean;
  daysBetweenUses?: number;
  daysLeft?: number;
  daysLeftKnown: boolean;
  trend?: 'steady' | 'faster' | 'slower' | '';
  sparkline: number[];
}

export interface HistoryMove {
  id: string;
  direction: 'in' | 'out';
  quantity: number;
  at: string;
  source: 'scan' | 'manual';
  productId: string;
  productName: string;
}

export interface HistoryView {
  kind: 'product' | 'group';
  id: string;
  name: string;
  detail?: string;
  onHand: number;
  pace: HistoryPace;
  moves: HistoryMove[];
}

export interface LedgerMove {
  id: string;
  direction: 'in' | 'out';
  quantity: number;
  at: string;
  source: 'scan' | 'manual';
  productId: string;
  productName: string;
  imageUrl?: string;
  groupId?: string;
  groupName?: string;
}

export interface HistoryLedger {
  moves: LedgerMove[];
  setupAt?: string;
}

export interface GroupPreview {
  productId: string;
  because: string;
  buy: number;
  explain: string;
}

export interface TargetQuantitySuggestion {
  itemId: string;
  suggestedQuantity: number;
  reasoning: string;
  consumptionEventCount: number;
  dataInsufficient: boolean;
}

export type ReplenishmentMode = 'target' | 'replenish';

export type ConnectionState = 'not_required' | 'connected' | 'reauth_required' | 'disconnected';

export interface ProviderCapabilities {
  auth: string;
  delivery: string;
  confirmation: string;
  mutation: string;
  identity: string;
}

// Matches ProviderInfo in internal/server/handler_providers.go.
export interface ProviderInfo {
  id: string;
  displayName: string;
  capabilities: ProviderCapabilities;
  connectionState: ConnectionState;
  credentialsConfigured: boolean;
  credentials?: ProviderCredentials;
}

// Non-secret view of a provider's application credentials. The client secret
// is never included.
export interface ProviderCredentials {
  clientId: string;
  redirectUri: string;
  modality: string;
  secretSet: boolean;
  source: 'saved' | 'environment' | 'none';
}

export interface SupplySettings {
  months: number;
  opening: boolean;
  wipePhrase: string;
}

export interface SupplyOverride {
  windowMonths?: number;
  quantity?: number;
  clear?: boolean;
}

export interface ShoppingListBasis {
  mode: ReplenishmentMode;
  targetQuantity?: number;
  instanceCount: number;
  consumedUnits: number;
  requested: number;
}

// Matches ShoppingListEntryResponse in internal/server/handler_shopping_list.go.
// Auto-derived entries have an empty string id (no backing shopping_list_items row).
// quantity is the provision quantity: an adjustment when one is set, otherwise
// the computed quantity.
export interface ShoppingListGroupMember {
  itemId: string;
  productId: string;
  name: string;
}

export interface ShoppingListGroup {
  id: string;
  name: string;
  rule: GroupRule;
  ruleConfirmed: boolean;
  members: ShoppingListGroupMember[];
}

export interface ShoppingListEntry {
  id: string;
  itemId: string;
  quantity: number;
  source: 'auto' | 'manual';
  note?: string;
  purchasedAt: string | null;
  replenishmentMode?: ReplenishmentMode;
  provider?: string;
  computedQuantity?: number;
  adjustment?: number;
  basis?: ShoppingListBasis;
  group?: ShoppingListGroup;
}

export interface ProvisionEntryResult {
  entryId: string;
  itemId: string;
  name: string;
  quantity: number;
  outcome: 'confirmed' | 'failed' | 'unknown' | string;
  outcomeReason?: string;
}

export interface ProvisionReport {
  provider: string;
  exported: number;
  failedItems?: string[];
  unknownItems?: string[];
  entries?: ProvisionEntryResult[];
}

// Result of a three-tier product lookup (user override -> global DB -> external API).
// Matches LookupResult in internal/product/lookup.go.
export interface LookupResult {
  product: ProductSummary | null;
  source: string;
}

// Matches buildInfoResponse in internal/server/handler_build.go.
// commit is the full git SHA the image is tagged with, or "unknown".
// version is "dev" for a local build. committedAt, builtAt, and subject are
// omitted when the build was not stamped.
export interface BuildInfo {
  commit: string;
  committedAt?: string;
  builtAt?: string;
  version?: string;
  subject?: string;
}

// Matches batchCommitResponse in internal/server/handler_scan_batch_commit.go.
export type GroupRule = 'same_as_ran_out' | 'favorite' | 'best_deal' | 'favor_variety';

export type SuggestionKind = 'looks_alike' | 'from_scan' | 'from_old_plan';

export interface GroupMember {
  productId: string;
  name: string;
  onHand: number;
  noRestock?: boolean;
  lastStockedAt?: string;
  lastConsumedAt?: string;
}

export interface GroupUsage {
  perMonth: number;
  unit: string;
}

export interface ProductGroup {
  id: string;
  name: string;
  rule: GroupRule | string;
  ruleConfirmed: boolean;
  pinnedProductId?: string;
  windowMonths?: number;
  quantity?: number;
  dimension?: NetDimension;
  usage?: GroupUsage;
  members: GroupMember[];
  runningLow: boolean;
}

export interface SuggestionMember {
  productId: string;
  name: string;
  included: boolean;
  caution: string;
  imageUrl?: string;
  brand?: string;
  variety?: string;
  category?: string;
  unitOfMeasure?: string;
  netAmount?: number;
  netUnit?: string;
  packCount?: number;
  barcodes?: string[];
}

export interface GroupSuggestion {
  id: string;
  kind: SuggestionKind | string;
  title: string;
  proposedRule?: string;
  pinnedProductId?: string;
  existingGroupId?: string;
  status: string;
  members: SuggestionMember[];
}

export interface GroupTarget {
  windowMonths?: number;
  quantity?: number;
  dimension?: NetDimension;
  clear?: boolean;
}

export interface PantryLinkSuggestion {
  itemId: string;
  productId: string;
  name: string;
  onHand: number;
}

export interface TargetConflictMember {
  productId: string;
  name: string;
  windowMonths?: number;
  quantity?: number;
  amount?: number;
  dimension?: NetDimension;
}

export interface BatchCommitResponse {
  updatedCount: number;
  committedIds?: string[];
  failedIds?: string[];
  failedReasons?: string[];
  failedCodes?: string[];
}
