import { useState } from 'react';
import { Alert, Button, Stack, VisuallyHidden } from '@mantine/core';
import { batchCommitScanEntries } from '../../api/client';
import type { BatchCommitResponse } from '../../api/contract';

export interface BatchReviewPanelProps {
  selectedIds: string[];
  onComplete: (response: BatchCommitResponse) => void;
}

export const BatchReviewPanel = ({ selectedIds, onComplete }: BatchReviewPanelProps) => {
  const [error, setError] = useState('');
  const [submitting, setSubmitting] = useState(false);

  const handleApprove = async () => {
    setSubmitting(true);
    setError('');
    try {
      const response = await batchCommitScanEntries({
        scanEntryIds: selectedIds,
        commit: true,
      });
      onComplete(response);
    } catch (requestError) {
      setError(requestError instanceof Error ? requestError.message : 'Unable to confirm selected scans.');
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
          Unable to confirm selected scans.
        </Alert>
      )}
    </Stack>
  );
};
