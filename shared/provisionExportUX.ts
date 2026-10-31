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

/** "your Kroger cart" vs "your store cart" when the name is unknown. */
export function provisionCartTarget(storeLabel: string): string {
  return storeLabel === 'your store' ? 'your store cart' : `your ${storeLabel} cart`;
}

export const PROVISION_GROUP_NOT_ADDED = 'Not added';
export const PROVISION_GROUP_NOT_CONFIRMED = 'Not confirmed';
export const PROVISION_GROUP_SENT = 'Sent or added';

export const PROVISION_RESULT_PANEL_LABEL = 'Cart send result';

export const PROVISION_SHOW_ALL_THRESHOLD = 10;

/** Maps each per-line status to the UI section it belongs in (#481). */
export type ProvisionResultGroup = 'not_added' | 'not_confirmed' | 'sent_or_added';

export function provisionStatusGroup(status: ProvisionResultStatus): ProvisionResultGroup | null {
  if (status === 'skipped' || status === 'failed') return 'not_added';
  if (status === 'unknown') return 'not_confirmed';
  if (status === 'sent' || status === 'added') return 'sent_or_added';
  return null;
}

export function provisionReasonText(storeLabel: string, reason: string | undefined): string {
  switch (provisionReasonUXCategory(reason)) {
    case 'no_barcode':
      return 'No barcode saved for this item';
    case 'no_match':
      return `Couldn't find this at ${storeLabel}`;
    case 'rejected':
      return `${storeLabel} didn't accept this item`;
    case 'error':
      return 'Something went wrong sending this item';
    default:
      return 'Something went wrong sending this item';
  }
}

export function provisionItemReasonLine(itemName: string, storeLabel: string, reason: string | undefined): string {
  return `${itemName}: ${provisionReasonText(storeLabel, reason)}`;
}

export type ProvisionWholeRequestError = 'provider_unavailable' | 'reconnect_required';

export function provisionWholeRequestMessage(
  code: ProvisionWholeRequestError,
  storeLabel: string,
): string {
  if (code === 'reconnect_required') {
    return `Couldn't connect to your ${storeLabel} account. Nothing was sent.`;
  }
  return `${storeLabel} isn't responding right now. Nothing was sent.`;
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

export interface ProvisionSummaryCopy {
  primary: string;
  cartConfirmLine?: string;
  openCartLabel?: string;
}

export function provisionSummaryCopy(
  sentCount: number,
  couldNotSendCount: number,
  storeLabel: string,
): ProvisionSummaryCopy {
  const sentPhrase = sentCount === 1
    ? `1 item sent to ${provisionCartTarget(storeLabel)}.`
    : `${sentCount} items sent to ${provisionCartTarget(storeLabel)}.`;

  let primary: string;
  if (sentCount <= 0 && couldNotSendCount > 0) {
    const items = couldNotSendCount === 1 ? '1 item' : `${couldNotSendCount} items`;
    primary = `Nothing was sent to your cart. ${items} couldn't be sent:`;
  } else if (sentCount > 0 && couldNotSendCount > 0) {
    const blocked = couldNotSendCount === 1 ? '1' : String(couldNotSendCount);
    primary = `${sentPhrase} ${blocked} couldn't be sent:`;
  } else {
    primary = sentPhrase;
  }

  const cartConfirmLine = sentCount >= 1
    ? `${storeLabel} doesn't confirm each item, so check your cart to make sure everything is there.`
    : undefined;

  return {
    primary,
    cartConfirmLine,
    openCartLabel: sentCount >= 1 ? `Open ${storeLabel} cart` : undefined,
  };
}

export interface ProvisionResultGroups {
  sentCount: number;
  addedCount: number;
  notAdded: ShoppingListExportResultEntry[];
  notConfirmed: ShoppingListExportResultEntry[];
  couldNotSendCount: number;
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

function legacyNotConfirmed(report: ProvisionReport): ShoppingListExportResultEntry[] {
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

function countSent(report: ProvisionReport, rows: ShoppingListExportResultEntry[] | null): number {
  if (typeof report.sentCount === 'number') return report.sentCount;
  if (rows !== null) {
    return rows.filter((row) => row.status === 'sent').length;
  }
  return report.exported ?? 0;
}

function countAdded(report: ProvisionReport, rows: ShoppingListExportResultEntry[] | null): number {
  if (typeof report.addedCount === 'number') return report.addedCount;
  if (rows !== null) {
    return rows.filter((row) => row.status === 'added').length;
  }
  return 0;
}

/** Groups export rows for web/mobile UI (#481). Falls back to legacy lists when results[] is absent. */
export function groupProvisionResults(report: ProvisionReport): ProvisionResultGroups {
  const rows = modernResults(report);
  const notAdded = rows !== null
    ? rows.filter((row) => row.status === 'skipped' || row.status === 'failed')
    : legacyNotAdded(report);
  const notConfirmed = rows !== null
    ? rows.filter((row) => row.status === 'unknown')
    : legacyNotConfirmed(report);

  const sentCount = countSent(report, rows);
  const addedCount = countAdded(report, rows);
  const couldNotSendCount = notAdded.length;

  let wholeRequestError: ProvisionWholeRequestError | undefined;
  if (isWholeRequestError(report.error) && sentCount === 0 && addedCount === 0) {
    wholeRequestError = report.error;
  }

  return {
    sentCount,
    addedCount,
    notAdded,
    notConfirmed,
    couldNotSendCount,
    wholeRequestError,
  };
}

export function provisionSentTotal(groups: ProvisionResultGroups): number {
  return groups.sentCount + groups.addedCount;
}
