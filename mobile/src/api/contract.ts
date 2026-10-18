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

// Commit outcomes. Appended after the shared routes so this file keeps that
// prefix and adds the phone's half.

export const LINK_OFFER_HEADING = 'Which item is this?';

export const NO_CLOSE_MATCH = 'No close match found.';

export const LINK_TARGET_GONE = 'link_target_gone';

export const BATCH_FAILURE_FALLBACK = 'Some scans could not be confirmed.';

// The only stock-out count the server has to send. A rename is this one edit.
export const REMOVED_COUNT_FIELD = 'removedCount';

export interface CommitConflictBehavior {
  linkOffer: boolean;
  // True only for no_item, and only the empty suggestion list uses the sentence.
  showEmptyLine: boolean;
  showMessage: boolean;
  useSuggestions: boolean;
  refreshSuggestions: boolean;
  refreshInstances: boolean;
}

// Branch on the server's code, whatever the HTTP status. An absent or unknown
// code shows the server text and leaves the pick list alone.
export function commitConflictBehavior(code: string | undefined): CommitConflictBehavior {
  switch (code) {
    case 'no_item':
      return {
        linkOffer: true,
        showEmptyLine: true,
        showMessage: false,
        useSuggestions: true,
        refreshSuggestions: false,
        refreshInstances: false,
      };
    case 'product_unresolved':
      return {
        linkOffer: true,
        showEmptyLine: false,
        showMessage: true,
        useSuggestions: false,
        refreshSuggestions: false,
        refreshInstances: false,
      };
    case 'instance_gone':
      return {
        linkOffer: false,
        showEmptyLine: false,
        showMessage: true,
        useSuggestions: false,
        refreshSuggestions: false,
        refreshInstances: true,
      };
    case 'none_on_hand':
      return {
        linkOffer: false,
        showEmptyLine: false,
        showMessage: true,
        useSuggestions: false,
        refreshSuggestions: false,
        refreshInstances: false,
      };
    case LINK_TARGET_GONE:
      return {
        linkOffer: true,
        showEmptyLine: false,
        showMessage: true,
        useSuggestions: true,
        refreshSuggestions: true,
        refreshInstances: false,
      };
    default:
      return {
        linkOffer: false,
        showEmptyLine: false,
        showMessage: true,
        useSuggestions: false,
        refreshSuggestions: false,
        refreshInstances: false,
      };
  }
}

export interface BatchFailureNotice {
  id: string;
  message: string;
  code: string;
}

// Pair each failed id with the server's reason and, when present, the code at
// the same index. failedCodes is additive and may be missing.
export function batchFailureNotices(response: {
  failedIds?: string[];
  failedReasons?: string[];
  failedCodes?: string[];
}): BatchFailureNotice[] {
  const ids = response.failedIds ?? [];
  const reasons = response.failedReasons ?? [];
  const codes = response.failedCodes ?? [];
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

// Requested count is the card's own unitCount. removedCount below 1, missing,
// or already at that count says nothing.
export function formatPartialRemoval(unitCount: number, body: object | null | undefined): string | null {
  if (body === null || body === undefined) return null;
  const removedCount = (body as Record<string, unknown>)[REMOVED_COUNT_FIELD];
  if (typeof removedCount !== 'number' || !Number.isInteger(removedCount)) return null;
  if (removedCount < 1 || removedCount >= unitCount) return null;
  if (removedCount === 1) return 'Only 1 was on hand, so 1 was taken out.';
  return `Only ${removedCount} were on hand, so ${removedCount} were taken out.`;
}
