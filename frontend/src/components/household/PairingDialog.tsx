import { useCallback, useEffect, useState } from 'react';
import { Button, Group, Modal, Stack, Text, Title } from '@mantine/core';
import {
  PAIRING_EXPIRED_SENTENCE,
  PRINT_PAIRING_CODE_LABEL,
  defaultWallBarcodeSettings,
  hostedErrorSentence,
  pairingDialogTitle,
  pairingKeyboardFallback,
  type PairingCode,
  type WallBarcodeSettings,
} from '@stockbot/shared/contract';
import { ApiError, getPairing, startPairing } from '../../api/client';
import { useKitPrintRoot } from './kitPrintUtils';
import { SetupKitPrintSheet } from './setupKitPrintSheet';
import { Code128Mark, QrMark } from './symbols';

const PAIRING_STEP_TWO =
  'When the scanner screen says "Scan the pairing barcode from Stockbot", scan the barcode below.';

export function PairingDialog({
  opened,
  householdName,
  onClose,
  pairingContext = 'scanner',
  wallBarcodeSettings,
}: {
  opened: boolean;
  householdName: string;
  onClose: () => void;
  pairingContext?: 'scanner' | 'devices';
  wallBarcodeSettings?: WallBarcodeSettings;
}) {
  const [code, setCode] = useState<PairingCode | null>(null);
  const [expired, setExpired] = useState(false);
  const [issueError, setIssueError] = useState('');
  const [issuing, setIssuing] = useState(false);
  const wallSettings = wallBarcodeSettings ?? defaultWallBarcodeSettings();
  const keyboardContext = pairingContext === 'scanner' ? 'scanner' : 'hub';
  const keyboardLine = code ? pairingKeyboardFallback(code.barcodeValue, keyboardContext) : null;
  const { printing, print } = useKitPrintRoot();

  const issue = useCallback(async () => {
    setIssuing(true);
    setIssueError('');
    try {
      const next = await startPairing();
      setCode(next);
      setExpired(new Date(next.expiresAt).getTime() <= Date.now());
    } catch (requestError) {
      if (requestError instanceof ApiError && requestError.code !== '') {
        setIssueError(hostedErrorSentence(requestError.code));
      } else {
        setIssueError("Can't reach Stockbot. Check your connection and try again.");
      }
      setCode(null);
    } finally {
      setIssuing(false);
    }
  }, []);

  useEffect(() => {
    if (!opened) return undefined;
    let cancelled = false;
    void Promise.resolve().then(() => {
      if (!cancelled) void issue();
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

  const barcodeValue = code?.barcodeValue ?? code?.code ?? '';
  const qrValue = code?.qrValue ?? code?.qrPayload ?? '';
  const spaced = barcodeValue.split('').join(' ');

  return (
    <Modal opened={opened} onClose={onClose} title={pairingDialogTitle(pairingContext)} size="lg">
      {issuing && !code && (
        <Text role="status">Getting a pairing barcode…</Text>
      )}
      {issueError !== '' && (
        <Stack gap="sm">
          <Text role="alert">{issueError}</Text>
          <Button onClick={() => { void issue(); }}>Try again</Button>
        </Stack>
      )}
      {expired && (
        <Stack>
          <Text role="alert">{PAIRING_EXPIRED_SENTENCE}</Text>
          <Button onClick={() => { void issue(); }}>Get a new one</Button>
        </Stack>
      )}
      {code && !expired && (
        <Stack className="pairing-dialog-body" gap="md">
          <div className="pairing-screen-only">
            <Text className="pairing-spaced" aria-label="Pairing code">{spaced}</Text>
            <Stack gap="xs" className="pairing-wifi-setup">
              <Title order={5}>Setting up a brand-new scanner?</Title>
              <Text size="sm">
                1. Plug in the scanner. If its screen says READY TO SET UP, join the Wi-Fi network it shows (Stockbot-Scanner-…) on your phone, then enter your home Wi-Fi name and password.
              </Text>
              <Text size="sm">2. {PAIRING_STEP_TWO}</Text>
            </Stack>
            <Code128Mark value={barcodeValue} quietZone />
            <QrMark value={qrValue} />
            {keyboardLine && <Text className="pairing-keyboard">{keyboardLine}</Text>}
          </div>
          <div
            className="kit-print-root setup-kit-print-root"
            data-printing={printing ? 'true' : 'false'}
          >
            <SetupKitPrintSheet
              householdName={householdName}
              barcodeValue={barcodeValue}
              expiresAt={code.expiresAt}
              wallSettings={wallSettings}
            />
          </div>
          <Group className="pairing-screen-only">
            <Button onClick={print} disabled={issuing}>{PRINT_PAIRING_CODE_LABEL}</Button>
            <Button variant="default" onClick={onClose}>Close</Button>
          </Group>
        </Stack>
      )}
    </Modal>
  );
}
