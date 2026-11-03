import { hostedErrorSentence } from '@stockbot/shared/contract';
import {
  krogerAppCopy,
  krogerAppErrMismatch,
  krogerAppErrMismatchSaved,
  storeNameContext,
  type StoreNameContext,
} from '@stockbot/shared/krogerAppCopy';
import { krogerLabels } from '@stockbot/shared/krogerLabels';
import type { KrogerAppTestCode } from '@stockbot/shared/types';
import { ApiError } from '../../../api/client';

export const redirectMismatchSaveEnabled =
  import.meta.env.VITE_KROGER_REDIRECT_MISMATCH_SAVE === 'true';

export function putFailureMessage(error: unknown): string {
  if (error instanceof ApiError) {
    if (error.code === 'store_app_keys_rejected') return krogerAppCopy.errWrongKeys;
    if (error.code === 'store_app_unreachable') return krogerAppCopy.errUnreachable;
    if (error.code !== '') return hostedErrorSentence(error.code);
  }
  return error instanceof Error ? error.message : krogerAppCopy.errSaveFailed;
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

export function shoppingKrogerLegacyMessage(): string {
  return krogerAppCopy.rowConnectedBodyFallback;
}

export function shoppingRedirectMismatchMessage(): string {
  return krogerAppCopy.errMismatchShop;
}

export function storeNameCtx(linkedStoreName: string | null | undefined): StoreNameContext {
  return storeNameContext(linkedStoreName);
}
