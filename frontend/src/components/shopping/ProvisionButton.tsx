import { useState } from 'react';
import { Button, Group, Stack, Text } from '@mantine/core';
import { exportShoppingList, resolveUnknownProvision } from '../../api/client';
import type { ProviderInfo, ProvisionReport, ShoppingListEntry } from '@stockbot/shared/types';
import { provisionQuantity, summarizeNames } from './outcome';
import { inlineFailure } from '@stockbot/shared/contract';

export interface ProvisionButtonProps {
  provider: ProviderInfo | null;
  entries: ShoppingListEntry[];
  explainEmpty?: boolean;
  onFinished: () => void;
}

const storeIsReady = (provider: ProviderInfo | null) => (
  provider !== null
  && provider.credentialsConfigured
  && (provider.connectionState === 'connected' || provider.connectionState === 'not_required')
);

const canProvision = (provider: ProviderInfo | null, entries: ShoppingListEntry[], inFlight: boolean) => {
  if (inFlight) return false;
  if (!entries.some((entry) => provisionQuantity(entry) >= 1)) return false;
  // An unconfigured store still accepts the export. The server confirms
  // nothing and returns the planned lines.
  if (provider === null || !provider.credentialsConfigured) return true;
  return storeIsReady(provider);
};

export const ProvisionButton = ({ provider, entries, explainEmpty = false, onFinished }: ProvisionButtonProps) => {
  const [inFlight, setInFlight] = useState(false);
  const [report, setReport] = useState<ProvisionReport | null>(null);
  const [error, setError] = useState('');

  const staged = entries.some((entry) => provisionQuantity(entry) >= 1);
  const enabled = canProvision(provider, entries, inFlight);
  const label = 'Send to Kroger';

  const provision = async () => {
    setInFlight(true);
    setError('');
    setReport(null);
    try {
      const providerId = provider !== null && provider.credentialsConfigured ? provider.id : undefined;
      const result = await exportShoppingList(providerId);
      setReport(result);
      onFinished();
    } catch (requestError) {
      setReport(null);
      setError(inlineFailure(requestError, 'These items could not be sent to Kroger.') ?? '');
    } finally {
      setInFlight(false);
    }
  };

  const resolve = async (entryId: string, reachedProvider: boolean) => {
    if (provider === null || entryId === '') return;
    setError('');
    try {
      await resolveUnknownProvision(entryId, provider.id, reachedProvider);
      onFinished();
    } catch (requestError) {
      setError(inlineFailure(requestError, 'Unable to record that outcome.') ?? '');
    }
  };

  const failed = summarizeNames(report?.failedItems ?? []);
  const unknown = summarizeNames(report?.unknownItems ?? []);
  const nothingFailed = (report?.failedItems?.length ?? 0) === 0 && (report?.unknownItems?.length ?? 0) === 0;
  const showEmpty = explainEmpty && !staged;
  const showUnconfigured = provider !== null && !provider.credentialsConfigured;
  const showQuiet = report !== null && error === '' && nothingFailed && report.exported === 0 && (provider === null || !provider.credentialsConfigured);
  const showSent = report !== null && error === '' && nothingFailed && (report.exported > 0 || (provider !== null && provider.credentialsConfigured));
  const showFailed = report !== null && failed.shown.length > 0;
  const showUnknown = report !== null && unknown.shown.length > 0;
  const hasNotices = showEmpty || showUnconfigured || error !== '' || showQuiet || showSent || showFailed || showUnknown;

  return (
    <div className="provision-control">
      <Button className="shopping-send" loading={inFlight} disabled={!enabled} onClick={() => void provision()}>
        {label}
      </Button>
      <Text className="shopping-send-hint" size="sm" c="dimmed">Adds these items to your Kroger cart.</Text>
      {hasNotices && (
        <Stack className="provision-notices" gap={4}>
          {showEmpty && (
            <Text size="sm">Build the list before sending it to Kroger.</Text>
          )}
          {showUnconfigured && provider !== null && (
            <Text size="sm" c="dimmed">{provider.displayName} is unconfigured.</Text>
          )}
          {error !== '' && <Text c="red" size="sm">{error}</Text>}
          {showQuiet && (
            <Text size="sm">Nothing was sent. Connect a store before sending these items to Kroger.</Text>
          )}
          {showSent && report !== null && (
            <Text size="sm">{report.exported} item{report.exported === 1 ? '' : 's'} sent to Kroger.</Text>
          )}
          {showFailed && (
            <Text size="sm" c="red">
              Could not add: {failed.shown.join(', ')}
              {failed.overflow > 0 ? `, and ${failed.overflow} more` : ''}. These items remain on the list.
            </Text>
          )}
          {showUnknown && report !== null && (
            <Stack gap={4}>
              <Text size="sm">
                Unconfirmed: {unknown.shown.join(', ')}
                {unknown.overflow > 0 ? `, and ${unknown.overflow} more` : ''}.
              </Text>
              {(report.entries ?? [])
                .filter((entry) => entry.outcome === 'unknown' && entry.entryId !== '')
                .slice(0, 50)
                .map((entry) => (
                  <Group key={entry.entryId} gap="xs">
                    <Text size="sm">{entry.name}</Text>
                    <Button size="xs" variant="light" onClick={() => void resolve(entry.entryId, true)}>
                      It reached {provider?.displayName ?? 'Kroger'}
                    </Button>
                    <Button size="xs" variant="default" onClick={() => void resolve(entry.entryId, false)}>
                      It did not
                    </Button>
                  </Group>
                ))}
            </Stack>
          )}
        </Stack>
      )}
    </div>
  );
};
