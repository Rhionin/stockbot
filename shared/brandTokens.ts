// Brand, stock, and nothing-on-hand colors that already match on web and phone.
// Hex is lowercase. This module stays a leaf: no UI frameworks.

export type ColorScale10 = readonly [
  string,
  string,
  string,
  string,
  string,
  string,
  string,
  string,
  string,
  string,
];

// Shade 6 is the filled brand teal. Shade 8 is the deep confirm fill.
export const tealScale: ColorScale10 = [
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

// Shade 6 is the filled Low amber. Shade 9 is deep amber ink (phone stock-out accent).
export const amberScale: ColorScale10 = [
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

export const brandTeal = tealScale[6];
export const brandTealDeep = tealScale[8];
export const amberDeep = amberScale[9];

export const stockColors = {
  full: brandTeal,
  low: amberScale[6],
  out: '#ef4444',
} as const;

export const nothingOnHandLight = {
  wash: amberScale[0],
  ink: amberDeep,
  border: amberScale[8],
} as const;

/** Shared success / accent edge on web (`--accent-border`) and phone (`successEdge`). */
export const accentBorder = 'rgba(13, 148, 136, 0.5)';

/** Phone shell ink and chrome (queue rows, forms). Web uses Mantine neutrals instead. */
export const mobileChrome = {
  ink: '#14242C',
  inkMuted: '#4C616B',
  paper: '#F3F6F8',
  removeWash: '#FECDD3',
  rule: '#D3DDE3',
  camera: '#0C1C22',
  danger: '#9F1239',
  white: '#FFFFFF',
  successWash: 'rgba(13, 148, 136, 0.1)',
} as const;
