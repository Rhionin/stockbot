import { describe, expect, it } from 'vitest';
import { contrastRatio } from '@stockbot/shared/contrast';

describe('item detail contrast', () => {
  it('spec 30: sheet ink and placeholder edges meet WCAG 2.2 AA on the bin tokens', () => {
    const lightInk = '#141517';
    const lightMuted = '#495057';
    const lightWell = '#f1f3f5';
    const darkInk = '#f8f9fa';
    const darkMuted = '#dee2e6';
    const darkWell = '#2c2e33';
    const darkLine = '#868e96';
    const lightTint = '#f1f3f5';
    const darkTint = '#373a40';

    expect(contrastRatio(lightInk, lightTint)).toBeGreaterThanOrEqual(16);
    expect(contrastRatio(lightMuted, lightTint)).toBeGreaterThanOrEqual(7);
    expect(contrastRatio(darkInk, darkTint)).toBeGreaterThanOrEqual(10);
    expect(contrastRatio(darkMuted, darkTint)).toBeGreaterThanOrEqual(8);
    expect(contrastRatio(lightMuted, lightWell)).toBeGreaterThanOrEqual(3);
    expect(contrastRatio(darkLine, darkWell)).toBeGreaterThanOrEqual(3);
  });
});
