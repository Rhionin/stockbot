import { colors } from './ui/theme';

/** Pre-Step-2a mobile literals (master @003ba8b1). Hex compared case-insensitively. */
const MOBILE_BASELINE_BRAND = {
  shelf: '#0D9488',
  shelfDeep: '#115E59',
  using: '#92400E',
  nothingWash: '#FFFBEB',
  nothingInk: '#92400E',
  nothingBorder: '#B45309',
  successEdge: 'rgba(13, 148, 136, 0.5)',
} as const;

const MOBILE_BASELINE_STOCK_FULL = '#0d9488';

function normHex(color: string): string {
  return color.startsWith('#') ? color.toLowerCase() : color;
}

describe('mobile brand token parity', () => {
  it('preserves pre-refactor shelf, stock, nothing-on-hand, and accent edge colors', () => {
    expect(normHex(colors.shelf)).toBe(normHex(MOBILE_BASELINE_BRAND.shelf));
    expect(normHex(colors.shelfDeep)).toBe(normHex(MOBILE_BASELINE_BRAND.shelfDeep));
    expect(normHex(colors.shelf)).toBe(normHex(MOBILE_BASELINE_STOCK_FULL));
    expect(normHex(colors.using)).toBe(normHex(MOBILE_BASELINE_BRAND.using));
    expect(normHex(colors.using)).toBe(normHex(MOBILE_BASELINE_BRAND.nothingInk));
    expect(normHex(colors.nothingWash)).toBe(normHex(MOBILE_BASELINE_BRAND.nothingWash));
    expect(normHex(colors.nothingInk)).toBe(normHex(MOBILE_BASELINE_BRAND.nothingInk));
    expect(normHex(colors.nothingBorder)).toBe(normHex(MOBILE_BASELINE_BRAND.nothingBorder));
    expect(colors.successEdge).toBe(MOBILE_BASELINE_BRAND.successEdge);
  });
});
