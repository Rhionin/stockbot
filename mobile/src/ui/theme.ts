import {
  accentBorder,
  amberDeep,
  brandTeal,
  brandTealDeep,
  mobileChrome,
  nothingOnHandLight,
} from '@stockbot/shared/brandTokens';

// Cool label stock and the locked Stockbot teal. Amber from the web theme
// marks food leaving the shelf. Atkinson Hyperlegible is the one family,
// chosen because this screen is read at arm's length.
export const colors = {
  ink: mobileChrome.ink,
  inkMuted: mobileChrome.inkMuted,
  paper: mobileChrome.paper,
  shelf: brandTeal,
  shelfDeep: brandTealDeep,
  successWash: mobileChrome.successWash,
  successEdge: accentBorder,
  using: amberDeep,
  nothingWash: nothingOnHandLight.wash,
  nothingInk: nothingOnHandLight.ink,
  nothingBorder: nothingOnHandLight.border,
  removeWash: mobileChrome.removeWash,
  rule: mobileChrome.rule,
  camera: mobileChrome.camera,
  danger: mobileChrome.danger,
  white: mobileChrome.white,
};

export const fonts = {
  regular: 'AtkinsonHyperlegible_400Regular',
  bold: 'AtkinsonHyperlegible_700Bold',
};
