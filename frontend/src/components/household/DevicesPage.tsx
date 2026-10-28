import { useCallback, useEffect, useState } from 'react';
import { Button, Group, Stack, Text, Title } from '@mantine/core';
import {
  hostedErrorSentence,
  type Device,
} from '@stockbot/shared/contract';
import { ApiError, listDevices } from '../../api/client';
import { useHouseholdSession } from '../auth/householdSession';
import { DeviceScannerList } from './deviceScannerShared';
import { useLiveHint } from './liveHints';
import { PairingDialog } from './PairingDialog';
import { WallBarcodeSheetModal } from './WallBarcodeSheetModal';

export function DevicesPage() {
  const { auth } = useHouseholdSession();
  const active = auth?.households.find((row) => row.id === auth.activeHouseholdId) ?? null;
  const owner = active?.role === 'owner';
  const householdName = active?.name ?? 'this household';
  const [devices, setDevices] = useState<Device[]>([]);
  const [error, setError] = useState('');
  const [pairing, setPairing] = useState(false);
  const [wallSheet, setWallSheet] = useState(false);

  const load = useCallback(async () => {
    try {
      setDevices(await listDevices());
      setError('');
    } catch (requestError) {
      setError(requestError instanceof ApiError ? hostedErrorSentence(requestError.code) : 'Stockbot could not load devices.');
    }
  }, []);

  useEffect(() => {
    void Promise.resolve().then(load);
  }, [load]);

  useLiveHint(['devices'], load, 10_000);

  return (
    <Stack className="household-page" gap="lg">
      <Group justify="space-between" wrap="wrap">
        <Title order={2}>Devices</Title>
        <Group gap="sm" wrap="wrap">
          <Button className="device-touch-target" onClick={() => setPairing(true)}>Pair a hub</Button>
          <Button className="device-touch-target" variant="default" onClick={() => setWallSheet(true)}>
            Print wall barcodes
          </Button>
        </Group>
      </Group>
      {error !== '' && <Text role="alert">{error}</Text>}
      <DeviceScannerList
        devices={devices}
        isOwner={owner}
        onDevicesChange={setDevices}
        onReload={load}
      />
      <PairingDialog
        opened={pairing}
        householdName={householdName}
        onClose={() => {
          setPairing(false);
          void load();
        }}
        onPrintWallBarcodes={() => setWallSheet(true)}
      />
      <WallBarcodeSheetModal opened={wallSheet} onClose={() => setWallSheet(false)} />
    </Stack>
  );
}
