import { describe, expect, it } from 'vitest';
import type { KrogerProviderDisabled } from './contract';
import type { ProviderInfo } from './types';
import {
  isKrogerProviderDisabled,
  krogerDisabledOnProviderList,
  providerListEntries,
  type ProviderListEntry,
} from './providerList';

const krogerProvider = (): ProviderInfo => ({
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
});

describe('B11 KrogerProviderDisabled on GET /api/providers', () => {
  it('narrows disabled Kroger rows before reading ProviderInfo fields', () => {
    const row = { id: 'kroger' as const, displayName: 'Kroger', status: 'disabled' as const };
    expect(isKrogerProviderDisabled(row)).toBe(true);
    expect(() => {
      if (isKrogerProviderDisabled(row)) {
        void row.displayName;
        return;
      }
      const full = row as ProviderInfo;
      void full.credentialsConfigured;
    }).not.toThrow();
  });

  it('filters disabled Kroger out of active provider entries', () => {
    const disabled: KrogerProviderDisabled = { id: 'kroger', displayName: 'Kroger', status: 'disabled' };
    const list: ProviderListEntry[] = [krogerProvider(), disabled];
    expect(krogerDisabledOnProviderList(list)).toBe(true);
    expect(providerListEntries(list)).toHaveLength(1);
    expect(providerListEntries(list)[0]?.credentialsConfigured).toBe(true);
  });
});
