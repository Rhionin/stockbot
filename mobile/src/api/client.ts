// Phone client for the Stockbot API. Paths and body shapes come from the web
// contract. Every request names the Expo build and, once signed in, carries the
// account's Bearer session token.
import {
  AUTH_LOGIN_PATH,
  AUTH_LOGOUT_PATH,
  AUTH_RESET_PATH,
  AUTH_SESSION_PATH,
  BATCH_COMMIT_PATH,
  CLIENT_HEADER,
  CONFIG_PATH,
  INVITE_ACCEPT_PATH,
  SWITCH_HOUSEHOLD_PATH,
  UPGRADE_REQUIRED_SENTENCE,
  expoClientHeader,
  type AuthSession,
  type LiveConfig,
  type LoginResponse,
  type SwitchHouseholdRequest,
  type BackfillCommitRequest,
  type BackfillCommitResponse,
  type BatchCommitInput,
  type BatchCommitResponse,
  type CommitScanResponse,
  DEFAULT_SCAN_USER_ID,
  SCANNER_CONFIG_PATH,
  SCANNER_MODE_PATH,
  SCANS_PATH,
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
import { APP_VERSION } from '../appVersion';

export const CODE_UPGRADE_REQUIRED = 'upgrade_required';

// A 401 on these routes is a failed sign-in, not an expired session.
const SIGN_IN_PATHS: ReadonlySet<string> = new Set([AUTH_LOGIN_PATH, AUTH_RESET_PATH, INVITE_ACCEPT_PATH]);

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
  token: string | null;
}

export interface StockbotClient {
  getConfig: () => Promise<LiveConfig>;
  login: (email: string, password: string) => Promise<LoginResponse>;
  logout: () => Promise<void>;
  getAuthSession: () => Promise<AuthSession>;
  switchHousehold: (householdId: string) => Promise<AuthSession>;
  getScannerConfig: () => Promise<ScannerConfig>;
  setScannerMode: (mode: ScanDirection) => Promise<SetScannerModeResponse>;
  createScan: (barcode: string, direction: ScanDirection) => Promise<ScanEntry>;
  listPending: () => Promise<ScanEntry[]>;
  commitScan: (id: string, linkProductId?: string) => Promise<ScanEntry & CommitScanResponse>;
  commitBackfill: (id: string) => Promise<BackfillCommitResponse>;
  cancelScan: (id: string) => Promise<ScanEntry>;
  batchCommit: (ids: string[]) => Promise<BatchCommitResponse>;
  // Household items, including zero stock. Same call as getInventoryList in
  // the web client (GET /api/inventory). Not the product catalog.
  getInventoryList: (query?: string) => Promise<InventoryItem[]>;
}

export interface ClientOptions {
  onUnauthorized?: () => void;
  onUpgradeRequired?: () => void;
  fetchImpl?: typeof fetch;
  appVersion?: string;
}

export function createStockbotClient(credentials: StockbotCredentials, options: ClientOptions = {}): StockbotClient {
  const fetchImpl = options.fetchImpl ?? fetch;
  const appVersion = options.appVersion ?? APP_VERSION;

  async function request<T>(path: string, init?: RequestInit): Promise<T> {
    const headers = new Headers(init?.headers);
    headers.set(CLIENT_HEADER, expoClientHeader(appVersion));
    if (credentials.token !== null) headers.set('Authorization', `Bearer ${credentials.token}`);
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
      const parsed = await readError(res);
      if (res.status === 426 || parsed.code === CODE_UPGRADE_REQUIRED) {
        options.onUpgradeRequired?.();
        throw new ApiError(res.status, UPGRADE_REQUIRED_SENTENCE, CODE_UPGRADE_REQUIRED);
      }
      if (res.status === 401 && !SIGN_IN_PATHS.has(path)) options.onUnauthorized?.();
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
    getConfig() {
      return request<LiveConfig>(CONFIG_PATH);
    },
    login(email, password) {
      return request<LoginResponse>(AUTH_LOGIN_PATH, {
        method: 'POST',
        body: JSON.stringify({ email, password }),
      });
    },
    async logout() {
      await request<undefined>(AUTH_LOGOUT_PATH, { method: 'POST' });
    },
    getAuthSession() {
      return request<AuthSession>(AUTH_SESSION_PATH);
    },
    switchHousehold(householdId) {
      const body: SwitchHouseholdRequest = { householdId };
      return request<AuthSession>(SWITCH_HOUSEHOLD_PATH, {
        method: 'POST',
        body: JSON.stringify(body),
      });
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
    commitBackfill(id) {
      const body: BackfillCommitRequest = { backfill: true };
      return request<BackfillCommitResponse>(scanCommitPath(id), {
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
