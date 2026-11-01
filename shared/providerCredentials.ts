import { inlineFailure } from './contract';
import { provisionCartTarget, provisionStoreLabel } from './provisionExportUX';
import type { ProviderInfo } from './types';

export const PROVIDER_DISCONNECT_FAILURE = "Couldn't disconnect. Try again.";

export const PROVIDER_APP_CREDENTIALS_FORBIDDEN_MESSAGE =
  'Only the server admin can change the Kroger app keys.';

/**
 * OAuth app keys (client id/secret) — not a household connect/disconnect flow.
 * Admin-only: the server writes these keys for every household, and a non-admin
 * in the primary household still sees source "saved"/"environment", so the
 * credential source cannot stand in for the admin check.
 */
export function canManageProviderAppCredentials(
  provider: ProviderInfo,
  isServerAdmin: boolean,
): boolean {
  return isServerAdmin && provider.capabilities.auth === 'oauth2_authorization_code';
}

export function providerAppCredentialsAccessDenied(error: unknown): boolean {
  if (error !== null && typeof error === 'object' && 'code' in error) {
    return (error as { code: string }).code === 'forbidden_operator';
  }
  return false;
}

/** Visible provider name in disconnect copy (falls back to "your store"). */
export function providerDisconnectStoreLabel(displayName: string | undefined): string {
  return provisionStoreLabel(displayName);
}

/** Title for the non-admin disconnect-only connection dialog. */
export function providerDisconnectDialogTitle(displayName: string | undefined): string {
  return `Disconnect ${providerDisconnectStoreLabel(displayName)}?`;
}

/**
 * Body copy for the non-admin disconnect-only dialog.
 * OAuth provider connections are stored per household (see internal/cart/connection/connection.go).
 */
export function providerDisconnectDialogBody(displayName: string | undefined): string {
  const storeLabel = providerDisconnectStoreLabel(displayName);
  const account = storeLabel === 'your store'
    ? 'your store account'
    : `your ${storeLabel} account`;
  const cart = provisionCartTarget(storeLabel);
  return `This disconnects ${account} for everyone in your household. Nothing on your list is deleted. Sending to ${cart} stops until someone reconnects.`;
}

/** Disconnect failure copy when the server gives no message. */
export function providerDisconnectFailureMessage(error: unknown): string {
  if (error !== null && typeof error === 'object' && 'error' in error) {
    const body = (error as { error?: unknown }).error;
    if (typeof body === 'string' && body.trim() !== '') return body.trim();
  }
  const message = inlineFailure(error, PROVIDER_DISCONNECT_FAILURE);
  if (message === null || message.trim() === '') return PROVIDER_DISCONNECT_FAILURE;
  return message;
}
