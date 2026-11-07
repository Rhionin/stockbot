import {
  accentBorder,
  amberDeep,
  brandTeal,
  brandTealDeep,
  nothingOnHandLight,
} from '@stockbot/shared/brandTokens';

// Cool label stock and the locked Stockbot teal. Amber from the web theme
// marks food leaving the shelf. Atkinson Hyperlegible is the one family,
// chosen because this screen is read at arm's length.
export const colors = {
  ink: '#14242C',
  inkMuted: '#4C616B',
  paper: '#F3F6F8',
  shelf: brandTeal,
  shelfDeep: brandTealDeep,
  // Mantine teal-light wash and the brand accent edge, used for a success banner.
  successWash: 'rgba(13, 148, 136, 0.1)',
  successEdge: accentBorder,
  using: amberDeep,
  nothingWash: nothingOnHandLight.wash,
  nothingInk: nothingOnHandLight.ink,
  nothingBorder: nothingOnHandLight.border,
  removeWash: '#FECDD3',
  rule: '#D3DDE3',
  camera: '#0C1C22',
  danger: '#9F1239',
  white: '#FFFFFF',
};

export const fonts = {
  regular: 'AtkinsonHyperlegible_400Regular',
  bold: 'AtkinsonHyperlegible_700Bold',
};
