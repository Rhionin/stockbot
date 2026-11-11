import { describe, expect, it } from 'vitest';
import {
  accentBorder,
  amberDeep,
  amberScale,
  brandTeal,
  brandTealDeep,
  mobileChrome,
  nothingOnHandLight,
  stockColors,
  tealScale,
} from './brandTokens';

const CONTRACT_TEAL_SCALE = [
  '#f0fdfa',
  '#ccfbf1',
  '#99f6e4',
  '#5eead4',
  '#2dd4bf',
  '#14b8a6',
  '#0d9488',
  '#0f766e',
  '#115e59',
  '#134e4a',
] as const;

const CONTRACT_AMBER_SCALE = [
  '#fffbeb',
  '#fef3c7',
  '#fde68a',
  '#fcd34d',
  '#fbbf24',
  '#f7b027',
  '#f59e0b',
  '#d97706',
  '#b45309',
  '#92400e',
] as const;

describe('shared brandTokens', () => {
  it('exports the full pre-unification teal and amber 10-shade scales', () => {
    expect([...tealScale]).toEqual([...CONTRACT_TEAL_SCALE]);
    expect([...amberScale]).toEqual([...CONTRACT_AMBER_SCALE]);
  });

  it('exports the locked brand, stock, nothing-on-hand, and accent edge values', () => {
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
    expect(mobileChrome.ink).toBe('#14242c');
    expect(mobileChrome.white).toBe('#ffffff');
  });
});
