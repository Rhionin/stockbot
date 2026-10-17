import { describe, expect, it } from 'vitest';
import { formatShoppingQuantity } from './quantity';

describe('formatShoppingQuantity', () => {
  it('separates a count from a size and leaves a single part alone', () => {
    const cases = [
      { count: 1, label: '14.5 oz', expected: '1 × 14.5 oz' },
      { count: 6, label: '12 fl oz', expected: '6 × 12 fl oz' },
      { count: 1, label: '15 oz cans', expected: '1 × 15 oz cans' },
      { count: 1, label: '1 gal', expected: '1 × 1 gal' },
      { count: 1, label: '1 Gallon', expected: '1 × 1 Gallon' },
      { count: 3, label: '1 qt', expected: '3 × 1 qt' },
      { count: 1, label: '1 lb', expected: '1 × 1 lb' },
      { count: 1, label: '16oz', expected: '1 × 16oz' },
      { count: 1, label: '  14.5 oz  ', expected: '1 × 14.5 oz' },
      { count: 2, label: 'boxes', expected: '2 boxes' },
      { count: 1, label: 'bag', expected: '1 bag' },
      { count: 1, label: 'can', expected: '1 can' },
      { count: 2, label: 'units', expected: '2 units' },
      { count: null, label: '14.5 oz', expected: '14.5 oz' },
      { count: 3, label: '', expected: '3' },
    ] as const;

    for (const row of cases) {
      expect(formatShoppingQuantity(row.count, row.label), `${row.count} ${row.label}`).toBe(row.expected);
    }
  });
});
