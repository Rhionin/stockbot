import { describe, expect, it } from 'vitest';
import { groupStock, stockColors, stockState } from './stockState';

describe('stockState', () => {
  it('maps a stocked quiet shelf to full, a short shelf to low, and an empty shelf to out', () => {
    expect(stockState(12, false)).toBe('full');
    expect(stockState(2, true)).toBe('low');
    expect(stockState(0, true)).toBe('out');
    expect(stockState(0, false)).toBe('out');
  });

  it('leaves an empty group unmarked as out', () => {
    expect(groupStock(0, false, 0)).toBe('full');
    expect(groupStock(0, true, 2)).toBe('out');
    expect(groupStock(4, true, 2)).toBe('low');
  });

  it('locks the approved swatches', () => {
    expect(stockColors).toEqual({
      full: '#0d9488',
      low: '#f59e0b',
      out: '#ef4444',
    });
  });
});
