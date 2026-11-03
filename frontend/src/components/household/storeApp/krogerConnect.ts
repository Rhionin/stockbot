import { krogerAppCopy } from '@stockbot/shared/krogerAppCopy';
import { authorizeProvider } from '../../../api/client';
import { mapKrogerAppApiError, storeNameCtx } from './krogerAppMessages';

export async function startKrogerConnect(returnTo: 'household' | 'shopping'): Promise<void> {
  const { authorizationUrl } = await authorizeProvider('kroger', returnTo);
  window.location.assign(authorizationUrl);
}

export type KrogerConnectResult =
  | { ok: true }
  | { ok: false; kind: 'store_disabled' | 'not_configured' | 'failed'; message?: string };

export async function connectKrogerAccount(
  returnTo: 'household' | 'shopping',
  linkedStoreName?: string | null,
): Promise<KrogerConnectResult> {
  try {
    await startKrogerConnect(returnTo);
    return { ok: true };
  } catch (error) {
    const mapped = mapKrogerAppApiError(error);
    if (mapped.kind === 'store_disabled') {
      return { ok: false, kind: 'store_disabled' };
    }
    if (mapped.kind === 'not_configured') {
      return { ok: false, kind: 'not_configured' };
    }
    const ctx = storeNameCtx(linkedStoreName);
    const message = ctx.store === 'your store'
      ? krogerAppCopy.errConnectFailedFallback
      : krogerAppCopy.errConnectFailed.replaceAll('{store}', ctx.store);
    return { ok: false, kind: 'failed', message };
  }
}
