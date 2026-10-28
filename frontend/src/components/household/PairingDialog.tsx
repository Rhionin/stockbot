import { useCallback, useEffect, useState } from 'react';
import { Button, Group, Modal, Stack, Text, Title } from '@mantine/core';
import {
  PAIRING_EXPIRED_SENTENCE,
  PRINT_PAIRING_CODE_LABEL,
  pairingKeyboardFallback,
  pairingPrintBody,
  type PairingCode,
} from '@stockbot/shared/contract';
import { getPairing, startPairing } from '../../api/client';
import { Code128Mark, QrMark } from './symbols';

export function PairingDialog({
  opened,
  householdName,
  onClose,
  onPrintWallBarcodes,
}: {
  opened: boolean;
  householdName: string;
  onClose: () => void;
  onPrintWallBarcodes?: () => void;
}) {
  const [code, setCode] = useState<PairingCode | null>(null);
  const [expired, setExpired] = useState(false);

  const issue = useCallback(async () => {
    const next = await startPairing();
    setCode(next);
    setExpired(new Date(next.expiresAt).getTime() <= Date.now());
  }, []);

  useEffect(() => {
    if (!opened) return undefined;
    let cancelled = false;
    void Promise.resolve().then(() => {
      if (!cancelled) void issue().catch(() => undefined);
    });
    return () => {
      cancelled = true;
    };
  }, [opened, issue]);

  useEffect(() => {
    if (!opened || code === null || expired) return undefined;
    const timer = window.setInterval(() => {
      if (new Date(code.expiresAt).getTime() <= Date.now()) {
        setExpired(true);
        return;
      }
      void getPairing(code.pairingId).then((lookup) => {
        if (lookup.status === 'expired') setExpired(true);
        if (lookup.status === 'complete') onClose();
      }).catch(() => undefined);
    }, 2000);
    return () => window.clearInterval(timer);
  }, [opened, code, expired, onClose]);

  const spaced = code?.barcodeValue.split('').join(' ') ?? '';

  return (
    <Modal opened={opened} onClose={onClose} title="Pair a hub" size="lg">
      {expired && (
        <Stack>
          <Text role="alert">{PAIRING_EXPIRED_SENTENCE}</Text>
          <Button onClick={() => { void issue(); }}>Get a new one</Button>
        </Stack>
      )}
      {code && !expired && (
        <Stack className="pairing-sheet" gap="md">
          <Text className="pairing-spaced" aria-label="Pairing code">{spaced}</Text>
          <Stack gap="xs" className="pairing-wifi-setup">
            <Title order={5}>Setting up a brand-new scanner?</Title>
            <Text size="sm">
              1. Turn on the scanner. If its screen says &quot;Ready to set up&quot;, connect your phone to the Wi-Fi network &quot;Stockbot-Scanner-XXXX&quot; to enter your home Wi-Fi password.
            </Text>
            <Text size="sm">
              2. Once the scanner screen says &quot;Scan pairing barcode&quot;, scan the 1D barcode below.
            </Text>
          </Stack>
          <Code128Mark value={code.barcodeValue} quietZone />
          <QrMark value={code.qrValue} />
          <Text className="pairing-print-copy">{pairingPrintBody(householdName)}</Text>
          <Text className="pairing-keyboard">{pairingKeyboardFallback(code.barcodeValue)}</Text>
          <Group>
            <Button onClick={() => window.print()}>{PRINT_PAIRING_CODE_LABEL}</Button>
            {onPrintWallBarcodes && (
              <Button variant="default" onClick={onPrintWallBarcodes}>Print wall barcodes</Button>
            )}
            <Button variant="default" onClick={onClose}>Close</Button>
          </Group>
        </Stack>
      )}
    </Modal>
  );
}
