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

/** Every color/space/radius value Tamagui may use — only exports from shared leaf modules. */
export const TOKEN_PALETTE = {
  colors: new Set<string>([
    accentBorder,
    amberDeep,
    ...amberScale,
    brandTeal,
    brandTealDeep,
    mobileChrome.camera,
    mobileChrome.danger,
    mobileChrome.ink,
    mobileChrome.inkMuted,
    mobileChrome.paper,
    mobileChrome.removeWash,
    mobileChrome.rule,
    mobileChrome.successWash,
    mobileChrome.white,
    nothingOnHandLight.wash,
    nothingOnHandLight.ink,
    nothingOnHandLight.border,
    stockColors.full,
    stockColors.low,
    stockColors.out,
    ...tealScale,
    NOTICE_BANNER_EDGE,
    NOTICE_BANNER_TEXT,
    NOTICE_BANNER_TINT,
  ]),
  space: new Set<number>([
    NOTICE_BANNER_RADIUS,
    NOTICE_SCREEN_PADDING_BOTTOM,
    NOTICE_SCREEN_PADDING_INLINE,
    NOTICE_SCREEN_PADDING_TOP,
  ]),
  radius: new Set<number>([NOTICE_BANNER_RADIUS]),
} as const;

export function assertTokenPaletteValue(kind: keyof typeof TOKEN_PALETTE, value: string | number) {
  const allowed = TOKEN_PALETTE[kind];
  if (!allowed.has(value as never)) {
    throw new Error(`Token ${String(value)} is not in the shared ${kind} palette`);
  }
}
