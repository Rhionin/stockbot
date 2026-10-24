import {
  createTheme,
  Popover,
  Switch,
  v8CssVariablesResolver,
  type CSSVariablesResolver,
  type MantineColorsTuple,
  type MantineTheme,
} from '@mantine/core';
import { flatten, readableLightShade, readableShade, towardContrast } from '@stockbot/shared/contrast';

// Shade 6 is the filled brand teal from the locked mark (#0D9488).
const teal: MantineColorsTuple = [
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
];

// Shade 6 is the filled Low amber (#F59E0B). Darker steps are for ink on light paper.
const amber: MantineColorsTuple = [
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
];

const primaryShadeIndex = (theme: MantineTheme, scheme: 'light' | 'dark') =>
  typeof theme.primaryShade === 'number' ? theme.primaryShade : theme.primaryShade[scheme];

const colorScale = (value: MantineTheme['colors'][string]): readonly string[] | null =>
  Array.isArray(value) && value.length >= 10 && typeof value[0] === 'string' ? value : null;

// A scale step when one clears AA, otherwise the same hue darkened or lightened
// until it does. Yellow, lime, orange, and green run out of steps on a light wash.
const readableInk = (
  name: string,
  scale: readonly string[],
  background: string,
  start: number,
  direction: 'darker' | 'lighter',
): string => {
  const index = direction === 'darker'
    ? readableShade(scale, background, start)
    : readableLightShade(scale, background, start);
  if (index >= 0) return `var(--mantine-color-${name}-${index})`;
  const ink = direction === 'darker' ? scale[scale.length - 1] : scale[0];
  const target = direction === 'darker' ? '#000000' : '#ffffff';
  return towardContrast(ink, target, background);
};

// v8 keeps the transparent light variant: a 10% wash of the filled shade, with
// that same shade as the ink. On white that ink is about 3:1. Step along the
// existing scale until the ink clears WCAG AA, then darken past the scale when
// it still does not. Filled shade 6 stays the brand. Text and outline inks are
// the same treatment on the page background, so outline buttons and c="red"
// clear AA without changing the filled color.
export const cssVariablesResolver: CSSVariablesResolver = (theme) => {
  const resolved = v8CssVariablesResolver(theme);
  const lightShade = primaryShadeIndex(theme, 'light');
  const darkShade = primaryShadeIndex(theme, 'dark');
  const darkPaper = theme.colors.dark[7];

  for (const name of Object.keys(theme.colors)) {
    const scale = colorScale(theme.colors[name]);
    if (!scale) continue;
    const lightWash = flatten(scale[lightShade], 0.1, theme.white);
    resolved.light[`--mantine-color-${name}-light-color`] = readableInk(name, scale, lightWash, lightShade, 'darker');
    const lightOnPaper = readableInk(name, scale, theme.white, lightShade, 'darker');
    resolved.light[`--mantine-color-${name}-text`] = lightOnPaper;
    resolved.light[`--mantine-color-${name}-outline`] = lightOnPaper;

    const darkWash = flatten(scale[Math.max(0, darkShade - 2)], 0.15, darkPaper);
    resolved.dark[`--mantine-color-${name}-light-color`] = readableInk(
      name,
      scale,
      darkWash,
      Math.max(darkShade - 5, 0),
      'lighter',
    );
    resolved.dark[`--mantine-color-${name}-text`] = readableInk(name, scale, darkPaper, Math.max(darkShade - 2, 0), 'lighter');
    resolved.dark[`--mantine-color-${name}-outline`] = readableInk(
      name,
      scale,
      darkPaper,
      Math.max(darkShade - 4, 0),
      'lighter',
    );
  }

  const gray = colorScale(theme.colors.gray);
  const dark = colorScale(theme.colors.dark);
  if (gray) {
    resolved.light['--mantine-color-dimmed'] = readableInk('gray', gray, theme.white, 6, 'darker');
  }
  if (dark) {
    resolved.dark['--mantine-color-dimmed'] = readableInk('dark', dark, darkPaper, 2, 'lighter');
  }

  return resolved;
};

// Mantine 9 changes defaults that would show up in the UI. These settings
// keep the 7.x look and behavior called out in the 7→8 and 8→9 guides:
// 4px radius, transparent light colors, the old switch thumb, and popovers
// that stay open when their target leaves the DOM.
export const theme = createTheme({
  primaryColor: 'teal',
  primaryShade: 6,
  autoContrast: true,
  luminanceThreshold: 0.22,
  colors: {
    teal,
    amber,
  },
  defaultRadius: 'sm',
  components: {
    Switch: Switch.extend({
      defaultProps: {
        withThumbIndicator: false,
      },
    }),
    Popover: Popover.extend({
      defaultProps: {
        hideDetached: false,
      },
    }),
  },
});
