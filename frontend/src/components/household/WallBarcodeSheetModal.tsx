import { Button, Group, Modal, Stack, Text, Title } from '@mantine/core';
import { Code128Mark } from './symbols';

export function WallBarcodeSheetModal({
  opened,
  onClose,
}: {
  opened: boolean;
  onClose: () => void;
}) {
  return (
    <Modal opened={opened} onClose={onClose} title="Storage-room barcode sheet" size="lg">
      <Stack className="storage-barcode-sheet" gap="lg">
        <article className="storage-barcode-card storage-barcode-card-out">
          <Text className="storage-barcode-badge" fw={700}>STOCK OUT · DEFAULT</Text>
          <Title order={4}>Taking items from the shelf</Title>
          <Code128Mark value="STOCK-OUT" quietZone />
          <Text size="sm">
            Scan this barcode (or press the mode button) before taking items out. Scanners automatically return to Stock out 5 minutes after the last scan.
          </Text>
        </article>
        <article className="storage-barcode-card storage-barcode-card-in">
          <Text className="storage-barcode-badge" fw={700}>STOCK IN · PUTTING AWAY</Text>
          <Title order={4}>Adding groceries to the shelf</Title>
          <Code128Mark value="STOCK-IN" quietZone />
          <Text size="sm">
            Scan this barcode before unloading groceries. Each scan resets the 5-minute timer; after 5 minutes of inactivity, the scanner switches back to Stock out.
          </Text>
        </article>
        <article className="storage-barcode-card storage-barcode-card-setup">
          <Text className="storage-barcode-badge" fw={600}>
            Setup B Only (Symcode Wireless Handheld) — Disable QR Codes
          </Text>
          <Code128Mark value="%%SpecCode99" quietZone />
          <Text size="sm">
            Scan this once after pairing a Symcode handheld scanner so it ignores marketing QR codes printed next to UPC barcodes on food packages. (M5StickS3 scanners disable QR codes automatically.)
          </Text>
        </article>
      </Stack>
      <Group justify="flex-end" mt="md">
        <Button variant="default" onClick={onClose}>Close</Button>
        <Button onClick={() => window.print()}>Print sheet</Button>
      </Group>
    </Modal>
  );
}
