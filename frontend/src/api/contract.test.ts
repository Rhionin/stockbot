import { describe, expect, it } from 'vitest';
import { formatPartialRemoval } from './contract';

const oneUnnamed = '1 card had fewer on hand than scanned, so only what was on hand was taken out.';

describe('formatPartialRemoval', () => {
  it.each([
    {
      name: 'count 1 with no name',
      names: { 'scan-1': '' },
      line: oneUnnamed,
    },
  ])('$name', ({ names, line }) => {
    expect(formatPartialRemoval({
      updatedCount: 1,
      committedIds: ['scan-1'],
      stockOutCounts: {
        'scan-1': { removedCount: 1, requestedCount: 3 },
      },
    }, names)).toBe(line);
  });
});
