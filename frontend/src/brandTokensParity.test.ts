import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import { DEFAULT_THEME, mergeMantineTheme } from '@mantine/core';
import { describe, expect, it } from 'vitest';
import {
  accentBorder,
  amberDeep,
  amberScale,
  brandTeal,
  brandTealDeep,
  nothingOnHandLight,
  stockColors as sharedStockColors,
  tealScale,
} from '@stockbot/shared/brandTokens';
import { stockColors } from './stockState';
import { theme } from './theme';

const repoRoot = join(process.cwd(), '..');
const indexCss = readFileSync(join(repoRoot, 'frontend/src/index.css'), 'utf8');

const fullTheme = mergeMantineTheme(DEFAULT_THEME, theme);

function cssVar(name: string): string | null {
  const match = new RegExp(`${name}:\\s*([^;]+);`).exec(indexCss);
  return match?.[1]?.trim() ?? null;
}

describe('web brand token parity', () => {
  it('matches the Mantine theme scales and stock swatches', () => {
    expect(fullTheme.colors?.teal).toEqual([...tealScale]);
    expect(fullTheme.colors?.amber).toEqual([...amberScale]);
    expect(fullTheme.colors?.teal?.[6]).toBe(brandTeal);
    expect(fullTheme.colors?.teal?.[8]).toBe(brandTealDeep);
    expect(fullTheme.colors?.amber?.[9]).toBe(amberDeep);
    expect(stockColors).toEqual(sharedStockColors);
  });

  it('matches the light nothing-on-hand and accent edge CSS variables', () => {
    expect(cssVar('--stock-full')).toBe(sharedStockColors.full);
    expect(cssVar('--stock-low')).toBe(sharedStockColors.low);
    expect(cssVar('--stock-out')).toBe(sharedStockColors.out);
    expect(cssVar('--accent-border')).toBe(accentBorder);
    expect(cssVar('--scan-nothing-wash')).toBe(nothingOnHandLight.wash);
    expect(cssVar('--scan-nothing-ink')).toBe(nothingOnHandLight.ink);
    expect(cssVar('--scan-nothing-border')).toBe(nothingOnHandLight.border);
  });
});
