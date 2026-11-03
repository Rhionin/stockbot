import { DEFAULT_THEME, mergeMantineTheme } from '@mantine/core';
import { describe, expect, it } from 'vitest';
import { contrastRatio, flatten, parseHex } from '@stockbot/shared/contrast';
import { cssVariablesResolver, theme } from './theme';

const fullTheme = mergeMantineTheme(DEFAULT_THEME, theme);

const relativeLuminance = (color: string): number => {
  const [red, green, blue] = parseHex(color);
  const linear = (channel: number) => {
    const unit = channel / 255;
    return unit <= 0.04045 ? unit / 12.92 : ((unit + 0.055) / 1.055) ** 2.4;
  };
  return 0.2126 * linear(red) + 0.7152 * linear(green) + 0.0722 * linear(blue);
};

const token = (value: string | undefined, colors: typeof fullTheme.colors) => {
  if (value?.startsWith('#')) return value;
  const match = /^var\(--mantine-color-([a-z0-9]+)-(\d)\)$/.exec(value ?? '');
  if (!match) throw new Error(`unresolved token ${value}`);
  const scale = colors[match[1]];
  if (!Array.isArray(scale)) throw new Error(`missing scale ${match[1]}`);
  return scale[Number(match[2])];
};

describe('theme', () => {
  it('sets modal header role to group for landmark checks', () => {
    expect(theme.components?.ModalHeader?.defaultProps?.role).toBe('group');
  });

  it('uses the locked teal as the primary brand color', () => {
    expect(theme.primaryColor).toBe('teal');
    expect(theme.colors?.teal?.[6]).toBe('#0d9488');
    expect(theme.colors?.teal?.[7]).toBe('#0f766e');
    expect(theme.colors?.amber?.[6]).toBe('#f59e0b');
  });

  it('uses teal-700 for filled primary buttons in light and dark mode', () => {
    expect(theme.primaryShade).toEqual({ light: 7, dark: 7 });
    expect(fullTheme.colors.teal[7]).toBe('#0f766e');
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

  it('pins --mantine-color-error for under-field errors at WCAG AA on page backgrounds', () => {
    const variables = cssVariablesResolver(fullTheme);
    const paper = fullTheme.colors.dark[7];
    const lightError = variables.light['--mantine-color-error']!;
    const darkError = variables.dark['--mantine-color-error']!;
    expect(lightError.toLowerCase()).toBe('#e03131');
    expect(contrastRatio(lightError, fullTheme.white)).toBeGreaterThanOrEqual(4.5);
    expect(darkError?.startsWith('#')).toBe(true);
    expect(contrastRatio(darkError!, paper)).toBeGreaterThanOrEqual(4.5);
    expect(relativeLuminance(darkError!)).toBeLessThanOrEqual(relativeLuminance('#fa5252') + 1e-6);
  });

  it('pins error red ink to #E03131 or darker in light mode and readable ink on dark paper', () => {
    const variables = cssVariablesResolver(fullTheme);
    const paper = fullTheme.colors.dark[7];
    const redScale = fullTheme.colors.red;
    const redDarkWash = flatten(redScale[4], 0.15, paper);
    const redKeys = [
      '--mantine-color-red-light-color',
      '--mantine-color-red-text',
      '--mantine-color-red-outline',
    ] as const;
    for (const key of redKeys) {
      const lightInk = variables.light[key];
      expect(lightInk?.startsWith('#')).toBe(true);
      expect(contrastRatio(lightInk!, fullTheme.white)).toBeGreaterThanOrEqual(4.5);
      expect(contrastRatio(lightInk!, fullTheme.white)).toBeGreaterThanOrEqual(contrastRatio('#e03131', fullTheme.white));
    }

    const darkTextKeys = ['--mantine-color-red-text', '--mantine-color-red-outline'] as const;
    for (const key of darkTextKeys) {
      const darkInk = variables.dark[key];
      expect(darkInk?.startsWith('#')).toBe(true);
      expect(darkInk!.toLowerCase()).not.toBe('#ff8787');
      expect(contrastRatio(darkInk!, paper)).toBeGreaterThanOrEqual(4.5);
      expect(relativeLuminance(darkInk!)).toBeLessThanOrEqual(relativeLuminance('#fa5252') + 1e-6);
    }

    const darkLightColor = variables.dark['--mantine-color-red-light-color'];
    expect(darkLightColor?.startsWith('#')).toBe(true);
    expect(contrastRatio(darkLightColor!, redDarkWash)).toBeGreaterThanOrEqual(4.5);
  });

  it('keeps keyboard focus ring at least 3:1 without lightening filled primary buttons', () => {
    const variables = cssVariablesResolver(fullTheme);
    const paper = fullTheme.colors.dark[7];
    const body = variables.dark['--mantine-color-body'] ?? fullTheme.colors.dark[8];
    const lightRing = variables.light['--stock-focus-ring']!;
    const darkRing = variables.dark['--stock-focus-ring']!;
    expect(lightRing.startsWith('#')).toBe(true);
    expect(darkRing.startsWith('#')).toBe(true);
    expect(contrastRatio(lightRing, fullTheme.white)).toBeGreaterThanOrEqual(3);
    expect(contrastRatio(darkRing, paper)).toBeGreaterThanOrEqual(3);
    expect(contrastRatio(darkRing, token(body, fullTheme.colors))).toBeGreaterThanOrEqual(3);
    expect(contrastRatio(fullTheme.colors.teal[7], fullTheme.white)).toBeGreaterThanOrEqual(4.5);
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
