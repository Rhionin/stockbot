import { describe, expect, it } from 'vitest';
import { themes, paletteColorValues } from './tamagui.config';
import { TOKEN_PALETTE } from './tokenPalette';

describe('tamagui theme parity', () => {
  it('keeps light and dark theme colors inside the shared token palette', () => {
    const palette = new Set(paletteColorValues());
    for (const [name, theme] of Object.entries(themes)) {
      for (const [key, value] of Object.entries(theme)) {
        expect(palette.has(value), `${name}.${key}=${value}`).toBe(true);
        expect(TOKEN_PALETTE.colors.has(value), `${name}.${key}`).toBe(true);
      }
    }
  });

  it('derives both themes from the same token module exports', () => {
    const lightKeys = Object.keys(themes.light).sort();
    const darkKeys = Object.keys(themes.dark).sort();
    expect(darkKeys).toEqual(lightKeys);
  });
});
