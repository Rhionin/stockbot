import type { ProviderInfo } from './types';

export const PROVIDER_APP_CREDENTIALS_FORBIDDEN_MESSAGE =
  'Only the server admin can change the Kroger app keys.';

/** OAuth app keys (client id/secret) — not a household connect/disconnect flow. */
export function canManageProviderAppCredentials(
  provider: ProviderInfo,
  isServerAdmin: boolean,
): boolean {
  if (provider.capabilities.auth !== 'oauth2_authorization_code') {
    return false;
  }
  if (isServerAdmin) {
    return true;
  }
  const source = provider.credentials?.source;
  return source !== undefined && source !== 'none';
}

export function providerAppCredentialsAccessDenied(error: unknown): boolean {
  if (error !== null && typeof error === 'object' && 'code' in error) {
    return (error as { code: string }).code === 'forbidden_operator';
  }
  return false;
}
