// Phone client for the Stockbot API. Paths and body shapes come from the web
// contract. Every request carries the household password as HTTP Basic auth.
import {
  BATCH_COMMIT_PATH,
  type BatchCommitInput,
  type BatchCommitResponse,
  type CommitScanResponse,
  DEFAULT_SCAN_USER_ID,
  type HouseholdSession,
  SCANNER_CONFIG_PATH,
  SCANNER_MODE_PATH,
  SCANS_PATH,
  SESSION_PATH,
  type SetScannerModeResponse,
  inventoryListPath,
  scanCommitPath,
  scansQuery,
  applyHttpResponse,
  noticeForDisposition,
  notifyApiSuccess,
  raiseOfflineBanner,
  readItemId,
  readOnHand,
  SERVER_ERROR_FALLBACK,
  stockbotCode,
  UnreachableError,
  unreachableFrom,
  unreadableResponse,
} from '@stockbot/shared/contract';
import type {
  InventoryItem,
  PantryLinkSuggestion,
  ScanDirection,
  ScanEntry,
  ScannerConfig,
  TargetConflictMember,
} from '@stockbot/shared/types';

// The public hostname's login name. See deploy/README.md.
export const HOUSEHOLD_USERNAME = 'pantry';

export class ApiError extends Error {
  readonly status: number;
  readonly code: string;
  readonly suggestions: PantryLinkSuggestion[];
  readonly itemId: string;
  readonly onHand: number | undefined;
  readonly provider: string;
  readonly members: TargetConflictMember[];

  constructor(
    status: number,
    message: string,
    code = '',
    suggestions: PantryLinkSuggestion[] = [],
    itemId = '',
    onHand?: number,
    provider = '',
    members: TargetConflictMember[] = [],
  ) {
    super(message);
    this.name = 'ApiError';
    this.status = status;
    this.code = code;
    this.suggestions = suggestions;
    this.itemId = itemId;
    this.onHand = onHand;
    this.provider = provider;
    this.members = members;
  }
}

export interface StockbotCredentials {
  serverUrl: string;
  password: string;
}

export interface StockbotClient {
  getSession: () => Promise<HouseholdSession>;
  getScannerConfig: () => Promise<ScannerConfig>;
  setScannerMode: (mode: ScanDirection) => Promise<SetScannerModeResponse>;
  createScan: (barcode: string, direction: ScanDirection) => Promise<ScanEntry>;
  listPending: () => Promise<ScanEntry[]>;
  commitScan: (id: string, linkProductId?: string) => Promise<ScanEntry & CommitScanResponse>;
  cancelScan: (id: string) => Promise<ScanEntry>;
  batchCommit: (ids: string[]) => Promise<BatchCommitResponse>;
  // Household items, including zero stock. Same call as getInventoryList in
  // the web client (GET /api/inventory). Not the product catalog.
  getInventoryList: (query?: string) => Promise<InventoryItem[]>;
}

export interface ClientOptions {
  onUnauthorized?: () => void;
  fetchImpl?: typeof fetch;
}

export function basicAuthorization(username: string, password: string): string {
  const bytes = new TextEncoder().encode(`${username}:${password}`);
  let binary = '';
  for (const byte of bytes) binary += String.fromCharCode(byte);
  return `Basic ${btoa(binary)}`;
}

export function createStockbotClient(credentials: StockbotCredentials, options: ClientOptions = {}): StockbotClient {
  const fetchImpl = options.fetchImpl ?? fetch;
  const authorization = basicAuthorization(HOUSEHOLD_USERNAME, credentials.password);

  async function request<T>(path: string, init?: RequestInit): Promise<T> {
    const headers = new Headers(init?.headers);
    headers.set('Authorization', authorization);
    headers.set('Accept', 'application/json');
    if (init?.body !== undefined) headers.set('Content-Type', 'application/json');

    let res: Response;
    try {
      res = await fetchImpl(`${credentials.serverUrl}${path}`, { ...init, headers });
    } catch (error) {
      const unreachable = unreachableFrom(error);
      if (unreachable !== null) {
        raiseOfflineBanner();
        throw unreachable;
      }
      throw new ApiError(0, 'Stockbot did not answer at that address.');
    }

    if (!res.ok) {
      if (res.status === 401) options.onUnauthorized?.();
      const parsed = await readError(res);
      const disposition = applyHttpResponse(res.status, parsed.code);
      const notice = noticeForDisposition(disposition);
      if (notice !== null) throw notice;
      throw new ApiError(
        res.status,
        parsed.message,
        parsed.code,
        parsed.suggestions,
        parsed.itemId,
        parsed.onHand,
        parsed.provider,
        parsed.members,
      );
    }

    let text: string;
    try {
      text = await res.text();
    } catch (error) {
      throw unreadableResponse(error);
    }
    if (text === '') {
      notifyApiSuccess();
      return undefined as T;
    }
    let body: T;
    try {
      body = JSON.parse(text) as T;
    } catch (error) {
      throw unreadableResponse(error);
    }
    notifyApiSuccess();
    return body;
  }

  return {
    async getSession() {
      const body = await request<Partial<HouseholdSession>>(SESSION_PATH);
      return {
        required: Boolean(body.required),
        username: body.username ?? '',
        error: body.error ?? '',
      };
    },
    getScannerConfig() {
      return request<ScannerConfig>(SCANNER_CONFIG_PATH);
    },
    setScannerMode(mode) {
      return request<SetScannerModeResponse>(SCANNER_MODE_PATH, {
        method: 'POST',
        body: JSON.stringify({ mode }),
      });
    },
    createScan(barcode, direction) {
      return request<ScanEntry>(SCANS_PATH, {
        method: 'POST',
        body: JSON.stringify({ barcode, direction, userId: DEFAULT_SCAN_USER_ID }),
      });
    },
    async listPending() {
      const entries = await request<ScanEntry[]>(scansQuery(DEFAULT_SCAN_USER_ID, 'pending'));
      return Array.isArray(entries) ? entries : [];
    },
    commitScan(id, linkProductId) {
      const body: { linkProductId?: string } = {};
      if (linkProductId) body.linkProductId = linkProductId;
      return request<ScanEntry & CommitScanResponse>(scanCommitPath(id), {
        method: 'POST',
        body: JSON.stringify(body),
      });
    },
    cancelScan(id) {
      return request<ScanEntry>(`${SCANS_PATH}/${id}`, {
        method: 'PATCH',
        body: JSON.stringify({ status: 'cancelled' }),
      });
    },
    batchCommit(ids) {
      const body: BatchCommitInput = { scanEntryIds: ids, commit: true };
      return request<BatchCommitResponse>(BATCH_COMMIT_PATH, {
        method: 'POST',
        body: JSON.stringify(body),
      });
    },
    async getInventoryList(query) {
      const rows = await request<unknown>(inventoryListPath(query, DEFAULT_SCAN_USER_ID));
      if (!Array.isArray(rows)) throw new Error('Inventory could not be read.');
      return rows as InventoryItem[];
    },
  };
}

function failureMessage(status: number, errorField: unknown, statusText: string): string {
  if (typeof errorField === 'string' && errorField !== '') return errorField;
  if (status >= 500) return SERVER_ERROR_FALLBACK;
  return statusText || 'The request failed.';
}

interface ParsedError {
  message: string;
  code: string;
  suggestions: PantryLinkSuggestion[];
  itemId: string;
  onHand: number | undefined;
  provider: string;
  members: TargetConflictMember[];
}

async function readError(res: Response): Promise<ParsedError> {
  const empty: ParsedError = {
    message: failureMessage(res.status, undefined, res.statusText),
    code: '',
    suggestions: [],
    itemId: '',
    onHand: undefined,
    provider: '',
    members: [],
  };
  try {
    const body = (await res.json()) as {
      error?: unknown;
      suggestions?: PantryLinkSuggestion[];
      provider?: unknown;
      members?: TargetConflictMember[];
    };
    return {
      message: failureMessage(res.status, body.error, res.statusText),
      code: stockbotCode(body),
      suggestions: body.suggestions ?? [],
      itemId: readItemId(body),
      onHand: readOnHand(body),
      provider: typeof body.provider === 'string' ? body.provider : '',
      members: body.members ?? [],
    };
  } catch {
    return empty;
  }
}
