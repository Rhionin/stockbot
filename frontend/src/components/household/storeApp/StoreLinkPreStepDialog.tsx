import { useEffect } from 'react';
import { Button, Group, Modal, Stack, Text } from '@mantine/core';
import { krogerAppCopy } from '@stockbot/shared/krogerAppCopy';

export interface StoreLinkPreStepDialogProps {
  opened: boolean;
  onClose: () => void;
  onContinue: () => void;
}

/** Pre-step before linking a store when no store app exists (UX §9A.8). */
export function StoreLinkPreStepDialog({ opened, onClose, onContinue }: StoreLinkPreStepDialogProps) {
  useEffect(() => {
    if (!opened) return undefined;
    const frame = requestAnimationFrame(() => {
      document.querySelector('.store-app-pre-step-modal .mantine-Modal-header')?.setAttribute('role', 'group');
    });
    return () => cancelAnimationFrame(frame);
  }, [opened]);

  return (
    <Modal
      className="store-app-pre-step-modal"
      opened={opened}
      onClose={onClose}
      title={krogerAppCopy.rowTitle}
      size="sm"
    >
      <Stack gap="md">
        <Text size="sm">{krogerAppCopy.linkNeed}</Text>
        <Group justify="flex-end" gap="sm">
          <Button className="store-app-touch-target" variant="default" onClick={onClose}>
            {krogerAppCopy.linkNotNow}
          </Button>
          <Button className="store-app-touch-target" onClick={onContinue}>
            {krogerAppCopy.openSetup}
          </Button>
        </Group>
      </Stack>
    </Modal>
  );
}
