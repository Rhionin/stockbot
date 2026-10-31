import { useState } from 'react';
import { Button, Loader, Stack, Text } from '@mantine/core';
import { authorizeProvider, exportShoppingList, resolveUnknownProvision } from '../../api/client';
import type { ProviderInfo, ProvisionReport, ShoppingListEntry } from '@stockbot/shared/types';
import { inlineFailure } from '@stockbot/shared/contract';
import { provisionCartTarget, provisionStoreLabel } from '@stockbot/shared/provisionExportUX';
import { provisionQuantity } from './outcome';
import { ProvisionResultPanel } from './ProvisionResultPanel';

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
  if (provider === null || !provider.credentialsConfigured) return true;
  return storeIsReady(provider);
};

export const ProvisionButton = ({ provider, entries, explainEmpty = false, onFinished }: ProvisionButtonProps) => {
  const [inFlight, setInFlight] = useState(false);
  const [report, setReport] = useState<ProvisionReport | null>(null);
  const [requestError, setRequestError] = useState('');
  const [cartUrl, setCartUrl] = useState<string | undefined>();

  const staged = entries.some((entry) => provisionQuantity(entry) >= 1);
  const enabled = canProvision(provider, entries, inFlight);
  const storeLabel = provisionStoreLabel(provider?.displayName);

  const provision = async () => {
    setInFlight(true);
    setRequestError('');
    setReport(null);
    setCartUrl(undefined);
    try {
      const providerId = provider !== null && provider.credentialsConfigured ? provider.id : undefined;
      const result = await exportShoppingList(providerId);
      setReport(result);
      const handoff = (result as ProvisionReport & { handoff?: { url?: string } }).handoff;
      if (handoff?.url) {
        setCartUrl(handoff.url);
      }
      onFinished();
    } catch (requestError) {
      setReport(null);
      setRequestError(inlineFailure(requestError, 'These items could not be sent to your store.') ?? '');
    } finally {
      setInFlight(false);
    }
  };

  const resolve = async (entryId: string, reachedProvider: boolean) => {
    if (provider === null || entryId === '') return;
    setRequestError('');
    try {
      await resolveUnknownProvision(entryId, provider.id, reachedProvider);
      onFinished();
    } catch (requestError) {
      setRequestError(inlineFailure(requestError, 'Unable to record that outcome.') ?? '');
    }
  };

  const reconnect = async () => {
    if (provider === null) return;
    setRequestError('');
    try {
      const { authorizationUrl } = await authorizeProvider(provider.id);
      window.location.assign(authorizationUrl);
    } catch (requestError) {
      setRequestError(inlineFailure(requestError, 'Unable to start the connection.') ?? '');
    }
  };

  const showEmpty = explainEmpty && !staged;
  const showUnconfigured = provider !== null && !provider.credentialsConfigured;
  const showResultPanel = report !== null && requestError === '';
  const showHttpError = requestError !== '';
  const hasNotices = showEmpty || showUnconfigured || showResultPanel || showHttpError;

  return (
    <div className="provision-control">
      <Button
        className="shopping-send"
        // Not Mantine `loading`: it fades the label, so "Sending…" would be hidden.
        leftSection={inFlight ? <Loader size="xs" color="currentColor" /> : undefined}
        aria-busy={inFlight || undefined}
        disabled={!enabled || inFlight}
        onClick={() => void provision()}
      >
        {inFlight ? 'Sending…' : 'Send to Kroger'}
      </Button>
      <Text className="shopping-send-hint" size="sm" c="dimmed">
        {`Adds these items to ${provisionCartTarget(storeLabel)}.`}
      </Text>
      {hasNotices && (
        <Stack className="provision-notices" gap="sm">
          {showEmpty && (
            <Text size="sm">Build the list before sending it to Kroger.</Text>
          )}
          {showUnconfigured && provider !== null && (
            <Text size="sm" c="dimmed">{provider.displayName} is unconfigured.</Text>
          )}
          {showHttpError && (
            <Stack gap="xs" role="status" aria-live="polite" aria-label="Cart send result">
              <Text component="h3" size="sm" fw={600} m={0}>
                Cart send result
              </Text>
              <Text size="sm" c="red">{requestError}</Text>
              <Button className="provision-result-action" variant="light" onClick={() => void provision()}>
                Try again
              </Button>
            </Stack>
          )}
          {showResultPanel && report !== null && (
            <ProvisionResultPanel
              report={report}
              provider={provider}
              cartUrl={cartUrl}
              onDismiss={() => setReport(null)}
              onTryAgain={() => void provision()}
              onReconnect={() => void reconnect()}
              onResolveUnknown={(entryId, reached) => void resolve(entryId, reached)}
            />
          )}
        </Stack>
      )}
    </div>
  );
};
