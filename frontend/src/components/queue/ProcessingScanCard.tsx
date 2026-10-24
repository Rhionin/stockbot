import { Card, Group, Loader, Stack, Text, Title } from '@mantine/core';
import type { ProcessingNotice } from '@stockbot/shared/types';

export interface ProcessingScanCardProps {
  notice: ProcessingNotice;
  justCaptured?: boolean;
}

export const ProcessingScanCard = ({ notice, justCaptured = false }: ProcessingScanCardProps) => (
  <Card
    component="article"
    withBorder
    padding="xs"
    radius="md"
    aria-busy="true"
    className={justCaptured ? 'processing-scan-card processing-scan-card--just-captured' : 'processing-scan-card'}
    aria-label={`Scan ${notice.barcode} processing`}
  >
    <Group gap="xs" wrap="nowrap" align="center">
      <Loader size="xs" />
      <Stack gap={0}>
        <Title order={3} size="sm" mb={0}>Looking up product</Title>
        <Text size="xs" c="dimmed">Barcode: {notice.barcode}</Text>
        <Text size="xs">Checking the open food database…</Text>
      </Stack>
    </Group>
  </Card>
);
