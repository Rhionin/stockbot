import { describe, expect, it } from 'vitest';
import { batchConfirmLabel, batchReviewHeading, reviewSelectedLabel, selectAllInSessionLabel, selectScanLabel } from './batchReview';

describe('batch review copy', () => {
  it('names the count and keeps one scan singular', () => {
    expect(batchReviewHeading(1)).toBe('Confirm 1 scan');
    expect(batchReviewHeading(3)).toBe('Confirm 3 scans');
    expect(batchConfirmLabel(3)).toBe('Confirm 3');
    expect(reviewSelectedLabel(2)).toBe('Review 2 selected');
  });

  it('labels the checkboxes', () => {
    expect(selectScanLabel('Oats')).toBe('Select Oats');
    expect(selectAllInSessionLabel('9:00 AM')).toBe('Select all eligible scans in 9:00 AM');
  });
});
