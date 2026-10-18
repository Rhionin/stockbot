// Phone client for the Stockbot API. Paths and body shapes come from the web
// contract. Every request carries the household password as HTTP Basic auth.
import {
  BATCH_COMMIT_PATH,
  type BatchCommitInput,
  DEFAULT_SCAN_USER_ID,
  type HouseholdSession,
  SCANNER_CONFIG_PATH,
  SCANNER_MODE_PATH,
  SCANS_PATH,
  SESSION_PATH,
  type SetScannerModeResponse,
  scanCommitPath,
  scansQuery,
} from './contract';
import type {
  BatchCommitResponse,
  PantryLinkSuggestion,
  ScanDirection,
  ScanEntry,
  ScannerConfig,
} from '../../../frontend/src/types';

// The public hostname's login name. See deploy/README.md.
export const HOUSEHOLD_USERNAME = 'pantry';

export class ApiError extends Error {
  readonly status: number;
  readonly code: string;
  readonly suggestions: PantryLinkSuggestion[];

  constructor(status: number, message: string, code = '', suggestions: PantryLinkSuggestion[] = []) {
    super(message);
    this.name = 'ApiError';
    this.status = status;
    this.code = code;
    this.suggestions = suggestions;
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
  commitScan: (id: string, linkProductId?: string) => Promise<ScanEntry>;
  batchCommit: (ids: string[]) => Promise<BatchCommitResponse & { failedCodes?: string[] }>;
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
    } catch {
      throw new ApiError(0, 'Stockbot did not answer at that address.');
    }

    if (!res.ok) {
      if (res.status === 401) options.onUnauthorized?.();
      const parsed = await readError(res);
      const message = res.status === 401 ? 'That password was rejected.' : parsed.message;
      throw new ApiError(res.status, message, parsed.code, parsed.suggestions);
    }

    const text = await res.text();
    if (text === '') return undefined as T;
    try {
      return JSON.parse(text) as T;
    } catch {
      throw new ApiError(res.status, 'Stockbot sent a response that could not be read.');
    }
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
      return request<ScanEntry>(scanCommitPath(id), {
        method: 'POST',
        body: JSON.stringify(body),
      });
    },
    batchCommit(ids) {
      const body: BatchCommitInput = { scanEntryIds: ids, commit: true };
      return request<BatchCommitResponse>(BATCH_COMMIT_PATH, {
        method: 'POST',
        body: JSON.stringify(body),
      });
    },
  };
}

async function readError(res: Response): Promise<{ message: string; code: string; suggestions: PantryLinkSuggestion[] }> {
  try {
    const body = (await res.json()) as { error?: string; code?: string; suggestions?: PantryLinkSuggestion[] };
    return {
      message: body.error || res.statusText || 'The request failed.',
      code: body.code ?? '',
      suggestions: body.suggestions ?? [],
    };
  } catch {
    return { message: res.statusText || 'The request failed.', code: '', suggestions: [] };
  }
}
