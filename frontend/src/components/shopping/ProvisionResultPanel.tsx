import { useMemo, useState } from 'react';
import { Anchor, Button, CloseButton, Group, Stack, Text } from '@mantine/core';
import type { ProviderInfo, ProvisionReport } from '@stockbot/shared/types';
import {
  PROVISION_GROUP_NOT_ADDED,
  PROVISION_GROUP_NOT_CONFIRMED,
  PROVISION_GROUP_SENT,
  PROVISION_RESULT_PANEL_LABEL,
  PROVISION_SHOW_ALL_THRESHOLD,
  groupProvisionResults,
  provisionItemReasonLine,
  provisionSentTotal,
  provisionStoreLabel,
  provisionSummaryCopy,
  provisionWholeRequestActionLabel,
  provisionWholeRequestMessage,
} from '@stockbot/shared/provisionExportUX';

export interface ProvisionResultPanelProps {
  report: ProvisionReport;
  provider: ProviderInfo | null;
  cartUrl?: string;
  onDismiss: () => void;
  onTryAgain: () => void;
  onReconnect: () => void;
  onResolveUnknown: (entryId: string, reachedProvider: boolean) => void;
}

export const ProvisionResultPanel = ({
  report,
  provider,
  cartUrl,
  onDismiss,
  onTryAgain,
  onReconnect,
  onResolveUnknown,
}: ProvisionResultPanelProps) => {
  const [showAllNotAdded, setShowAllNotAdded] = useState(false);
  const storeLabel = provisionStoreLabel(provider?.displayName);
  const groups = useMemo(() => groupProvisionResults(report), [report]);
  const summary = useMemo(
    () => provisionSummaryCopy(groups.sentCount, groups.couldNotSendCount, storeLabel),
    [groups.couldNotSendCount, groups.sentCount, storeLabel],
  );
  const sentTotal = provisionSentTotal(groups);

  const notAddedVisible = showAllNotAdded || groups.notAdded.length <= PROVISION_SHOW_ALL_THRESHOLD
    ? groups.notAdded
    : groups.notAdded.slice(0, PROVISION_SHOW_ALL_THRESHOLD);
  const notAddedHidden = groups.notAdded.length - notAddedVisible.length;

  if (groups.wholeRequestError !== undefined) {
    const code = groups.wholeRequestError;
    const actionLabel = provisionWholeRequestActionLabel(code, storeLabel);
    return (
      <Stack
        className="provision-result-panel"
        gap="xs"
        role="status"
        aria-live="polite"
        aria-label={PROVISION_RESULT_PANEL_LABEL}
      >
        <Group justify="space-between" align="flex-start" wrap="nowrap">
          <Text component="h3" size="sm" fw={600} m={0}>
            {PROVISION_RESULT_PANEL_LABEL}
          </Text>
          <CloseButton
            className="provision-result-dismiss"
            aria-label="Dismiss cart send result"
            onClick={onDismiss}
          />
        </Group>
        <Text size="sm">{provisionWholeRequestMessage(code, storeLabel)}</Text>
        <Button
          className="provision-result-action"
          variant="light"
          onClick={() => (code === 'reconnect_required' ? onReconnect() : onTryAgain())}
        >
          {actionLabel}
        </Button>
      </Stack>
    );
  }

  return (
    <Stack
      className="provision-result-panel"
      gap="xs"
      role="status"
      aria-live="polite"
      aria-label={PROVISION_RESULT_PANEL_LABEL}
    >
      <Group justify="space-between" align="flex-start" wrap="nowrap">
        <Text component="h3" size="sm" fw={600} m={0}>
          {PROVISION_RESULT_PANEL_LABEL}
        </Text>
        <CloseButton
          className="provision-result-dismiss"
          aria-label="Dismiss cart send result"
          onClick={onDismiss}
        />
      </Group>
      <Text size="sm">{summary.primary}</Text>
      {summary.cartConfirmLine !== undefined && (
        <Text size="sm" c="dimmed">{summary.cartConfirmLine}</Text>
      )}
      {summary.openCartLabel !== undefined && cartUrl !== undefined && cartUrl !== '' && (
        <Anchor href={cartUrl} target="_blank" rel="noopener noreferrer" size="sm">
          {summary.openCartLabel}
        </Anchor>
      )}

      {sentTotal > 0 && (
        <Stack gap={4}>
          <Text size="sm" fw={600}>{PROVISION_GROUP_SENT}</Text>
          <Text size="sm">
            {sentTotal === 1 ? '1 item' : `${sentTotal} items`}
          </Text>
        </Stack>
      )}

      {groups.notAdded.length > 0 && (
        <Stack gap={4}>
          <Text size="sm" fw={600}>{PROVISION_GROUP_NOT_ADDED}</Text>
          {notAddedVisible.map((row, index) => (
            <Text key={`${row.entryId}-${row.name}-${index}`} size="sm" className="provision-result-item">
              {provisionItemReasonLine(row.name, storeLabel, row.reason)}
            </Text>
          ))}
          {!showAllNotAdded && notAddedHidden > 0 && (
            <Button
              className="provision-result-action"
              variant="subtle"
              size="compact-sm"
              onClick={() => setShowAllNotAdded(true)}
            >
              {`Show all ${groups.notAdded.length}`}
            </Button>
          )}
        </Stack>
      )}

      {groups.notConfirmed.length > 0 && (
        <Stack gap="xs">
          <Text size="sm" fw={600}>{PROVISION_GROUP_NOT_CONFIRMED}</Text>
          {groups.notConfirmed.slice(0, 50).map((row) => (
            <Stack key={row.entryId || row.name} gap={4} className="provision-result-item">
              <Text size="sm">{row.name}</Text>
              {row.entryId !== '' && (
                <Group gap="xs" wrap="wrap">
                  <Button
                    className="provision-result-action"
                    size="compact-sm"
                    variant="light"
                    onClick={() => onResolveUnknown(row.entryId, true)}
                  >
                    {`It reached ${storeLabel}`}
                  </Button>
                  <Button
                    className="provision-result-action"
                    size="compact-sm"
                    variant="default"
                    onClick={() => onResolveUnknown(row.entryId, false)}
                  >
                    It did not
                  </Button>
                </Group>
              )}
            </Stack>
          ))}
        </Stack>
      )}
    </Stack>
  );
};
