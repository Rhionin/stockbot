import type { ProviderInfo } from './types';

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

/** Title for the non-admin disconnect-only connection dialog. */
export function providerDisconnectDialogTitle(displayName: string): string {
  return `Disconnect ${displayName}?`;
}

/**
 * Body copy for the non-admin disconnect-only dialog.
 * OAuth provider connections are stored per household (see internal/cart/connection/connection.go).
 */
export function providerDisconnectDialogBody(displayName: string): string {
  return `This disconnects your ${displayName} account for everyone in your household. Nothing on your list is deleted. Sending to your ${displayName} cart stops until someone reconnects.`;
}
