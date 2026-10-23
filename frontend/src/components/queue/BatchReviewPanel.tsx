import { useEffect, useState } from 'react';
import { Alert, Button, Stack, VisuallyHidden } from '@mantine/core';
import { batchCommitScanEntries } from '../../api/client';
import { offlineSentence, type BatchCommitResponse } from '../../api/contract';
import { clearOfflineNotice, subscribeOfflineClear } from './offlineNotice';

export interface BatchReviewPanelProps {
  selectedIds: string[];
  onComplete: (response: BatchCommitResponse) => void;
  onStart?: () => void;
}

export const BatchReviewPanel = ({ selectedIds, onComplete, onStart }: BatchReviewPanelProps) => {
  const [error, setError] = useState('');
  const [submitting, setSubmitting] = useState(false);

  useEffect(() => subscribeOfflineClear(() => setError('')), []);

  const handleApprove = async () => {
    setSubmitting(true);
    setError('');
    onStart?.();
    try {
      const response = await batchCommitScanEntries({
        scanEntryIds: selectedIds,
        commit: true,
      });
      clearOfflineNotice();
      onComplete(response);
    } catch (requestError) {
      const offline = offlineSentence(requestError);
      setError(offline ?? 'Unable to confirm selected scans.');
    } finally {
      setSubmitting(false);
    }
  };

  return (
    <Stack component="section" aria-labelledby="confirm-heading" gap={4} className="scan-batch-approve">
      <VisuallyHidden>
        <h2 id="confirm-heading">Confirm scans</h2>
      </VisuallyHidden>
      <Button
        size="compact-xs"
        onClick={() => void handleApprove()}
        disabled={selectedIds.length === 0}
        loading={submitting}
        aria-label={`Confirm ${selectedIds.length} selected scans`}
      >
        Confirm {selectedIds.length}
      </Button>
      {error !== '' && (
        <Alert color="red" py={4}>
          {error}
        </Alert>
      )}
    </Stack>
  );
};
