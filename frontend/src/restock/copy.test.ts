import { describe, expect, it } from 'vitest';
import { plainCopy, restockCopy } from './copy';
import { asRestock } from './types';

describe('restock copy', () => {
  it('uses plain source labels', () => {
    const lines = [
      restockCopy.sectionBody,
      restockCopy.dontItem,
      restockCopy.dontItemHint,
      restockCopy.useCategory,
      restockCopy.household,
      'Set on this item',
      'From Snacks & sweets',
      'From Salad kits group',
      'Use category target',
      'Household default',
    ];
    for (const line of lines) {
      expect(plainCopy(line), line).toBe(true);
    }
  });

  it('ignores an inventory list returned in place of a restock snapshot', () => {
    expect(asRestock([{ item: { id: 'milk' } }])).toBeNull();
    expect(asRestock(null)).toBeNull();
  });
});
