import { provisionReasonUXCategory } from './contract';
import type {
  ProvisionEntryResult,
  ProvisionReport,
  ProvisionResultStatus,
  ShoppingListExportResultEntry,
} from './types';

/** Visible store name in cart-send copy. */
export function provisionStoreLabel(displayName: string | undefined): string {
  const trimmed = displayName?.trim();
  return trimmed !== undefined && trimmed !== '' ? trimmed : 'your store';
}

const UNKNOWN_STORE = 'your store';

/** Store label for the start of a sentence ("Your store isn't responding…"). */
export function provisionStoreLabelSentence(storeLabel: string): string {
  return storeLabel === UNKNOWN_STORE ? 'Your store' : storeLabel;
}

/** "your Smith's account" vs "your store account" (never "your your store"). */
function yourStore(storeLabel: string, noun: string): string {
  return storeLabel === UNKNOWN_STORE ? `your store ${noun}` : `your ${storeLabel} ${noun}`;
}

/** "your Kroger cart" vs "your store cart" when the name is unknown. */
export function provisionCartTarget(storeLabel: string): string {
  return storeLabel === UNKNOWN_STORE ? 'your store cart' : `your ${storeLabel} cart`;
}

export const PROVISION_RESULT_PANEL_LABEL = 'Cart send result';

/** Collapsed list length before Show all in the couldn't-be-sent section. */
export const PROVISION_SHOW_ALL_THRESHOLD = 10;

/** Collapsed list length before Show all under Sent. */
export const PROVISION_SENT_SHOW_ALL_THRESHOLD = 5;

/** Most rows any one section renders, even after Show all. */
export const PROVISION_ROW_CAP = 50;

export const PROVISION_CHECK_IN_CART = "It's in my cart";
export const PROVISION_CHECK_NOT_IN_CART = "It's not";
export const PROVISION_MARKED_IN_CART = 'Marked as in your cart';
export const PROVISION_MARKED_NOT_IN_CART = 'Marked as not in your cart';
export const PROVISION_UNDO_LABEL = 'Undo';

export function provisionCheckInCartAria(itemName: string): string {
  return `${PROVISION_CHECK_IN_CART}: ${itemName}`;
}

export function provisionCheckNotInCartAria(itemName: string): string {
  return `${PROVISION_CHECK_NOT_IN_CART}: ${itemName}`;
}

export function provisionSendButtonLabel(storeLabel: string): string {
  return `Send to ${storeLabel}`;
}

export function provisionSendHint(storeLabel: string): string {
  return `Sends these items to ${provisionCartTarget(storeLabel)}.`;
}

export function provisionEmptyListNote(storeLabel: string): string {
  return `Build the list before sending it to ${storeLabel}.`;
}

export function provisionSectionHeadingSent(n: number): string {
  return `Sent (${n})`;
}

export function provisionSectionHeadingCouldntSend(m: number): string {
  return `Couldn't be sent (${m})`;
}

export function provisionSectionHeadingCheck(u: number): string {
  return `Check these in your cart (${u})`;
}

/** Maps each per-line status to the UI section it belongs in (#481 / #485 UX). */
export type ProvisionResultGroup = 'not_added' | 'not_confirmed' | 'sent_or_added';

export function provisionStatusGroup(status: ProvisionResultStatus): ProvisionResultGroup | null {
  if (status === 'skipped' || status === 'failed') return 'not_added';
  if (status === 'unknown') return 'not_confirmed';
  if (status === 'sent' || status === 'added') return 'sent_or_added';
  return null;
}

/** Row-level grouping; `no_per_item_result` belongs in the check section, not failures. */
export function provisionRowGroup(row: ShoppingListExportResultEntry): ProvisionResultGroup | null {
  if (row.reason === 'no_per_item_result') return 'not_confirmed';
  return provisionStatusGroup(row.status);
}

export function provisionReasonText(storeLabel: string, reason: string | undefined): string {
  if (reason === 'unavailable') {
    return `Not available at ${storeLabel} right now`;
  }
  switch (provisionReasonUXCategory(reason)) {
    case 'no_barcode':
      return 'No barcode saved for this item';
    case 'no_match':
      return `Couldn't find this at ${storeLabel}`;
    case 'rejected':
      return `${provisionStoreLabelSentence(storeLabel)} didn't accept this item`;
    case 'error':
      return 'Something went wrong sending this item';
    default:
      return 'Something went wrong sending this item';
  }
}

function failedNeedsReconnectSend(
  row: Pick<ShoppingListExportResultEntry, 'status' | 'reason'>,
  reconnectRequired: boolean,
): boolean {
  if (row.status !== 'failed') return false;
  if (row.reason === 'reconnect_required') return true;
  if (!reconnectRequired) return false;
  return row.reason === undefined;
}

export function provisionItemReasonLine(
  itemName: string,
  storeLabel: string,
  reason: string | undefined,
  row?: Pick<ShoppingListExportResultEntry, 'status' | 'reason'>,
  reconnectRequired = false,
): string {
  if (row !== undefined && failedNeedsReconnectSend(row, reconnectRequired)) {
    return `${itemName}: Reconnect ${storeLabel} to send this`;
  }
  return `${itemName}: ${provisionReasonText(storeLabel, reason)}`;
}

export type ProvisionWholeRequestError = 'provider_unavailable' | 'reconnect_required';

/** Summary when N and U are both zero (per-item failures only). */
export const PROVISION_NOTHING_SENT_LINE = 'Nothing was sent to your cart.';

/** Undo window before a check-row choice is sent to the server (the ledger advance can't be reversed). */
export const PROVISION_RESOLVE_UNDO_MS = 10_000;

/** HTTP 502 `provider_unavailable` envelope when nothing was sent. */
export function provisionProviderUnavailableEnvelope(storeLabel: string): string {
  const responder = storeLabel === UNKNOWN_STORE ? 'Your store' : storeLabel;
  return `Nothing was sent. ${responder} isn't responding right now.`;
}

/** Partial send after reconnect-class failure, above the Reconnect control. */
export function provisionPartialReconnectPrompt(storeLabel: string): string {
  const accountLead = storeLabel === UNKNOWN_STORE
    ? 'Your store account'
    : `Your ${storeLabel} account`;
  return `${accountLead} disconnected before everything was sent. Reconnect, then send again to finish.`;
}

export const PROVISION_RESOLVE_FAILED = "Couldn't save that. Try again.";

export function provisionUndoAria(itemName: string): string {
  return `Undo: ${itemName}`;
}

export function provisionNothingSentReconnectLabel(storeLabel: string): string {
  return `Reconnect ${storeLabel}`;
}

function legacyReportError(report: ProvisionReport): string | undefined {
  const error = (report as ProvisionReport & { error?: string }).error;
  return typeof error === 'string' ? error : undefined;
}

/** Legacy HTTP 200 body with top-level `error` (pre-#484 contract). Final API uses 409/502 instead. */
export function provisionLegacyNothingSentError(report: ProvisionReport): ProvisionWholeRequestError | undefined {
  if (!isWholeRequestError(legacyReportError(report))) return undefined;
  const rows = modernResults(report);
  if (rows !== null && rows.some((row) => provisionRowGroup(row) === 'sent_or_added')) {
    return undefined;
  }
  if ((report.sentCount ?? 0) > 0 || (report.addedCount ?? 0) > 0) return undefined;
  if (rows === null && (report.exported ?? 0) > 0) return undefined;
  return legacyReportError(report) as ProvisionWholeRequestError;
}

export function provisionWholeRequestMessage(
  code: ProvisionWholeRequestError,
  storeLabel: string,
): string {
  if (code === 'reconnect_required') {
    return `Couldn't connect to ${yourStore(storeLabel, 'account')}. Nothing was sent.`;
  }
  return provisionProviderUnavailableEnvelope(storeLabel);
}

export function provisionWholeRequestActionLabel(
  code: ProvisionWholeRequestError,
  storeLabel: string,
): string {
  if (code === 'reconnect_required') {
    return `Reconnect ${storeLabel}`;
  }
  return 'Try again';
}

export interface ProvisionSummaryCounts {
  N: number;
  M: number;
  U: number;
}

export interface ProvisionSummaryCopy {
  lines: string[];
  cartConfirmLine?: string;
  openCartLabel?: string;
}

function sentSummaryLine(n: number, storeLabel: string): string {
  const target = provisionCartTarget(storeLabel);
  return n === 1 ? `1 item sent to ${target}.` : `${n} items sent to ${target}.`;
}

function couldntSendSummaryLine(m: number): string {
  return m === 1 ? "1 couldn't be sent." : `${m} couldn't be sent.`;
}

function mightNotReachSummaryLine(u: number): string {
  return u === 1
    ? '1 might not have reached your cart.'
    : `${u} might not have reached your cart.`;
}

/** Summary lines for the cart-send result panel (#485 UX). Never emits "0 items sent". */
export function provisionSummaryCopy(
  counts: ProvisionSummaryCounts,
  storeLabel: string,
): ProvisionSummaryCopy {
  const { N, M, U } = counts;
  const lines: string[] = [];

  if (N === 0 && U === 0) {
    lines.push('Nothing was sent to your cart.');
  }
  if (N >= 1) {
    lines.push(sentSummaryLine(N, storeLabel));
  }
  if (M >= 1) {
    lines.push(couldntSendSummaryLine(M));
  }
  if (U >= 1) {
    lines.push(mightNotReachSummaryLine(U));
  }

  const cartConfirmLine = N + U >= 1
    ? `${provisionStoreLabelSentence(storeLabel)} doesn't confirm each item, so check your cart to make sure everything is there.`
    : undefined;

  return {
    lines,
    cartConfirmLine,
    openCartLabel: N >= 1 ? `Open ${storeLabel} cart` : undefined,
  };
}

export interface ProvisionResultGroups {
  sentRows: ShoppingListExportResultEntry[];
  notAdded: ShoppingListExportResultEntry[];
  check: ShoppingListExportResultEntry[];
  N: number;
  M: number;
  U: number;
  wholeRequestError?: ProvisionWholeRequestError;
}

function isWholeRequestError(error: string | undefined): error is ProvisionWholeRequestError {
  return error === 'provider_unavailable' || error === 'reconnect_required';
}

function modernResults(report: ProvisionReport): ShoppingListExportResultEntry[] | null {
  if (Array.isArray(report.results) && report.results.length > 0) {
    return report.results;
  }
  return null;
}

function legacyNotAdded(report: ProvisionReport): ShoppingListExportResultEntry[] {
  return (report.failedItems ?? []).map((name) => ({
    entryId: '',
    itemId: '',
    name,
    quantity: 1,
    status: 'failed',
  }));
}

function legacyCheck(report: ProvisionReport): ShoppingListExportResultEntry[] {
  const fromEntries = (report.entries ?? [])
    .filter((entry) => entry.outcome === 'unknown')
    .map(entryRowToResult);
  if (fromEntries.length > 0) {
    return fromEntries;
  }
  return (report.unknownItems ?? []).map((name) => ({
    entryId: '',
    itemId: '',
    name,
    quantity: 1,
    status: 'unknown',
  }));
}

function entryRowToResult(entry: ProvisionEntryResult): ShoppingListExportResultEntry {
  return {
    entryId: entry.entryId,
    itemId: entry.itemId,
    name: entry.name,
    quantity: entry.quantity,
    status: entry.outcome === 'unknown' ? 'unknown' : 'failed',
    reason: entry.outcomeReason as ShoppingListExportResultEntry['reason'],
    detail: entry.detail,
  };
}

function partitionRows(rows: ShoppingListExportResultEntry[]) {
  const sentRows: ShoppingListExportResultEntry[] = [];
  const notAdded: ShoppingListExportResultEntry[] = [];
  const check: ShoppingListExportResultEntry[] = [];
  for (const row of rows) {
    const group = provisionRowGroup(row);
    if (group === 'sent_or_added') sentRows.push(row);
    else if (group === 'not_added') notAdded.push(row);
    else if (group === 'not_confirmed') check.push(row);
  }
  return { sentRows, notAdded, check };
}

function countSentFromReport(report: ProvisionReport, sentRows: ShoppingListExportResultEntry[]): number {
  if (sentRows.length > 0) return sentRows.length;
  if (typeof report.sentCount === 'number' || typeof report.addedCount === 'number') {
    return (report.sentCount ?? 0) + (report.addedCount ?? 0);
  }
  return report.exported ?? 0;
}

/** Groups export rows for web/mobile UI (#481). Falls back to legacy lists when results[] is absent. */
export function groupProvisionResults(report: ProvisionReport): ProvisionResultGroups {
  const rows = modernResults(report);
  let sentRows: ShoppingListExportResultEntry[];
  let notAdded: ShoppingListExportResultEntry[];
  let check: ShoppingListExportResultEntry[];

  if (rows !== null) {
    ({ sentRows, notAdded, check } = partitionRows(rows));
  } else {
    sentRows = [];
    notAdded = legacyNotAdded(report);
    check = legacyCheck(report);
    // Legacy servers only expose a count, not names: N comes from `exported`, no rows are invented.
  }

  const N = countSentFromReport(report, sentRows);
  const M = notAdded.length;
  const U = check.length;

  const blocking = provisionLegacyNothingSentError(report);
  if (blocking !== undefined) {
    return {
      sentRows: [],
      notAdded: [],
      check: [],
      N: 0,
      M: 0,
      U: 0,
      wholeRequestError: blocking,
    };
  }

  return {
    sentRows,
    notAdded,
    check,
    N,
    M,
    U,
  };
}

/** @deprecated Use groups.N */
export function provisionSentTotal(groups: ProvisionResultGroups): number {
  return groups.N;
}
