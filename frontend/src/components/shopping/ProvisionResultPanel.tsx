import { useMemo, useRef, useState } from 'react';
import { Anchor, Button, CloseButton, Group, Stack, Text } from '@mantine/core';
import type { ProviderInfo, ProvisionReport } from '@stockbot/shared/types';
import {
  PROVISION_CHECK_IN_CART,
  PROVISION_CHECK_NOT_IN_CART,
  PROVISION_MARKED_IN_CART,
  PROVISION_MARKED_NOT_IN_CART,
  PROVISION_RESULT_PANEL_LABEL,
  PROVISION_ROW_CAP,
  PROVISION_SENT_SHOW_ALL_THRESHOLD,
  PROVISION_SHOW_ALL_THRESHOLD,
  PROVISION_UNDO_LABEL,
  groupProvisionResults,
  provisionCheckInCartAria,
  provisionCheckNotInCartAria,
  provisionItemReasonLine,
  provisionSectionHeadingCheck,
  provisionSectionHeadingCouldntSend,
  provisionSectionHeadingSent,
  provisionStoreLabel,
  provisionSummaryCopy,
  provisionWholeRequestActionLabel,
  provisionWholeRequestMessage,
} from '@stockbot/shared/provisionExportUX';

export type CheckResolution = 'in' | 'not';

export interface ProvisionResultPanelProps {
  report: ProvisionReport;
  provider: ProviderInfo | null;
  cartUrl?: string;
  onDismiss: () => void;
  onTryAgain: () => void;
  onReconnect: () => void;
  onResolveUnknown: (entryId: string, reachedProvider: boolean) => Promise<void>;
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
  const [showAllSent, setShowAllSent] = useState(false);
  const [resolved, setResolved] = useState<Record<string, CheckResolution>>({});
  const rowRefs = useRef<Record<string, HTMLDivElement | null>>({});
  const storeLabel = provisionStoreLabel(provider?.displayName);
  const groups = useMemo(() => groupProvisionResults(report), [report]);
  const summary = useMemo(
    () => provisionSummaryCopy({ N: groups.N, M: groups.M, U: groups.U }, storeLabel),
    [groups.M, groups.N, groups.U, storeLabel],
  );

  const notAddedVisible = showAllNotAdded || groups.notAdded.length <= PROVISION_SHOW_ALL_THRESHOLD
    ? groups.notAdded.slice(0, PROVISION_ROW_CAP)
    : groups.notAdded.slice(0, PROVISION_SHOW_ALL_THRESHOLD);
  const notAddedHidden = groups.notAdded.length - notAddedVisible.length;

  const sentVisible = showAllSent || groups.sentRows.length <= PROVISION_SENT_SHOW_ALL_THRESHOLD
    ? groups.sentRows.slice(0, PROVISION_ROW_CAP)
    : groups.sentRows.slice(0, PROVISION_SENT_SHOW_ALL_THRESHOLD);
  const sentHidden = groups.sentRows.length - sentVisible.length;

  const markResolved = (entryId: string, choice: CheckResolution) => {
    setResolved((prev) => ({ ...prev, [entryId]: choice }));
    requestAnimationFrame(() => {
      rowRefs.current[entryId]?.querySelector('button')?.focus();
    });
  };

  const undoResolution = (entryId: string) => {
    setResolved((prev) => {
      const next = { ...prev };
      delete next[entryId];
      return next;
    });
    requestAnimationFrame(() => {
      rowRefs.current[entryId]?.querySelector('button')?.focus();
    });
  };

  const confirmCheck = async (entryId: string, reachedProvider: boolean) => {
    if (entryId === '') return;
    await onResolveUnknown(entryId, reachedProvider);
    markResolved(entryId, reachedProvider ? 'in' : 'not');
  };

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
      {summary.lines.map((line) => (
        <Text key={line} size="sm">{line}</Text>
      ))}
      {summary.cartConfirmLine !== undefined && (
        <Text size="sm" c="dimmed">{summary.cartConfirmLine}</Text>
      )}
      {summary.openCartLabel !== undefined && cartUrl !== undefined && cartUrl !== '' && (
        <Anchor className="provision-result-link" href={cartUrl} target="_blank" rel="noopener noreferrer" size="sm">
          {summary.openCartLabel}
        </Anchor>
      )}

      {groups.N > 0 && groups.sentRows.length > 0 && (
        <Stack gap={4}>
          <Text size="sm" fw={600}>{provisionSectionHeadingSent(groups.N)}</Text>
          {sentVisible.map((row, index) => (
            <Text key={`${row.entryId}-${row.name}-${index}`} size="sm" className="provision-result-item">
              {row.name}
            </Text>
          ))}
          {!showAllSent && sentHidden > 0 && (
            <Button
              className="provision-result-action"
              variant="subtle"
              size="compact-sm"
              onClick={() => setShowAllSent(true)}
            >
              {`Show all ${groups.N}`}
            </Button>
          )}
        </Stack>
      )}

      {groups.M > 0 && (
        <Stack gap={4}>
          <Text size="sm" fw={600}>{provisionSectionHeadingCouldntSend(groups.M)}</Text>
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

      {groups.U > 0 && (
        <Stack gap="xs">
          <Text size="sm" fw={600}>{provisionSectionHeadingCheck(groups.U)}</Text>
          {groups.check.slice(0, PROVISION_ROW_CAP).map((row) => {
            const entryId = row.entryId;
            const choice = entryId !== '' ? resolved[entryId] : undefined;
            return (
              <div
                key={entryId || row.name}
                className="provision-check-row"
                ref={(node) => {
                  if (entryId !== '') rowRefs.current[entryId] = node;
                }}
              >
                <Stack gap={4} className="provision-result-item">
                  <Text size="sm">{row.name}</Text>
                {entryId !== '' && choice === undefined && (
                  <Group gap="xs" wrap="wrap">
                    <Button
                      className="provision-result-action"
                      size="compact-sm"
                      variant="light"
                      aria-label={provisionCheckInCartAria(row.name)}
                      onClick={() => void confirmCheck(entryId, true)}
                    >
                      {PROVISION_CHECK_IN_CART}
                    </Button>
                    <Button
                      className="provision-result-action"
                      size="compact-sm"
                      variant="default"
                      aria-label={provisionCheckNotInCartAria(row.name)}
                      onClick={() => void confirmCheck(entryId, false)}
                    >
                      {PROVISION_CHECK_NOT_IN_CART}
                    </Button>
                  </Group>
                )}
                {entryId !== '' && choice !== undefined && (
                  <Group gap="xs" wrap="wrap" align="center">
                    <Text size="sm">
                      {choice === 'in' ? PROVISION_MARKED_IN_CART : PROVISION_MARKED_NOT_IN_CART}
                    </Text>
                    <Button
                      className="provision-result-action"
                      size="compact-sm"
                      variant="subtle"
                      onClick={() => undoResolution(entryId)}
                    >
                      {PROVISION_UNDO_LABEL}
                    </Button>
                  </Group>
                )}
                </Stack>
              </div>
            );
          })}
        </Stack>
      )}
    </Stack>
  );
};
