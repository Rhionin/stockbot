import { Platform, useWindowDimensions, type ViewStyle } from 'react-native';

export const DESKTOP_MIN_WIDTH = 768;
export const PAGE_COLUMN_MAX_WIDTH = 736;
export const FORM_COLUMN_MAX_WIDTH = 480;
export const DESKTOP_NAV_HEIGHT = 64;
export const DESKTOP_PAGE_PADDING_TOP = 40;

export function isWeb(): boolean {
  return Platform.OS === 'web';
}

export function isDesktopLayout(width: number): boolean {
  return isWeb() && width >= DESKTOP_MIN_WIDTH;
}

export function useDesktopLayout(): boolean {
  const { width } = useWindowDimensions();
  return isDesktopLayout(width);
}

export function useColumn(maxWidth: number = PAGE_COLUMN_MAX_WIDTH): ViewStyle | undefined {
  const desktop = useDesktopLayout();
  return desktop ? { width: '100%', maxWidth, alignSelf: 'center' } : undefined;
}
