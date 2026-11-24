import { Card, Group, Loader, Stack, Text, Title } from '@mantine/core';
import type { ProcessingNotice } from '@stockbot/shared/types';
import { PROCESSING_DETAIL, PROCESSING_TITLE, processingBarcodeLine, processingCardLabel } from '@stockbot/shared/queueCards';

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
    aria-label={processingCardLabel(notice.barcode)}
  >
    <Group gap="xs" wrap="nowrap" align="center">
      <Loader size="xs" />
      <Stack gap={0}>
        <Title order={3} size="sm" mb={0}>{PROCESSING_TITLE}</Title>
        <Text size="xs" c="dimmed">{processingBarcodeLine(notice.barcode)}</Text>
        <Text size="xs">{PROCESSING_DETAIL}</Text>
      </Stack>
    </Group>
  </Card>
);
