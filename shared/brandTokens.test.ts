import { describe, expect, it } from 'vitest';
import {
  accentBorder,
  amberDeep,
  amberScale,
  brandTeal,
  brandTealDeep,
  nothingOnHandLight,
  stockColors,
  tealScale,
} from './brandTokens';

describe('shared brandTokens', () => {
  it('keeps the locked 10-shade teal and amber scales', () => {
    expect(tealScale).toHaveLength(10);
    expect(amberScale).toHaveLength(10);
    expect(brandTeal).toBe('#0d9488');
    expect(brandTealDeep).toBe('#115e59');
    expect(amberDeep).toBe('#92400e');
    expect(stockColors).toEqual({
      full: '#0d9488',
      low: '#f59e0b',
      out: '#ef4444',
    });
    expect(nothingOnHandLight).toEqual({
      wash: '#fffbeb',
      ink: '#92400e',
      border: '#b45309',
    });
    expect(accentBorder).toBe('rgba(13, 148, 136, 0.5)');
  });
});
