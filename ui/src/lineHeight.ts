import { Platform } from 'react-native';

// Tamagui turns a bare number into a unitless CSS line-height on web, a
// multiple of the font size. Native wants points, so web gets an explicit px.
export function lineHeightPx(points: number): number | string {
  return Platform.OS === 'web' ? `${points}px` : points;
}
