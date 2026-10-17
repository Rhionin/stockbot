import { Button, Group, Modal, Stack, Text } from '@mantine/core';
import type { RestockPreview } from '../../restock/types';

export function StopDialog({
  preview,
  opened,
  busy,
  onClose,
  onConfirm,
}: {
  preview: RestockPreview | null;
  opened: boolean;
  busy?: boolean;
  onClose: () => void;
  onConfirm: () => void;
}) {
  return (
    <Modal opened={opened} onClose={onClose} title={preview?.title ?? ''} size="lg">
      {preview && (
        <Stack gap="sm">
          <Text size="sm" c="dimmed">{preview.body}</Text>
          {preview.stop.length > 0 && (
            <div>
              <Text fw={700} size="sm" c="dimmed">{`Will stop restocking · ${preview.stop.length}`}</Text>
              {preview.stop.map((line) => (
                <Group key={line.itemId} justify="space-between" py={6} wrap="nowrap">
                  <Text size="sm" fw={600}>{line.name}</Text>
                  <Text size="sm" c="dimmed">{line.before} → <Text span fw={700} c="dark">{line.after}</Text></Text>
                </Group>
              ))}
            </div>
          )}
          {preview.keep.length > 0 && (
            <div>
              <Text fw={700} size="sm" c="dimmed">{`Won't change · ${preview.keep.length}`}</Text>
              {preview.keep.map((line) => (
                <Group key={line.itemId} justify="space-between" py={6} wrap="nowrap" align="flex-start">
                  <div>
                    <Text size="sm" fw={600}>{line.name}</Text>
                    {line.reason && <Text size="xs" c="dimmed">{line.reason}</Text>}
                  </div>
                  <Text size="sm" fw={700}>{line.after}</Text>
                </Group>
              ))}
            </div>
          )}
          <Group justify="flex-end">
            <Button variant="default" onClick={onClose}>Cancel</Button>
            <Button loading={busy} onClick={onConfirm}>{preview.confirm}</Button>
          </Group>
        </Stack>
      )}
    </Modal>
  );
}
