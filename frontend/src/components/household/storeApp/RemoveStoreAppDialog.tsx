import { useState } from 'react';
import { Button, Group, Modal, Stack, Text } from '@mantine/core';
import { krogerAppCopy, storeNameContext } from '@stockbot/shared/krogerAppCopy';
import { inlineFailure } from '@stockbot/shared/contract';

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
  const ctx = storeNameContext(linkedStoreName);
  const body = ctx.store === 'your store'
    ? krogerAppCopy.rmBodyFallback
    : krogerAppCopy.rmBody
      .replaceAll('{store}', ctx.store);

  const confirm = async () => {
    setBusy(true);
    setError('');
    try {
      await onConfirm();
      onClose();
    } catch (requestError) {
      setError(inlineFailure(requestError, krogerAppCopy.rmFailed) ?? krogerAppCopy.rmFailed);
    } finally {
      setBusy(false);
    }
  };

  return (
    <Modal opened={opened} onClose={onClose} title={krogerAppCopy.rmTitle} size="sm">
      <Stack gap="md">
        <Text size="sm">{body}</Text>
        {error !== '' && <Text role="alert" c="red" size="sm">{error}</Text>}
        <Group justify="flex-end" gap="sm">
          <Button className="store-app-touch-target" variant="default" onClick={onClose} disabled={busy}>
            {krogerAppCopy.rmCancel}
          </Button>
          <Button
            className="store-app-touch-target"
            color="red"
            loading={busy}
            onClick={() => { void confirm(); }}
          >
            {busy ? krogerAppCopy.rmBusy : krogerAppCopy.rmConfirm}
          </Button>
        </Group>
      </Stack>
    </Modal>
  );
}
