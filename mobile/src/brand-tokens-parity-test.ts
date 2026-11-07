import {
  accentBorder,
  amberDeep,
  brandTeal,
  brandTealDeep,
  nothingOnHandLight,
  stockColors,
} from '@stockbot/shared/brandTokens';
import { colors } from './ui/theme';

describe('mobile brand token parity', () => {
  it('reads the same brand, stock, nothing-on-hand, and accent edge as shared', () => {
    expect(colors.shelf).toBe(brandTeal);
    expect(colors.shelfDeep).toBe(brandTealDeep);
    expect(colors.shelf).toBe(stockColors.full);
    expect(colors.using).toBe(amberDeep);
    expect(colors.using).toBe(nothingOnHandLight.ink);
    expect(colors.nothingWash).toBe(nothingOnHandLight.wash);
    expect(colors.nothingInk).toBe(nothingOnHandLight.ink);
    expect(colors.nothingBorder).toBe(nothingOnHandLight.border);
    expect(colors.successEdge).toBe(accentBorder);
  });
});
