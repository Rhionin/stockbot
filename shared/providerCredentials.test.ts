import { describe, expect, it } from 'vitest';
import type { ProviderInfo } from './types';
import {
  canManageProviderAppCredentials,
  providerAppCredentialsAccessDenied,
  PROVIDER_APP_CREDENTIALS_FORBIDDEN_MESSAGE,
} from './providerCredentials';

const kroger = (overrides: Partial<ProviderInfo> = {}): ProviderInfo => ({
  id: 'kroger',
  displayName: 'Kroger',
  capabilities: {
    auth: 'oauth2_authorization_code',
    delivery: 'server_push',
    confirmation: 'per_request',
    mutation: 'add_only',
    identity: 'derived',
  },
  connectionState: 'disconnected',
  credentialsConfigured: true,
  ...overrides,
});

describe('canManageProviderAppCredentials', () => {
  it('lets the server admin manage keys even when the list hides credential source', () => {
    expect(canManageProviderAppCredentials(kroger({
      credentials: {
        clientId: '',
        redirectUri: '',
        modality: 'PICKUP',
        secretSet: false,
        source: 'none',
      },
    }), true)).toBe(true);
  });

  it('hides keys from Test Household members when the server reports source none', () => {
    expect(canManageProviderAppCredentials(kroger({
      credentials: {
        clientId: '',
        redirectUri: '',
        modality: 'PICKUP',
        secretSet: false,
        source: 'none',
      },
    }), false)).toBe(false);
  });

  it('hides keys from a non-admin primary household member whose source is saved', () => {
    expect(canManageProviderAppCredentials(kroger({
      credentials: {
        clientId: 'fixture-client',
        redirectUri: 'https://test-household.example/cb',
        modality: 'PICKUP',
        secretSet: true,
        source: 'saved',
      },
    }), false)).toBe(false);
  });

  it('hides keys from non-admins when source is environment', () => {
    expect(canManageProviderAppCredentials(kroger({
      credentials: {
        clientId: 'client',
        redirectUri: 'https://example/cb',
        modality: 'PICKUP',
        secretSet: true,
        source: 'environment',
      },
    }), false)).toBe(false);
  });

  it('never offers app keys for a provider without OAuth', () => {
    expect(canManageProviderAppCredentials(kroger({
      capabilities: {
        auth: 'none',
        delivery: 'server_push',
        confirmation: 'per_request',
        mutation: 'add_only',
        identity: 'derived',
      },
    }), true)).toBe(false);
  });
});

describe('providerAppCredentialsAccessDenied', () => {
  it('names the Kroger admin-only message for tests', () => {
    expect(PROVIDER_APP_CREDENTIALS_FORBIDDEN_MESSAGE).toContain('admin');
    expect(PROVIDER_APP_CREDENTIALS_FORBIDDEN_MESSAGE).not.toMatch(/\boperator\b/i);
  });

  it('detects forbidden_operator failures', () => {
    expect(providerAppCredentialsAccessDenied({ code: 'forbidden_operator' })).toBe(true);
    expect(providerAppCredentialsAccessDenied({ code: 'validation' })).toBe(false);
  });
});
