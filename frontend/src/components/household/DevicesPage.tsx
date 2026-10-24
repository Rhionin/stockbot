import { useCallback, useEffect, useState } from 'react';
import { Button, Group, Modal, Stack, Text, TextInput, Title } from '@mantine/core';
import { notifications } from '@mantine/notifications';
import {
  deviceKindLabel,
  disconnectHubConfirm,
  disconnectedHubNotice,
  hostedErrorSentence,
  type Device,
} from '@stockbot/shared/contract';
import { ApiError, listDevices, renameDevice, revokeDevice } from '../../api/client';
import { useHouseholdSession } from '../auth/householdSession';
import { useLiveHint } from './liveHints';
import { PairingDialog } from './PairingDialog';

function seen(device: Device): string {
  if (device.revokedAt) return 'Disconnected';
  if (!device.lastSeenAt) return device.online ? 'Online' : 'Offline';
  return `${device.online ? 'Online' : 'Offline'} · ${new Date(device.lastSeenAt).toLocaleString()}`;
}

export function DevicesPage() {
  const { auth } = useHouseholdSession();
  const active = auth?.households.find((row) => row.id === auth.activeHouseholdId) ?? null;
  const owner = active?.role === 'owner';
  const householdName = active?.name ?? 'this household';
  const [devices, setDevices] = useState<Device[]>([]);
  const [error, setError] = useState('');
  const [pairing, setPairing] = useState(false);
  const [renameId, setRenameId] = useState<string | null>(null);
  const [label, setLabel] = useState('');
  const [disconnecting, setDisconnecting] = useState<Device | null>(null);

  const load = useCallback(async () => {
    try {
      setDevices(await listDevices());
      setError('');
    } catch (requestError) {
      setError(requestError instanceof ApiError ? hostedErrorSentence(requestError.code) : 'Stockbot could not load devices.');
    }
  }, []);

  const refresh = useCallback(() => {
    void load();
  }, [load]);

  useEffect(() => {
    void Promise.resolve().then(load);
  }, [load]);

  useLiveHint(['devices'], refresh, 10_000);

  const hubName = (device: Device) => device.label?.trim() || 'Hub';

  return (
    <Stack className="household-page" gap="lg">
      <Group justify="space-between">
        <Title order={2}>Devices</Title>
        <Button onClick={() => setPairing(true)}>Pair a hub</Button>
      </Group>
      {error !== '' && <Text role="alert">{error}</Text>}
      <ul className="device-list">
        {devices.map((device) => (
          <li key={device.id} className={device.online ? 'device-row device-row-online' : 'device-row'}>
            <span className={device.online ? 'presence presence-online' : 'presence'} aria-hidden="true" />
            <div>
              <strong>{hubName(device)}</strong>
              <div>{deviceKindLabel(device.kind)} · {seen(device)}</div>
            </div>
            {device.kind !== 'direct' && (
              <Group gap="xs">
                <Button size="compact-sm" variant="default" onClick={() => { setRenameId(device.id); setLabel(device.label ?? ''); }}>
                  Rename
                </Button>
                {owner && !device.revokedAt && (
                  <Button size="compact-sm" color="red" variant="subtle" onClick={() => setDisconnecting(device)}>
                    Disconnect
                  </Button>
                )}
              </Group>
            )}
          </li>
        ))}
      </ul>
      <Modal opened={renameId !== null} onClose={() => setRenameId(null)} title="Rename device">
        <TextInput label="Name" value={label} onChange={(event) => setLabel(event.currentTarget.value)} />
        <Group justify="flex-end" mt="md">
          <Button variant="default" onClick={() => setRenameId(null)}>Cancel</Button>
          <Button
            onClick={() => {
              if (renameId === null) return;
              void renameDevice(renameId, label.trim()).then(() => {
                setRenameId(null);
                return load();
              });
            }}
          >
            Save
          </Button>
        </Group>
      </Modal>
      <Modal opened={disconnecting !== null} onClose={() => setDisconnecting(null)} title="Disconnect hub">
        {disconnecting && (
          <Text>{disconnectHubConfirm(hubName(disconnecting), disconnecting.queued ?? 0)}</Text>
        )}
        <Group justify="flex-end" mt="md">
          <Button variant="default" onClick={() => setDisconnecting(null)}>Cancel</Button>
          <Button
            className="household-danger"
            color="red"
            onClick={() => {
              if (disconnecting === null) return;
              const name = hubName(disconnecting);
              void revokeDevice(disconnecting.id).then(() => {
                setDisconnecting(null);
                notifications.show({ message: disconnectedHubNotice(name), color: 'teal' });
                return load();
              });
            }}
          >
            Disconnect
          </Button>
        </Group>
      </Modal>
      <PairingDialog
        opened={pairing}
        householdName={householdName}
        onClose={() => {
          setPairing(false);
          void load();
        }}
      />
    </Stack>
  );
}
