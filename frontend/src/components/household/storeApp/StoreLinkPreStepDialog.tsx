import { Button, Group, Modal, Stack, Text } from '@mantine/core';
import { krogerAppCopy } from '@stockbot/shared/krogerAppCopy';

export interface StoreLinkPreStepDialogProps {
  opened: boolean;
  onClose: () => void;
  onContinue: () => void;
}

/** Pre-step before linking a store when no store app exists (UX §9A.8). */
export function StoreLinkPreStepDialog({ opened, onClose, onContinue }: StoreLinkPreStepDialogProps) {
  return (
    <Modal opened={opened} onClose={onClose} title={krogerAppCopy.rowTitle} size="sm">
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
