import { useCallback, useEffect, useState } from 'react';
import { Alert, Button, Group, Stack, Text, Title } from '@mantine/core';
import { hostedErrorSentence, type Device } from '@stockbot/shared/contract';
import { resolveWallBarcodeSettings } from './kitCopy456';
import { ApiError, listDevices } from '../../api/client';
import { useLiveHint } from './liveHints';
import { DeviceScannerList } from './deviceScannerShared';
import { PairingDialog } from './PairingDialog';
import { WallBarcodeSheetModal } from './WallBarcodeSheetModal';

export function ScannersSection({
  householdName,
  isOwner,
}: {
  householdName: string;
  isOwner: boolean;
}) {
  const [devices, setDevices] = useState<Device[]>([]);
  const [scannersError, setScannersError] = useState('');
  const [pairing, setPairing] = useState(false);
  const [wallSheet, setWallSheet] = useState(false);

  const loadDevices = useCallback(async () => {
    try {
      setDevices(await listDevices());
      setScannersError('');
    } catch (requestError) {
      if (requestError instanceof ApiError) {
        setScannersError(hostedErrorSentence(requestError.code));
      } else {
        setDevices([]);
      }
    }
  }, []);

  useEffect(() => {
    void Promise.resolve().then(loadDevices);
  }, [loadDevices]);

  useLiveHint(['devices'], loadDevices, 10_000);

  const activeScanners = devices.filter((row) => row.kind === 'esp32_scanner' && row.revokedAt == null);

  return (
    <section aria-labelledby="scanners-heading">
      <Title id="scanners-heading" order={3}>Scanners</Title>
      <Text mt="xs">
        Connect storage-room barcode scanners and choose whether scans wait in the Scan Queue or update stock right away.
      </Text>
      {activeScanners.length > 0 && (
        <Group gap="sm" wrap="wrap" mt="sm">
          <Button className="device-touch-target" onClick={() => setPairing(true)}>Connect a scanner</Button>
          <Button className="device-touch-target" variant="default" onClick={() => setWallSheet(true)}>
            Print wall barcodes
          </Button>
        </Group>
      )}
      {scannersError !== '' && <Alert color="red" variant="light" role="alert" mt="sm">{scannersError}</Alert>}
      {activeScanners.length === 0 ? (
        <Stack gap="sm" mt="md">
          <Text>
            No scanners yet. Connect one to scan items in and out right at the shelf.
          </Text>
          <Button className="device-touch-target" onClick={() => setPairing(true)}>Connect a scanner</Button>
        </Stack>
      ) : (
        <DeviceScannerList
          devices={devices}
          isOwner={isOwner}
          onDevicesChange={setDevices}
          onReload={loadDevices}
        />
      )}
      <PairingDialog
        opened={pairing}
        householdName={householdName}
        pairingContext="scanner"
        wallBarcodeSettings={resolveWallBarcodeSettings(devices)}
        onClose={() => {
          setPairing(false);
          void loadDevices();
        }}
      />
      <WallBarcodeSheetModal opened={wallSheet} onClose={() => setWallSheet(false)} devices={devices} />
    </section>
  );
}
