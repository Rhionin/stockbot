import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState, type MutableRefObject } from 'react';
import { Anchor, Button, CloseButton, Group, Loader, Stack, Text } from '@mantine/core';
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
  PROVISION_RESOLVE_FAILED_LEAD,
  PROVISION_RESOLVE_TRY_AGAIN,
  PROVISION_RESOLVE_UNDO_MS,
  groupProvisionResults,
  provisionCheckInCartAria,
  provisionCheckNotInCartAria,
  provisionItemReasonLine,
  provisionPartialReconnectPrompt,
  provisionNothingSentReconnectLabel,
  provisionRowRetryAria,
  provisionSectionHeadingCheck,
  provisionSectionHeadingCouldntSend,
  provisionSectionHeadingSent,
  provisionStoreLabel,
  provisionUndoAria,
  provisionSummaryCopy,
  provisionWholeRequestActionLabel,
  provisionWholeRequestMessage,
  type ProvisionWholeRequestError,
} from '@stockbot/shared/provisionExportUX';

export type CheckResolution = 'in' | 'not';

export interface ProvisionResultPanelProps {
  report: ProvisionReport | null;
  envelopeError?: ProvisionWholeRequestError;
  provider: ProviderInfo | null;
  cartUrl?: string;
  onDismiss: () => void;
  onTryAgain: () => void;
  onReconnect: () => void;
  onResolveUnknown: (entryId: string, reachedProvider: boolean) => Promise<void>;
  /** Set to a function that commits every pending choice and resolves false if any save failed. */
  /** Called when flush/dismiss/send is blocked because one or more row saves failed. */
  onBlockedSave?: (failedEntryIds: string[]) => void;
  /** Called when every pending row save has succeeded again. */
  onBlockedSaveCleared?: () => void;
  flushRef?: MutableRefObject<((options?: { blockedSend?: boolean }) => true | Promise<boolean>) | null>;
}

const NothingSentEnvelope = ({
  code,
  storeLabel,
  onDismiss,
  onTryAgain,
  onReconnect,
}: {
  code: ProvisionWholeRequestError;
  storeLabel: string;
  onDismiss: () => void;
  onTryAgain: () => void;
  onReconnect: () => void;
}) => {
  const message = provisionWholeRequestMessage(code, storeLabel);
  const actionLabel = provisionWholeRequestActionLabel(code, storeLabel);
  const reconnect = code === 'reconnect_required';

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
      <Text size="sm">{message}</Text>
      <Button
        className="provision-result-action"
        variant="light"
        onClick={reconnect ? onReconnect : onTryAgain}
      >
        {actionLabel}
      </Button>
    </Stack>
  );
};

export const ProvisionResultPanel = ({
  report,
  envelopeError,
  provider,
  cartUrl,
  onDismiss,
  onTryAgain,
  onReconnect,
  onResolveUnknown,
  flushRef,
  onBlockedSave,
  onBlockedSaveCleared,
}: ProvisionResultPanelProps) => {
  const [showAllNotAdded, setShowAllNotAdded] = useState(false);
  const [showAllSent, setShowAllSent] = useState(false);
  const [resolved, setResolved] = useState<Record<string, CheckResolution>>({});
  const [committed, setCommitted] = useState<Record<string, true>>({});
  const [savingRows, setSavingRows] = useState<Record<string, true>>({});
  const [rowErrors, setRowErrors] = useState<Record<string, string>>({});
  // A choice stays local until the undo window ends: the server's ledger advance can't be undone.
  const pending = useRef<Record<string, { choice: CheckResolution; timer: ReturnType<typeof setTimeout> }>>({});
  const failedRetry = useRef<Record<string, CheckResolution>>({});
  const inFlight = useRef<Record<string, Promise<boolean>>>({});
  const resolveRef = useRef(onResolveUnknown);
  useEffect(() => {
    resolveRef.current = onResolveUnknown;
  }, [onResolveUnknown]);
  const rowRefs = useRef<Record<string, HTMLDivElement | null>>({});
  const storeLabel = provisionStoreLabel(provider?.displayName);
  const groups = useMemo(
    () => (report !== null ? groupProvisionResults(report) : null),
    [report],
  );
  const legacyEnvelope = groups?.wholeRequestError;
  const blockingCode = envelopeError ?? legacyEnvelope;

  const summary = useMemo(() => {
    if (groups === null) return { lines: [] as string[] };
    return provisionSummaryCopy({ N: groups.N, M: groups.M, U: groups.U }, storeLabel);
  }, [groups, storeLabel]);

  const notAddedVisible = groups === null || showAllNotAdded || groups.notAdded.length <= PROVISION_SHOW_ALL_THRESHOLD
    ? (groups?.notAdded ?? []).slice(0, PROVISION_ROW_CAP)
    : groups.notAdded.slice(0, PROVISION_SHOW_ALL_THRESHOLD);
  const notAddedHidden = (groups?.notAdded.length ?? 0) - notAddedVisible.length;

  const sentVisible = groups === null || showAllSent || groups.sentRows.length <= PROVISION_SENT_SHOW_ALL_THRESHOLD
    ? (groups?.sentRows ?? []).slice(0, PROVISION_ROW_CAP)
    : groups.sentRows.slice(0, PROVISION_SENT_SHOW_ALL_THRESHOLD);
  const sentHidden = (groups?.sentRows.length ?? 0) - sentVisible.length;

  const focusRowRetry = (entryId: string) => {
    requestAnimationFrame(() => {
      const row = rowRefs.current[entryId];
      const retry = row?.querySelector('.provision-row-retry');
      if (retry instanceof HTMLButtonElement) {
        retry.focus();
      }
    });
  };

  const focusRowContainer = (entryId: string) => {
    requestAnimationFrame(() => {
      rowRefs.current[entryId]?.focus();
    });
  };

  const focusRow = (entryId: string) => {
    requestAnimationFrame(() => {
      const row = rowRefs.current[entryId];
      if (row === null || row === undefined) return;
      const retry = row.querySelector('.provision-row-retry');
      if (retry instanceof HTMLButtonElement) {
        retry.focus();
        return;
      }
      const button = row.querySelector('button');
      if (button instanceof HTMLButtonElement) {
        button.focus();
        return;
      }
      const fallback = row.querySelector('[data-provision-row-focus]');
      if (fallback instanceof HTMLElement) fallback.focus();
    });
  };

  const announceBlocked = useCallback((failedIds: string[], blockedSend: boolean) => {
    const first = failedIds[0];
    if (first !== undefined) {
      rowRefs.current[first]?.scrollIntoView({ block: 'nearest', behavior: 'smooth' });
      focusRowRetry(first);
    }
    if (blockedSend) {
      onBlockedSave?.(failedIds);
    }
  }, [onBlockedSave]);

  const clearBlockedIfClean = useCallback(() => {
    onBlockedSaveCleared?.();
  }, [onBlockedSaveCleared]);

  const persistChoice = useCallback((entryId: string, choice: CheckResolution, ok: boolean) => {
    if (ok) {
      delete failedRetry.current[entryId];
      setCommitted((prev) => ({ ...prev, [entryId]: true }));
      setResolved((prev) => ({ ...prev, [entryId]: choice }));
      setRowErrors((prev) => {
        const next = { ...prev };
        delete next[entryId];
        if (Object.keys(next).length === 0) {
          queueMicrotask(() => onBlockedSaveCleared?.());
        }
        return next;
      });
      return;
    }
    failedRetry.current[entryId] = choice;
    setResolved((prev) => {
      const next = { ...prev };
      delete next[entryId];
      return next;
    });
    setRowErrors((prev) => ({ ...prev, [entryId]: PROVISION_RESOLVE_FAILED_LEAD }));
  }, [onBlockedSaveCleared]);

  const saveChoice = useCallback((entryId: string, choice: CheckResolution): Promise<boolean> => {
    setSavingRows((prev) => ({ ...prev, [entryId]: true }));
    focusRowContainer(entryId);
    const saving = resolveRef.current(entryId, choice === 'in').then(
      () => {
        persistChoice(entryId, choice, true);
        return true;
      },
      () => {
        persistChoice(entryId, choice, false);
        return false;
      },
    ).finally(() => {
      delete inFlight.current[entryId];
      setSavingRows((prev) => {
        const next = { ...prev };
        delete next[entryId];
        return next;
      });
      focusRow(entryId);
    });
    inFlight.current[entryId] = saving;
    return saving;
  }, [persistChoice]);

  const commit = useCallback((entryId: string): Promise<boolean> => {
    const item = pending.current[entryId];
    if (item === undefined) {
      const retry = failedRetry.current[entryId];
      if (retry !== undefined) return saveChoice(entryId, retry);
      return Promise.resolve(true);
    }
    clearTimeout(item.timer);
    const choice = item.choice;
    delete pending.current[entryId];
    return saveChoice(entryId, choice);
  }, [saveChoice]);

  const retryRowSave = (entryId: string) => {
    const choice = failedRetry.current[entryId];
    if (choice === undefined) return;
    void saveChoice(entryId, choice).then((ok) => {
      if (ok) clearBlockedIfClean();
    });
  };

  // Returns true at once when nothing is waiting, so a new send clears the old result without a tick.
  const flushAll = useCallback((options?: { blockedSend?: boolean }): true | Promise<boolean> => {
    const jobs: { id: string; promise: Promise<boolean> }[] = [];
    for (const [id, promise] of Object.entries(inFlight.current)) {
      jobs.push({ id, promise });
    }
    for (const id of Object.keys(pending.current)) {
      jobs.push({ id, promise: commit(id) });
    }
    if (jobs.length === 0) {
      onBlockedSaveCleared?.();
      return true;
    }
    const blockedSend = options?.blockedSend === true;
    return Promise.all(jobs.map((job) => job.promise.then((ok) => ({ id: job.id, ok })))).then((results) => {
      const failed = results.filter((result) => !result.ok).map((result) => result.id);
      if (failed.length > 0) {
        announceBlocked(failed, blockedSend);
        return false;
      }
      onBlockedSaveCleared?.();
      return true;
    });
  }, [announceBlocked, commit, onBlockedSaveCleared]);

  const flushAllRef = useRef(flushAll);
  flushAllRef.current = flushAll;

  useLayoutEffect(() => {
    if (flushRef !== undefined) {
      flushRef.current = (options) => flushAllRef.current(options);
    }
    return () => {
      if (flushRef !== undefined) flushRef.current = null;
    };
  }, [flushRef]);

  useEffect(() => {
    const onPageHide = () => void flushAllRef.current();
    window.addEventListener('pagehide', onPageHide);
    return () => {
      window.removeEventListener('pagehide', onPageHide);
    };
  }, []);

  useEffect(() => () => {
    // Leaving the page ends the undo window. Dismiss and a new send flush first, so they can keep the panel on failure.
    void flushAllRef.current();
  }, []);

  const confirmCheck = (entryId: string, reachedProvider: boolean) => {
    if (entryId === '') return;
    const choice: CheckResolution = reachedProvider ? 'in' : 'not';
    setRowErrors((prev) => {
      const next = { ...prev };
      delete next[entryId];
      return next;
    });
    setResolved((prev) => ({ ...prev, [entryId]: choice }));
    pending.current[entryId] = {
      choice,
      timer: setTimeout(() => commit(entryId), PROVISION_RESOLVE_UNDO_MS),
    };
    focusRow(entryId);
  };

  const undoResolution = (entryId: string) => {
    const item = pending.current[entryId];
    if (item === undefined) return;
    clearTimeout(item.timer);
    delete pending.current[entryId];
    setResolved((prev) => {
      const next = { ...prev };
      delete next[entryId];
      return next;
    });
    focusRow(entryId);
  };

  const dismiss = async () => {
    // Keep the panel when a save fails, so the row alert stays visible.
    if (await flushAll()) onDismiss();
  };

  if (blockingCode !== undefined) {
    return (
      <NothingSentEnvelope
        code={blockingCode}
        storeLabel={storeLabel}
        onDismiss={onDismiss}
        onTryAgain={onTryAgain}
        onReconnect={onReconnect}
      />
    );
  }

  if (groups === null || report === null) {
    return null;
  }

  const reconnectLabel = provisionNothingSentReconnectLabel(storeLabel);

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
          onClick={() => void dismiss()}
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
              {`Show all ${groups.sentRows.length}`}
            </Button>
          )}
        </Stack>
      )}

      {groups.M > 0 && (
        <Stack gap={4}>
          <Text size="sm" fw={600}>{provisionSectionHeadingCouldntSend(groups.M)}</Text>
          {notAddedVisible.map((row, index) => (
            <Text key={`${row.entryId}-${row.name}-${index}`} size="sm" className="provision-result-item">
              {provisionItemReasonLine(row.name, storeLabel, row.reason, row, report.reconnectRequired === true)}
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
            const saving = entryId !== '' && savingRows[entryId] === true;
            return (
              <div
                key={entryId || row.name}
                className="provision-check-row"
                tabIndex={saving ? -1 : undefined}
                ref={(node) => {
                  if (entryId !== '') rowRefs.current[entryId] = node;
                }}
              >
                <Stack gap={4} className="provision-result-item">
                  <Text size="sm">{row.name}</Text>
                {entryId !== '' && rowErrors[entryId] !== undefined && (
                  <Stack gap={4} role="alert">
                    <Text size="sm" c="red">{rowErrors[entryId]}</Text>
                    <Button
                      className="provision-result-action provision-row-retry"
                      size="compact-sm"
                      variant="light"
                      aria-label={provisionRowRetryAria(row.name)}
                      onClick={() => retryRowSave(entryId)}
                    >
                      {PROVISION_RESOLVE_TRY_AGAIN}
                    </Button>
                  </Stack>
                )}
                {entryId !== '' && choice === undefined && rowErrors[entryId] === undefined && (
                  <Group gap="xs" wrap="wrap">
                    <Button
                      className="provision-result-action"
                      size="compact-sm"
                      variant="light"
                      aria-label={provisionCheckInCartAria(row.name)}
                      onClick={() => confirmCheck(entryId, true)}
                    >
                      {PROVISION_CHECK_IN_CART}
                    </Button>
                    <Button
                      className="provision-result-action"
                      size="compact-sm"
                      variant="default"
                      aria-label={provisionCheckNotInCartAria(row.name)}
                      onClick={() => confirmCheck(entryId, false)}
                    >
                      {PROVISION_CHECK_NOT_IN_CART}
                    </Button>
                  </Group>
                )}
                {entryId !== '' && choice !== undefined && rowErrors[entryId] === undefined && (
                  <Group gap="xs" wrap="wrap" align="center" className="provision-check-marked">
                    <Text size="sm">
                      {choice === 'in' ? PROVISION_MARKED_IN_CART : PROVISION_MARKED_NOT_IN_CART}
                    </Text>
                    {saving && (
                      <Loader className="provision-row-saving-spinner" size="xs" aria-hidden="true" />
                    )}
                    {committed[entryId] !== true && !saving && (
                      <Button
                        className="provision-result-action"
                        size="compact-sm"
                        variant="subtle"
                        aria-label={provisionUndoAria(row.name)}
                        onClick={() => undoResolution(entryId)}
                      >
                        {PROVISION_UNDO_LABEL}
                      </Button>
                    )}
                  </Group>
                )}
                </Stack>
              </div>
            );
          })}
        </Stack>
      )}

      {report.reconnectRequired === true && (
        <Stack gap="xs">
          <Text size="sm">{provisionPartialReconnectPrompt(storeLabel)}</Text>
          <Button
            className="provision-result-action"
            variant="light"
            onClick={onReconnect}
          >
            {reconnectLabel}
          </Button>
        </Stack>
      )}
    </Stack>
  );
};
