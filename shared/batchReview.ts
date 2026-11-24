import { formatBatchScanCount } from './queueUtils';

export const SELECT_ALL_LABEL = 'Select all';
export const CLEAR_SELECTION_LABEL = 'Clear';
export const BATCH_REVIEW_BACK = 'Back';
export const BATCH_CONFIRM_FAILURE = 'Those scans could not be confirmed.';

export const selectAllInSessionLabel = (timeRange: string): string =>
  `Select all eligible scans in ${timeRange}`;

export const selectScanLabel = (name: string): string => `Select ${name}`;

export const reviewSelectedLabel = (count: number): string => `Review ${count} selected`;

export const batchReviewHeading = (count: number): string => `Confirm ${formatBatchScanCount(count)}`;

export const batchConfirmLabel = (count: number): string => `Confirm ${count}`;

export const SELECT_ALL_IN_TAB_LABEL = 'Select all eligible scans for batch approval';
