import { useCallback, useState } from 'react';
import { Button, Group, Modal, Radio, Stack, Text, TextInput } from '@mantine/core';
import { notifications } from '@mantine/notifications';
import {
  deviceKindLabel,
  deviceUntitledLabel,
  disconnectDeviceModalTitle,
  disconnectHubConfirm,
  disconnectedHubNotice,
  hostedErrorSentence,
  renameDeviceModalTitle,
  type Device,
  type UpdateDeviceRequest,
} from '@stockbot/shared/contract';
import { ApiError, renameDevice, revokeDevice, updateDevice } from '../../api/client';

function hosted(error: unknown, fallback: string): string {
  if (error instanceof ApiError && error.code !== '') return hostedErrorSentence(error.code);
  return error instanceof Error ? error.message : fallback;
}

function displayName(device: Device): string {
  return device.label?.trim() || deviceUntitledLabel(device.kind);
}

function seen(device: Device): string {
  if (device.revokedAt) return 'Disconnected';
  if (!device.lastSeenAt) return device.online ? 'Online' : 'Offline';
  return `${device.online ? 'Online' : 'Offline'} · ${new Date(device.lastSeenAt).toLocaleString()}`;
}

function deviceStatusLine(device: Device): string {
  const parts = [deviceKindLabel(device.kind), seen(device)];
  if (device.queued != null && device.queued > 0) {
    parts.push(`${device.queued} waiting to send`);
  }
  return parts.join(' · ');
}

function batteryPill(device: Device): { label: string; color: string } | null {
  if (device.batteryPct == null) return null;
  const pct = device.batteryPct;
  if (device.charging) return { label: `${pct}% (Charging)`, color: '#134e4a' };
  if (pct <= 10) return { label: `${pct}% (Charge now)`, color: '#B91C1C' };
  if (pct <= 25) return { label: `${pct}% (Low battery)`, color: '#B45309' };
  return { label: `${pct}%`, color: '#134e4a' };
}

function showScannerSettings(device: Device): boolean {
  return device.kind !== 'direct' && device.revokedAt == null;
}

type DeviceScannerListProps = {
  devices: Device[];
  isOwner: boolean;
  onDevicesChange: (devices: Device[]) => void;
  onReload: () => void;
};

export function DeviceScannerList({
  devices,
  isOwner,
  onDevicesChange,
  onReload,
}: DeviceScannerListProps) {
  const [renameId, setRenameId] = useState<string | null>(null);
  const [renameKind, setRenameKind] = useState<string>('pi_hub');
  const [label, setLabel] = useState('');
  const [disconnecting, setDisconnecting] = useState<Device | null>(null);
  const [savingDeviceId, setSavingDeviceId] = useState<string | null>(null);
  const [savedDeviceId, setSavedDeviceId] = useState<string | null>(null);
  const [settingsErrorById, setSettingsErrorById] = useState<Record<string, string>>({});

  const patchDevice = useCallback(async (device: Device, patch: UpdateDeviceRequest) => {
    setSavingDeviceId(device.id);
    setSavedDeviceId(null);
    setSettingsErrorById((prev) => {
      const next = { ...prev };
      delete next[device.id];
      return next;
    });
    try {
      const updated = await updateDevice(device.id, patch);
      onDevicesChange(devices.map((row) => (row.id === updated.id ? updated : row)));
      setSavedDeviceId(device.id);
    } catch (requestError) {
      setSettingsErrorById((prev) => ({
        ...prev,
        [device.id]: hosted(requestError, 'Could not save scanner settings.'),
      }));
    } finally {
      setSavingDeviceId(null);
    }
  }, [devices, onDevicesChange]);

  return (
    <>
      <ul className="device-list">
        {devices.map((device) => {
          const pill = batteryPill(device);
          const settingsDisabled = savingDeviceId === device.id;
          const commitMode = device.commitMode ?? 'queue';
          const defaultDirection = device.defaultDirection ?? 'stock_out';
          const inactivityRevertSeconds = device.inactivityRevertSeconds ?? 300;
          return (
            <li
              key={device.id}
              className={device.online ? 'device-row device-row-online device-scanner-card' : 'device-row device-scanner-card'}
            >
              <span className={device.online ? 'presence presence-online' : 'presence'} aria-hidden="true" />
              <div className="device-scanner-main">
                <strong className="device-scanner-name">{displayName(device)}</strong>
                <div className="device-scanner-meta">
                  <span>{deviceStatusLine(device)}</span>
                  {pill && (
                    <span className="device-battery-pill" style={{ color: pill.color }}>
                      {pill.label}
                    </span>
                  )}
                </div>
                {showScannerSettings(device) && (
                  <Stack className="device-scanner-settings" gap="sm" mt="sm">
                    <Radio.Group
                      label="When an item is scanned"
                      value={commitMode}
                      onChange={(value) => {
                        if (value === commitMode) return;
                        void patchDevice(device, { commitMode: value as 'queue' | 'immediate' });
                      }}
                    >
                      <Stack gap="xs" mt="xs">
                        <Radio
                          value="queue"
                          label="Require review in Scan Queue"
                          description="Scans wait in the Scan Queue until you confirm them."
                          disabled={settingsDisabled}
                        />
                        <Radio
                          value="immediate"
                          label="Adjust stock immediately for known items"
                          description="Known items update stock right away. Unknown barcodes still go to the Scan Queue to be named."
                          disabled={settingsDisabled}
                        />
                      </Stack>
                    </Radio.Group>
                    <Radio.Group
                      label="Default mode"
                      value={defaultDirection}
                      onChange={(value) => {
                        if (value === defaultDirection) return;
                        void patchDevice(device, { defaultDirection: value as 'stock_in' | 'stock_out' });
                      }}
                    >
                      <Stack gap="xs" mt="xs">
                        <Radio
                          value="stock_out"
                          label="Stock out (Scans remove from stock)"
                          disabled={settingsDisabled}
                        />
                        <Radio
                          value="stock_in"
                          label="Stock in (Scans add to stock)"
                          disabled={settingsDisabled}
                        />
                      </Stack>
                    </Radio.Group>
                    <Radio.Group
                      label="Return to default mode after"
                      description="After switching modes on the scanner, it automatically returns to the default mode once idle."
                      value={String(inactivityRevertSeconds)}
                      onChange={(value) => {
                        const seconds = Number(value);
                        if (seconds === inactivityRevertSeconds) return;
                        void patchDevice(device, { inactivityRevertSeconds: seconds });
                      }}
                    >
                      <Stack gap="xs" mt="xs">
                        <Radio value="60" label="1 minute" disabled={settingsDisabled} />
                        <Radio value="180" label="3 minutes" disabled={settingsDisabled} />
                        <Radio value="300" label="5 minutes" disabled={settingsDisabled} />
                        <Radio value="600" label="10 minutes" disabled={settingsDisabled} />
                      </Stack>
                    </Radio.Group>
                    {settingsDisabled && <Text size="sm">Saving…</Text>}
                    {savedDeviceId === device.id && !settingsDisabled && (
                      <Text role="status" size="sm" c="teal">Scanner settings saved.</Text>
                    )}
                    {settingsErrorById[device.id] && (
                      <Text role="alert" size="sm" c="red">{settingsErrorById[device.id]}</Text>
                    )}
                  </Stack>
                )}
                {device.kind !== 'direct' && (
                  <Group gap="xs" wrap="wrap" mt="sm">
                    <Button
                      size="compact-sm"
                      variant="default"
                      className="device-touch-target"
                      onClick={() => {
                        setRenameId(device.id);
                        setRenameKind(device.kind);
                        setLabel(device.label ?? '');
                      }}
                    >
                      Rename
                    </Button>
                    {isOwner && !device.revokedAt && (
                      <Button
                        size="compact-sm"
                        color="red"
                        variant="subtle"
                        className="device-touch-target"
                        onClick={() => setDisconnecting(device)}
                      >
                        Disconnect
                      </Button>
                    )}
                  </Group>
                )}
              </div>
            </li>
          );
        })}
      </ul>
      <Modal opened={renameId !== null} onClose={() => setRenameId(null)} title={renameDeviceModalTitle(renameKind)}>
        <TextInput label="Name" value={label} onChange={(event) => setLabel(event.currentTarget.value)} />
        <Group justify="flex-end" mt="md">
          <Button variant="default" onClick={() => setRenameId(null)}>Cancel</Button>
          <Button
            onClick={() => {
              if (renameId === null) return;
              void renameDevice(renameId, label.trim()).then(() => {
                setRenameId(null);
                onReload();
              });
            }}
          >
            Save
          </Button>
        </Group>
      </Modal>
      <Modal
        opened={disconnecting !== null}
        onClose={() => setDisconnecting(null)}
        title={disconnecting ? disconnectDeviceModalTitle(disconnecting.kind) : 'Disconnect hub'}
      >
        {disconnecting && (
          <Text>{disconnectHubConfirm(displayName(disconnecting), disconnecting.queued ?? 0)}</Text>
        )}
        <Group justify="flex-end" mt="md">
          <Button variant="default" onClick={() => setDisconnecting(null)}>Cancel</Button>
          <Button
            className="household-danger"
            color="red"
            onClick={() => {
              if (disconnecting === null) return;
              const name = displayName(disconnecting);
              void revokeDevice(disconnecting.id).then(() => {
                setDisconnecting(null);
                notifications.show({ message: disconnectedHubNotice(name), color: 'teal' });
                onReload();
              });
            }}
          >
            Disconnect
          </Button>
        </Group>
      </Modal>
    </>
  );
}
