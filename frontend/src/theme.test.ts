import { DEFAULT_THEME, mergeMantineTheme } from '@mantine/core';
import { describe, expect, it } from 'vitest';
import { contrastRatio, flatten } from '@stockbot/shared/contrast';
import { cssVariablesResolver, theme } from './theme';

const fullTheme = mergeMantineTheme(DEFAULT_THEME, theme);

const token = (value: string | undefined, colors: typeof fullTheme.colors) => {
  if (value?.startsWith('#')) return value;
  const match = /^var\(--mantine-color-([a-z0-9]+)-(\d)\)$/.exec(value ?? '');
  if (!match) throw new Error(`unresolved token ${value}`);
  const scale = colors[match[1]];
  if (!Array.isArray(scale)) throw new Error(`missing scale ${match[1]}`);
  return scale[Number(match[2])];
};

describe('theme', () => {
  it('uses the locked teal as the primary brand color', () => {
    expect(theme.primaryColor).toBe('teal');
    expect(theme.colors?.teal?.[6]).toBe('#0d9488');
    expect(theme.colors?.amber?.[6]).toBe('#f59e0b');
  });

  it('keeps dimmed text and light-variant ink at WCAG AA', () => {
    const variables = cssVariablesResolver(fullTheme);
    const dimmedLight = token(variables.light['--mantine-color-dimmed'], fullTheme.colors);
    const dimmedDark = token(variables.dark['--mantine-color-dimmed'], fullTheme.colors);
    expect(dimmedLight).toBe('#495057');
    expect(dimmedDark).toBe('#b8b8b8');
    expect(contrastRatio(dimmedLight, fullTheme.white)).toBeGreaterThanOrEqual(4.5);
    expect(contrastRatio(dimmedDark, fullTheme.colors.dark[7])).toBeGreaterThanOrEqual(4.5);

    expect(token(variables.light['--mantine-color-teal-light-color'], fullTheme.colors)).toBe('#0f766e');
    expect(token(variables.light['--mantine-color-amber-light-color'], fullTheme.colors)).toBe('#b45309');
    expect(token(variables.light['--mantine-color-red-light-color'], fullTheme.colors)).toBe('#c92a2a');
  });

  it('keeps every color scale readable on its light wash, dark wash, and page background', () => {
    const variables = cssVariablesResolver(fullTheme);
    const paper = fullTheme.colors.dark[7];
    const failures: string[] = [];
    for (const name of Object.keys(fullTheme.colors)) {
      const scale = fullTheme.colors[name];
      if (!Array.isArray(scale) || scale.length < 10) continue;
      const pairs = [
        ['light wash', variables.light[`--mantine-color-${name}-light-color`], flatten(scale[6], 0.1, fullTheme.white)],
        ['dark wash', variables.dark[`--mantine-color-${name}-light-color`], flatten(scale[4], 0.15, paper)],
        ['light text', variables.light[`--mantine-color-${name}-text`], fullTheme.white],
        ['light outline', variables.light[`--mantine-color-${name}-outline`], fullTheme.white],
        ['dark text', variables.dark[`--mantine-color-${name}-text`], paper],
        ['dark outline', variables.dark[`--mantine-color-${name}-outline`], paper],
      ] as const;
      for (const [label, ink, background] of pairs) {
        const color = token(ink, fullTheme.colors);
        const ratio = contrastRatio(color, background);
        if (ratio < 4.5) failures.push(`${name} ${label} ${color} on ${background} is ${ratio.toFixed(2)}`);
      }
    }
    expect(failures).toEqual([]);
  });
});
