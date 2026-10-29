import { useMemo, useState } from 'react';
import { Button, Group, Modal, Stack, Text } from '@mantine/core';
import {
  PRINT_ACTUAL_SIZE_INSTRUCTION,
  SYMCODE_DISCLOSURE_LABEL,
  defaultWallBarcodeSettings,
  resolveWallBarcodeSettings,
  type Device,
} from '@stockbot/shared/contract';
import { useKitPrintRoot } from './kitPrintUtils';
import { WallKitPrintSheet } from './wallKitPrintSheet';
import { PrintableBarcode } from './printableBarcode';

export function WallBarcodeSheetModal({
  opened,
  onClose,
  devices = [],
}: {
  opened: boolean;
  onClose: () => void;
  devices?: Device[];
}) {
  const [symcodeOpen, setSymcodeOpen] = useState(false);
  const { printing, print } = useKitPrintRoot();
  const settings = devices.length > 0
    ? resolveWallBarcodeSettings(devices)
    : defaultWallBarcodeSettings();
  const scannerName = useMemo(() => {
    const scanners = devices.filter((row) => row.kind === 'esp32_scanner' && row.revokedAt == null);
    if (scanners.length === 1) return scanners[0].label?.trim() || undefined;
    return undefined;
  }, [devices]);

  return (
    <Modal opened={opened} onClose={onClose} title="Wall barcodes" size="lg">
      <Stack gap="md">
        <div
          className="kit-print-root wall-kit-print-root"
          data-printing={printing ? 'true' : 'false'}
        >
          <WallKitPrintSheet settings={settings} scannerName={scannerName} />
        </div>
        <div className="pairing-screen-only wall-sheet-screen-extra">
          <button
            type="button"
            className="storage-barcode-symcode-disclosure device-touch-target"
            aria-expanded={symcodeOpen}
            onClick={() => setSymcodeOpen((open) => !open)}
          >
            {SYMCODE_DISCLOSURE_LABEL}
          </button>
          {symcodeOpen && (
            <Stack mt="sm" gap="sm" className="storage-barcode-symcode-panel">
              <Text className="storage-barcode-badge storage-barcode-badge-lg" fw={600}>
                Symcode handheld only: turn off QR codes
              </Text>
              <PrintableBarcode value="%%SpecCode99" moduleMm={0.6} heightMm={18} />
              <Text size="sm">
                Scan this once after connecting a Symcode handheld so it ignores QR codes on food packages. Stockbot&apos;s own scanner already does this.
              </Text>
            </Stack>
          )}
        </div>
        <Text size="sm" className="kit-print-hint">{PRINT_ACTUAL_SIZE_INSTRUCTION}</Text>
        <Group justify="flex-end">
          <Button variant="default" onClick={onClose}>Close</Button>
          <Button onClick={print}>Print</Button>
        </Group>
      </Stack>
    </Modal>
  );
}
