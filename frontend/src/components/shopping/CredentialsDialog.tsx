import { useEffect, useRef, useState } from 'react';
import { Button, Divider, Group, Modal, NativeSelect, PasswordInput, Stack, Text, TextInput } from '@mantine/core';
import { clearProviderCredentials, saveProviderCredentials } from '../../api/client';
import type { ProviderInfo } from '@stockbot/shared/types';
import { inlineFailure } from '@stockbot/shared/contract';
import {
  PROVIDER_APP_CREDENTIALS_FORBIDDEN_MESSAGE,
  providerAppCredentialsAccessDenied,
  providerDisconnectDialogBody,
  providerDisconnectDialogTitle,
} from '@stockbot/shared/providerCredentials';

const touchTargetButton = { root: { minHeight: 44 } };

export interface CredentialsDialogProps {
  provider: ProviderInfo | null;
  opened: boolean;
  onClose: () => void;
  onChanged: () => void;
  onAccessDenied?: () => void;
  /** False hides the app-keys form (non-admins get a disconnect-only dialog). */
  showCredentialForm?: boolean;
  /** True after the server refused an app-keys change: show why instead of the form. */
  accessDenied?: boolean;
  onDisconnect?: () => void;
  disconnecting?: boolean;
  disconnectError?: string;
}

export const CredentialsDialog = ({
  provider,
  opened,
  onClose,
  onChanged,
  onAccessDenied,
  showCredentialForm = true,
  accessDenied = false,
  onDisconnect,
  disconnecting = false,
  disconnectError = '',
}: CredentialsDialogProps) => {
  const saved = provider?.credentials;
  const accessDeniedRef = useRef<HTMLParagraphElement>(null);
  const cancelRef = useRef<HTMLButtonElement>(null);
  const disconnectOnly = provider !== null
    && onDisconnect !== undefined
    && !showCredentialForm
    && !accessDenied;

  useEffect(() => {
    if (accessDenied && opened && provider !== null) {
      accessDeniedRef.current?.focus();
    }
  }, [accessDenied, opened, provider]);

  useEffect(() => {
    if (disconnectOnly && opened) {
      window.requestAnimationFrame(() => {
        cancelRef.current?.focus();
      });
    }
  }, [disconnectOnly, opened, provider?.id]);

  const modalTitle = provider === null
    ? 'Connection'
    : (disconnectOnly
      ? providerDisconnectDialogTitle(provider.displayName)
      : `${provider.displayName} connection`);

  return (
    <Modal
      opened={opened && provider !== null}
      onClose={onClose}
      title={modalTitle}
      size="sm"
      closeButtonProps={disconnectOnly ? { 'aria-label': 'Close' } : undefined}
    >
      {opened && provider !== null && (
        <Stack gap="sm">
          {accessDenied && (
            <Text
              ref={accessDeniedRef}
              tabIndex={-1}
              c="red"
              size="sm"
              role="alert"
            >
              {PROVIDER_APP_CREDENTIALS_FORBIDDEN_MESSAGE}
            </Text>
          )}
          {disconnectOnly && (
            <Text size="sm">{providerDisconnectDialogBody(provider.displayName)}</Text>
          )}
          {!accessDenied && showCredentialForm && (
            <CredentialForm
              key={`${provider.id}:${saved?.source ?? ''}:${saved?.clientId ?? ''}:${saved?.redirectUri ?? ''}:${saved?.modality ?? ''}:${saved?.secretSet ? '1' : '0'}`}
              provider={provider}
              onChanged={onChanged}
              onAccessDenied={onAccessDenied}
            />
          )}
          {onDisconnect !== undefined && !disconnectOnly && (
            <Stack gap="xs">
              {!accessDenied && showCredentialForm && <Divider />}
              <Button
                size="xs"
                type="button"
                variant="default"
                loading={disconnecting}
                onClick={onDisconnect}
              >
                {`Disconnect ${provider.displayName}`}
              </Button>
              {disconnectError !== '' && (
                <Text c="red" size="sm" role="alert">{disconnectError}</Text>
              )}
            </Stack>
          )}
          {disconnectOnly && (
            <Stack gap="xs">
              <Group justify="flex-end" gap="sm">
                <Button
                  ref={cancelRef}
                  type="button"
                  variant="default"
                  styles={touchTargetButton}
                  data-autofocus
                  onClick={onClose}
                >
                  Cancel
                </Button>
                <Button
                  type="button"
                  color="red"
                  styles={touchTargetButton}
                  loading={disconnecting}
                  onClick={onDisconnect}
                >
                  Disconnect
                </Button>
              </Group>
              {disconnectError !== '' && (
                <Text c="red" size="sm" role="alert">{disconnectError}</Text>
              )}
            </Stack>
          )}
        </Stack>
      )}
    </Modal>
  );
};

const credentialFailure = (requestError: unknown, fallback: string): string => {
  if (providerAppCredentialsAccessDenied(requestError)) {
    return PROVIDER_APP_CREDENTIALS_FORBIDDEN_MESSAGE;
  }
  return inlineFailure(requestError, fallback) ?? fallback;
};

const CredentialForm = ({
  provider,
  onChanged,
  onAccessDenied,
}: {
  provider: ProviderInfo;
  onChanged: () => void;
  onAccessDenied?: () => void;
}) => {
  const saved = provider.credentials;
  const [clientId, setClientId] = useState(saved?.clientId ?? '');
  const [clientSecret, setClientSecret] = useState('');
  const [redirectUri, setRedirectUri] = useState(saved?.redirectUri ?? '');
  const [modality, setModality] = useState(saved?.modality || 'PICKUP');
  const [pending, setPending] = useState(false);
  const [error, setError] = useState('');

  const save = async () => {
    setPending(true);
    setError('');
    try {
      await saveProviderCredentials(provider.id, {
        clientId,
        clientSecret,
        redirectUri,
        modality,
      });
      setClientSecret('');
      onChanged();
    } catch (requestError) {
      setError(credentialFailure(requestError, 'Unable to save credentials.'));
      if (providerAppCredentialsAccessDenied(requestError)) {
        onAccessDenied?.();
      }
    } finally {
      setPending(false);
    }
  };

  const clear = async () => {
    setPending(true);
    setError('');
    try {
      await clearProviderCredentials(provider.id);
      onChanged();
    } catch (requestError) {
      setError(credentialFailure(requestError, 'Unable to clear credentials.'));
      if (providerAppCredentialsAccessDenied(requestError)) {
        onAccessDenied?.();
      }
    } finally {
      setPending(false);
    }
  };

  return (
    <Stack
      gap="xs"
      component="form"
      aria-label={`${provider.displayName} credentials`}
      onSubmit={(event) => {
        event.preventDefault();
        void save();
      }}
    >
      <TextInput
        size="xs"
        label="Client ID"
        value={clientId}
        onChange={(event) => setClientId(event.currentTarget.value)}
      />
      <PasswordInput
        size="xs"
        label="Client secret"
        value={clientSecret}
        placeholder={saved?.secretSet ? 'Leave blank to keep the saved secret' : ''}
        onChange={(event) => setClientSecret(event.currentTarget.value)}
      />
      <TextInput
        size="xs"
        label="Redirect URI"
        value={redirectUri}
        onChange={(event) => setRedirectUri(event.currentTarget.value)}
      />
      <NativeSelect
        size="xs"
        label="Modality"
        value={modality}
        data={[
          { value: 'PICKUP', label: 'Pickup' },
          { value: 'DELIVERY', label: 'Delivery' },
        ]}
        onChange={(event) => setModality(event.currentTarget.value)}
      />
      {saved?.source === 'environment' && (
        <Text size="sm" c="dimmed">These credentials come from the server environment. Saving replaces them until you clear the saved credentials.</Text>
      )}
      {saved?.secretSet && (
        <Text size="sm" c="dimmed">A client secret is saved and is not shown.</Text>
      )}
      <Group gap="xs">
        <Button size="xs" type="submit" loading={pending}>Save credentials</Button>
        {saved?.source === 'saved' && (
          <Button size="xs" type="button" variant="default" loading={pending} onClick={() => void clear()}>
            Clear saved credentials
          </Button>
        )}
      </Group>
      {error !== '' && <Text c="red" size="sm" role="alert">{error}</Text>}
    </Stack>
  );
};
