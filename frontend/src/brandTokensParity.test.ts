import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import { DEFAULT_THEME, mergeMantineTheme } from '@mantine/core';
import { describe, expect, it } from 'vitest';
import { stockColors } from './stockState';
import { theme } from './theme';

/** Pre-Step-2a web literals (master @003ba8b1). Hex compared case-insensitively. */
const WEB_BASELINE_TEAL_SCALE = [
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

const WEB_BASELINE_AMBER_SCALE = [
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

const WEB_BASELINE_STOCK = {
  full: '#0d9488',
  low: '#f59e0b',
  out: '#ef4444',
} as const;

const WEB_BASELINE_NOTHING = {
  wash: '#fffbeb',
  ink: '#92400e',
  border: '#b45309',
} as const;

const WEB_BASELINE_ACCENT_BORDER = 'rgba(13, 148, 136, 0.5)';

function normHex(color: string): string {
  return color.startsWith('#') ? color.toLowerCase() : color;
}

function normScale(scale: readonly string[] | undefined): string[] {
  return (scale ?? []).map(normHex);
}

const repoRoot = join(process.cwd(), '..');
const indexCss = readFileSync(join(repoRoot, 'frontend/src/index.css'), 'utf8');

const fullTheme = mergeMantineTheme(DEFAULT_THEME, theme);

function cssVar(name: string): string | null {
  const match = new RegExp(`${name}:\\s*([^;]+);`).exec(indexCss);
  return match?.[1]?.trim() ?? null;
}

describe('web brand token parity', () => {
  it('preserves pre-refactor Mantine teal and amber 10-shade scales', () => {
    expect(normScale(fullTheme.colors?.teal)).toEqual([...WEB_BASELINE_TEAL_SCALE]);
    expect(normScale(fullTheme.colors?.amber)).toEqual([...WEB_BASELINE_AMBER_SCALE]);
  });

  it('preserves pre-refactor stock swatches wired through stockState', () => {
    expect({
      full: normHex(stockColors.full),
      low: normHex(stockColors.low),
      out: normHex(stockColors.out),
    }).toEqual(WEB_BASELINE_STOCK);
  });

  it('preserves pre-refactor light nothing-on-hand and accent edge CSS variables', () => {
    expect(normHex(cssVar('--stock-full') ?? '')).toBe(WEB_BASELINE_STOCK.full);
    expect(normHex(cssVar('--stock-low') ?? '')).toBe(WEB_BASELINE_STOCK.low);
    expect(normHex(cssVar('--stock-out') ?? '')).toBe(WEB_BASELINE_STOCK.out);
    expect(cssVar('--accent-border')).toBe(WEB_BASELINE_ACCENT_BORDER);
    expect(normHex(cssVar('--scan-nothing-wash') ?? '')).toBe(WEB_BASELINE_NOTHING.wash);
    expect(normHex(cssVar('--scan-nothing-ink') ?? '')).toBe(WEB_BASELINE_NOTHING.ink);
    expect(normHex(cssVar('--scan-nothing-border') ?? '')).toBe(WEB_BASELINE_NOTHING.border);
  });
});
