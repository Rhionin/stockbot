import type { KrogerProviderDisabled } from './contract';
import type { ProviderInfo } from './types';

export type { KrogerProviderDisabled };

export type ProviderListEntry = ProviderInfo | KrogerProviderDisabled;

export function isKrogerProviderDisabled(
  entry: ProviderListEntry,
): entry is KrogerProviderDisabled {
  return entry.id === 'kroger' && 'status' in entry && entry.status === 'disabled';
}

export function providerListEntries(entries: ProviderListEntry[]): ProviderInfo[] {
  return entries.filter((entry): entry is ProviderInfo => !isKrogerProviderDisabled(entry));
}

export function krogerDisabledOnProviderList(entries: ProviderListEntry[]): boolean {
  return entries.some(isKrogerProviderDisabled);
}
