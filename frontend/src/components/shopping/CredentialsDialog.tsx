import { useState } from 'react';
import { Button, Divider, Group, Modal, NativeSelect, PasswordInput, Stack, Text, TextInput } from '@mantine/core';
import { clearProviderCredentials, saveProviderCredentials } from '../../api/client';
import type { ProviderInfo } from '@stockbot/shared/types';
import { inlineFailure } from '@stockbot/shared/contract';

export interface CredentialsDialogProps {
  provider: ProviderInfo | null;
  opened: boolean;
  onClose: () => void;
  onChanged: () => void;
  onDisconnect?: () => void;
  disconnecting?: boolean;
  disconnectError?: string;
}

export const CredentialsDialog = ({
  provider,
  opened,
  onClose,
  onChanged,
  onDisconnect,
  disconnecting = false,
  disconnectError = '',
}: CredentialsDialogProps) => {
  const saved = provider?.credentials;
  return (
    <Modal
      opened={opened && provider !== null}
      onClose={onClose}
      title={provider === null ? 'Connection' : `${provider.displayName} connection`}
      size="sm"
    >
      {opened && provider !== null && (
        <Stack gap="sm">
          <CredentialForm
            key={`${provider.id}:${saved?.source ?? ''}:${saved?.clientId ?? ''}:${saved?.redirectUri ?? ''}:${saved?.modality ?? ''}:${saved?.secretSet ? '1' : '0'}`}
            provider={provider}
            onChanged={onChanged}
          />
          {onDisconnect !== undefined && (
            <Stack gap="xs">
              <Divider />
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
        </Stack>
      )}
    </Modal>
  );
};

const CredentialForm = ({ provider, onChanged }: { provider: ProviderInfo; onChanged: () => void }) => {
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
      setError(inlineFailure(requestError, 'Unable to save credentials.') ?? '');
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
      setError(inlineFailure(requestError, 'Unable to clear credentials.') ?? '');
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
      {error !== '' && <Text c="red" size="sm">{error}</Text>}
    </Stack>
  );
};
