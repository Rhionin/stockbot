import { useEffect, useRef, useState } from 'react';
import { Button, Group, Modal, Stack, Text } from '@mantine/core';
import { krogerAppCopy, storeNameContext } from '@stockbot/shared/krogerAppCopy';
import { removeFailureMessage } from './krogerAppMessages';

export interface RemoveStoreAppDialogProps {
  opened: boolean;
  onClose: () => void;
  onConfirm: () => Promise<void>;
  linkedStoreName?: string | null;
}

export function RemoveStoreAppDialog({
  opened,
  onClose,
  onConfirm,
  linkedStoreName,
}: RemoveStoreAppDialogProps) {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const cancelRef = useRef<HTMLButtonElement>(null);
  const ctx = storeNameContext(linkedStoreName);
  const body = ctx.store === 'your store'
    ? krogerAppCopy.rmBodyFallback
    : krogerAppCopy.rmBody
      .replaceAll('{store}', ctx.store);

  useEffect(() => {
    if (opened) {
      window.requestAnimationFrame(() => cancelRef.current?.focus());
    }
  }, [opened]);

  useEffect(() => {
    if (!opened) return;
    queueMicrotask(() => {
      document.querySelectorAll('.store-app-remove-modal .mantine-Modal-header').forEach((node) => {
        node.setAttribute('role', 'group');
      });
    });
  }, [opened]);

  const confirm = async () => {
    setBusy(true);
    setError('');
    try {
      await onConfirm();
      onClose();
    } catch {
      setError(removeFailureMessage());
    } finally {
      setBusy(false);
    }
  };

  return (
    <Modal
      className="store-app-remove-modal"
      opened={opened}
      onClose={onClose}
      title={krogerAppCopy.rmTitle}
      size="sm"
      closeButtonProps={{
        'aria-label': krogerAppCopy.closeLabel,
        className: 'store-app-modal-close',
      }}
    >
      <Stack gap="md">
        <Text size="sm" className="store-app-rm-body">{body}</Text>
        {error !== '' && <Text role="alert" c="red" size="sm">{error}</Text>}
        <Group justify="flex-end" gap="sm">
          <Button
            ref={cancelRef}
            className="store-app-touch-target store-app-touch-target-wide"
            variant="default"
            onClick={onClose}
            disabled={busy}
            data-autofocus
          >
            {krogerAppCopy.rmCancel}
          </Button>
          <Button
            className="store-app-touch-target store-app-touch-target-wide"
            color="red"
            loading={busy}
            aria-busy={busy}
            onClick={() => { void confirm(); }}
          >
            {busy ? krogerAppCopy.rmBusy : krogerAppCopy.rmConfirm}
          </Button>
        </Group>
      </Stack>
    </Modal>
  );
}
