import {
  krogerAppCopy,
  krogerAppErrMismatch,
  krogerAppErrMismatchSaved,
  krogerAppRowNoChecks,
  storeNameContext,
  type StoreNameContext,
} from '@stockbot/shared/krogerAppCopy';
import { krogerLabels } from '@stockbot/shared/krogerLabels';
import type { KrogerAppTest, KrogerAppTestCode } from '@stockbot/shared/types';
import { OFFLINE_SENTENCE, ServerNoticeError, UnreachableError } from '@stockbot/shared/contract';
import { ApiError } from '../../../api/client';

export const redirectMismatchSaveEnabled =
  import.meta.env.VITE_KROGER_REDIRECT_MISMATCH_SAVE === 'true';

export type KrogerAppErrorHandling =
  | { kind: 'store_disabled' }
  | { kind: 'forbidden_role' }
  | { kind: 'not_configured' }
  | {
    kind: 'message';
    message: string;
    alert: boolean;
    amber?: boolean;
    goStep?: number;
  };

export function normalizeKrogerCredential(value: string): string {
  return value.trim().replace(/\s+/g, '');
}

export function mapKrogerAppApiError(error: unknown): KrogerAppErrorHandling {
  if (error instanceof ServerNoticeError || error instanceof UnreachableError) {
    return { kind: 'message', message: OFFLINE_SENTENCE, alert: true };
  }
  if (!(error instanceof ApiError)) {
    return { kind: 'message', message: krogerAppCopy.errSaveFailed, alert: true };
  }
  switch (error.code) {
    case 'store_disabled':
      return { kind: 'store_disabled' };
    case 'forbidden_role':
      return { kind: 'forbidden_role' };
    case 'kroger_not_connected':
      return { kind: 'not_configured' };
    case 'store_app_keys_rejected':
      return {
        kind: 'message',
        message: krogerAppCopy.errWrongKeys,
        alert: true,
      };
    case 'store_app_unreachable':
      return { kind: 'message', message: krogerAppCopy.errUnreachable, alert: true };
    case 'rate_limited':
      return { kind: 'message', message: krogerAppCopy.errTooMany, alert: false, amber: true };
    case 'validation':
      return { kind: 'message', message: krogerAppCopy.vInvalid, alert: true };
    default:
      return { kind: 'message', message: krogerAppCopy.errSaveFailed, alert: true };
  }
}

/** @deprecated use mapKrogerAppApiError */
export function putFailureMessage(error: unknown): string {
  const mapped = mapKrogerAppApiError(error);
  if (mapped.kind !== 'message') return '';
  return mapped.message;
}

export function mapTestAgainResult(
  result: KrogerAppTest,
  ctx: StoreNameContext,
): { message: string; alert: boolean; amber?: boolean } {
  if (result.ok && !result.code) {
    return { message: krogerAppCopy.testOk, alert: false };
  }
  if (result.ok && result.code === 'no_product_access') {
    return { message: krogerAppRowNoChecks(ctx), alert: false, amber: true };
  }
  if (!result.ok) {
    return {
      message: testCodeMessage(result.code, ctx, true),
      alert: true,
    };
  }
  return {
    message: testCodeMessage(result.code, ctx, true),
    alert: result.code !== 'no_product_access',
    amber: result.code === 'no_product_access',
  };
}

export function testCodeMessage(
  code: KrogerAppTestCode | undefined,
  ctx: StoreNameContext,
  replaceMode: boolean,
): string {
  switch (code) {
    case 'wrong_keys':
      return krogerAppCopy.errWrongKeys;
    case 'kroger_unreachable':
      return krogerAppCopy.errUnreachable;
    case 'redirect_mismatch':
      return replaceMode
        ? krogerAppErrMismatchSaved(replaceMode)
        : krogerAppErrMismatch();
    case 'no_product_access':
      return ctx.store === 'your store'
        ? krogerAppCopy.noteNoProductsFallback.replaceAll('{k.box.products}', `"${krogerLabels.boxProducts}"`)
        : krogerAppCopy.noteNoProducts
          .replaceAll('{store}', ctx.store)
          .replaceAll('{k.box.products}', `"${krogerLabels.boxProducts}"`);
    default:
      return krogerAppCopy.errSaveFailed;
  }
}

export function rowLoadFailureMessage(error: unknown): string {
  if (error instanceof ApiError && error.code === 'not_found') {
    return krogerAppCopy.rowLoadFailed;
  }
  return krogerAppCopy.rowLoadFailed;
}

export function removeFailureMessage(): string {
  return krogerAppCopy.rmFailed;
}

export function krogerCallbackMessage(
  outcome: string,
  ctx: StoreNameContext,
  isAdmin: boolean,
): string {
  switch (outcome) {
    case 'connected':
      return '';
    case 'cancelled':
      return krogerAppCopy.s7Cancelled;
    case 'redirect_mismatch':
      return isAdmin ? krogerAppErrMismatch() : krogerAppCopy.errMismatchMember;
    case 'failed':
      return ctx.store === 'your store'
        ? krogerAppCopy.errConnectFailedFallback
        : krogerAppCopy.errConnectFailed.replaceAll('{store}', ctx.store);
    default:
      return '';
  }
}

export function krogerCallbackIsError(outcome: string): boolean {
  return outcome === 'failed' || outcome === 'redirect_mismatch';
}

export function shoppingKrogerLegacyMessage(): string {
  return krogerAppCopy.rowConnectedBodyFallback;
}

export function shoppingRedirectMismatchMessage(): string {
  return krogerAppCopy.errMismatchShop;
}

export function storeNameCtx(linkedStoreName: string | null | undefined): StoreNameContext {
  return storeNameContext(linkedStoreName);
}

export function linkedCartStoreName(stores: { name: string; linkedProvider: string | null }[]): string | null {
  const linked = stores.find((row) => row.linkedProvider !== null);
  return linked?.name ?? null;
}
