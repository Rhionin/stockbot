import { createTamagui, createTokens } from 'tamagui';
import {
  accentBorder,
  amberScale,
  brandTeal,
  brandTealDeep,
  nothingOnHandLight,
  stockColors,
  tealScale,
} from '@stockbot/shared/brandTokens';
import {
  NOTICE_BANNER_EDGE,
  NOTICE_BANNER_RADIUS,
  NOTICE_BANNER_TEXT,
  NOTICE_BANNER_TINT,
  NOTICE_SCREEN_PADDING_BOTTOM,
  NOTICE_SCREEN_PADDING_INLINE,
  NOTICE_SCREEN_PADDING_TOP,
} from '@stockbot/shared/noticeBanner';
import { assertTokenPaletteValue, TOKEN_PALETTE } from './tokenPalette';

const color = {
  accentBorder,
  brandTeal,
  brandTealDeep,
  noticeTint: NOTICE_BANNER_TINT,
  noticeText: NOTICE_BANNER_TEXT,
  noticeEdge: NOTICE_BANNER_EDGE,
  stockFull: stockColors.full,
  stockLow: stockColors.low,
  stockOut: stockColors.out,
  nothingWash: nothingOnHandLight.wash,
  nothingInk: nothingOnHandLight.ink,
  nothingBorder: nothingOnHandLight.border,
  ...Object.fromEntries(tealScale.map((value, index) => [`teal${index}`, value])),
  ...Object.fromEntries(amberScale.map((value, index) => [`amber${index}`, value])),
} as const;

for (const value of Object.values(color)) {
  assertTokenPaletteValue('colors', value);
}

const tokens = createTokens({
  color,
  space: {
    noticeRadius: NOTICE_BANNER_RADIUS,
    noticeInline: NOTICE_SCREEN_PADDING_INLINE,
    noticeTop: NOTICE_SCREEN_PADDING_TOP,
    noticeBottom: NOTICE_SCREEN_PADDING_BOTTOM,
  },
  radius: {
    0: 0,
    1: NOTICE_BANNER_RADIUS,
  },
  size: {
    0: 0,
    1: NOTICE_SCREEN_PADDING_INLINE,
  },
  zIndex: {
    0: 0,
    1: 1,
  },
});

for (const value of Object.values(tokens.space)) {
  if (typeof value === 'number') assertTokenPaletteValue('space', value);
}
for (const value of Object.values(tokens.radius)) {
  if (typeof value === 'number' && value !== 0) assertTokenPaletteValue('radius', value);
}

export const themes = {
  light: {
    background: NOTICE_BANNER_TINT,
    color: NOTICE_BANNER_TEXT,
    borderColor: NOTICE_BANNER_EDGE,
    actionBg: brandTeal,
    actionColor: tealScale[0],
    pageBg: tealScale[0],
    actionFocusRing: accentBorder,
  },
  dark: {
    background: tealScale[9],
    color: tealScale[0],
    borderColor: stockColors.out,
    actionBg: brandTealDeep,
    actionColor: tealScale[0],
    pageBg: tealScale[10 - 1],
    actionFocusRing: accentBorder,
  },
} as const;

for (const theme of Object.values(themes)) {
  for (const value of Object.values(theme)) {
    assertTokenPaletteValue('colors', value);
  }
}

export const tamaguiConfig = createTamagui({
  tokens,
  themes,
  media: {
    light: { colorScheme: 'light' },
    dark: { colorScheme: 'dark' },
  },
  shorthands: {
    px: 'paddingHorizontal',
    py: 'paddingVertical',
    bg: 'backgroundColor',
  } as const,
});

export type TamaguiSpikeConfig = typeof tamaguiConfig;

declare module 'tamagui' {
  // eslint-disable-next-line @typescript-eslint/no-empty-object-type
  interface TamaguiCustomConfig extends TamaguiSpikeConfig {}
}

export function paletteColorValues(): readonly string[] {
  return [...TOKEN_PALETTE.colors];
}
