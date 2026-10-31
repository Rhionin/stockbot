import { useState } from 'react';
import { Button, Loader, Stack, Text } from '@mantine/core';
import { authorizeProvider, exportShoppingList, resolveUnknownProvision, ApiError } from '../../api/client';
import type { ProviderInfo, ProvisionReport, ShoppingListEntry } from '@stockbot/shared/types';
import {
  CODE_PROVIDER_UNAVAILABLE,
  CODE_RECONNECT_REQUIRED,
  inlineFailure,
} from '@stockbot/shared/contract';
import {
  provisionEmptyListNote,
  provisionSendButtonLabel,
  provisionSendHint,
  provisionStoreLabel,
  type ProvisionWholeRequestError,
} from '@stockbot/shared/provisionExportUX';
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

function exportEnvelopeError(error: unknown): ProvisionWholeRequestError | undefined {
  if (!(error instanceof ApiError)) return undefined;
  if (error.code === CODE_RECONNECT_REQUIRED) return 'reconnect_required';
  if (error.code === CODE_PROVIDER_UNAVAILABLE) return 'provider_unavailable';
  return undefined;
}

export const ProvisionButton = ({ provider, entries, explainEmpty = false, onFinished }: ProvisionButtonProps) => {
  const [inFlight, setInFlight] = useState(false);
  const [report, setReport] = useState<ProvisionReport | null>(null);
  const [envelopeError, setEnvelopeError] = useState<ProvisionWholeRequestError | undefined>();
  const [requestError, setRequestError] = useState('');
  const [cartUrl, setCartUrl] = useState<string | undefined>();

  const staged = entries.some((entry) => provisionQuantity(entry) >= 1);
  const enabled = canProvision(provider, entries, inFlight);
  const storeLabel = provisionStoreLabel(provider?.displayName);
  const sendLabel = provisionSendButtonLabel(storeLabel);

  const provision = async () => {
    setInFlight(true);
    setRequestError('');
    setReport(null);
    setEnvelopeError(undefined);
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
      const envelope = exportEnvelopeError(requestError);
      if (envelope !== undefined) {
        setEnvelopeError(envelope);
      } else {
        setRequestError(inlineFailure(requestError, 'These items could not be sent to your store.') ?? '');
      }
    } finally {
      setInFlight(false);
    }
  };

  const resolve = async (entryId: string, reachedProvider: boolean) => {
    if (provider === null || entryId === '') return;
    setRequestError('');
    await resolveUnknownProvision(entryId, provider.id, reachedProvider);
    onFinished();
    // Errors propagate to the panel, which shows them in the row.
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
  const showResultPanel = (report !== null || envelopeError !== undefined) && requestError === '';
  const showHttpError = requestError !== '';
  const hasNotices = showEmpty || showUnconfigured || showResultPanel || showHttpError;

  return (
    <div className="provision-control">
      <Button
        className="shopping-send"
        leftSection={inFlight ? <Loader size="xs" color="currentColor" /> : undefined}
        aria-busy={inFlight || undefined}
        aria-label={inFlight ? 'Sending…' : sendLabel}
        disabled={!enabled || inFlight}
        onClick={() => void provision()}
      >
        {inFlight ? 'Sending…' : sendLabel}
      </Button>
      <Text className="shopping-send-hint" size="sm" c="dimmed">
        {provisionSendHint(storeLabel)}
      </Text>
      {hasNotices && (
        <Stack className="provision-notices" gap="sm">
          {showEmpty && (
            <Text size="sm">{provisionEmptyListNote(storeLabel)}</Text>
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
          {showResultPanel && (
            <ProvisionResultPanel
              report={report}
              envelopeError={envelopeError}
              provider={provider}
              cartUrl={cartUrl}
              onDismiss={() => {
                setReport(null);
                setEnvelopeError(undefined);
              }}
              onTryAgain={() => void provision()}
              onReconnect={() => void reconnect()}
              onResolveUnknown={(entryId, reached) => resolve(entryId, reached)}
            />
          )}
        </Stack>
      )}
    </div>
  );
};
