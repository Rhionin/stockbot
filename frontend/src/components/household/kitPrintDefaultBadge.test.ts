import { readFileSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { describe, expect, it } from 'vitest';

const cssPath = join(dirname(fileURLToPath(import.meta.url)), 'kitPrint.css');
const css = readFileSync(cssPath, 'utf8');

const DARK_INK = '#111827';
const MIN_AA_CONTRAST_ON_WHITE = 4.5;

function relativeLuminance(hex: string): number {
  const value = hex.replace('#', '');
  const channels = [0, 2, 4].map((offset) => {
    const byte = Number.parseInt(value.slice(offset, offset + 2), 16) / 255;
    return byte <= 0.03928 ? byte / 12.92 : ((byte + 0.055) / 1.055) ** 2.4;
  });
  return 0.2126 * channels[0] + 0.7152 * channels[1] + 0.0722 * channels[2];
}

function contrastRatio(foreground: string, background: string): number {
  const l1 = relativeLuminance(foreground);
  const l2 = relativeLuminance(background);
  const lighter = Math.max(l1, l2);
  const darker = Math.min(l1, l2);
  return (lighter + 0.05) / (darker + 0.05);
}

function extractPrintMediaBlock(sheet: string): string {
  const marker = '@media print';
  const mediaStart = sheet.indexOf(marker);
  if (mediaStart === -1) return '';
  const openBrace = sheet.indexOf('{', mediaStart);
  if (openBrace === -1) return '';
  let depth = 0;
  for (let index = openBrace; index < sheet.length; index += 1) {
    if (sheet[index] === '{') depth += 1;
    if (sheet[index] === '}') {
      depth -= 1;
      if (depth === 0) return sheet.slice(openBrace + 1, index);
    }
  }
  return '';
}

function selectorBlocks(block: string, selector: string): string[] {
  const escaped = selector.replace(/\./g, '\\.');
  const re = new RegExp(`${escaped}[^{]*\\{([^}]+)\\}`, 'g');
  return [...block.matchAll(re)].map((match) => match[1]);
}

function indexOfSelectorRule(sheet: string, selector: string): number {
  return sheet.indexOf(`${selector} {`);
}

describe('kit print DEFAULT badge (P1, backgrounds-off fallback)', () => {
  const printBlock = extractPrintMediaBlock(css);

  it('defines print-only badge ink on white with border (not first screen rule)', () => {
    expect(printBlock.length).toBeGreaterThan(100);
    const badgeBlocks = selectorBlocks(printBlock, '.kit-wall-default-badge');
    const badgeInBlocks = selectorBlocks(printBlock, '.kit-wall-default-badge-in');
    expect(badgeBlocks.length).toBeGreaterThan(0);
    expect(badgeInBlocks.length).toBeGreaterThan(0);

    const combined = [...badgeBlocks, ...badgeInBlocks].join('\n');
    expect(combined).toMatch(new RegExp(`color:\\s*${DARK_INK}`, 'i'));
    expect(combined).toMatch(/background:\s*#fff\s*!important/i);
    expect(combined).toMatch(new RegExp(`border:\\s*1px solid ${DARK_INK}`, 'i'));
    expect(combined).toMatch(/print-color-adjust:\s*exact/);
    expect(contrastRatio(DARK_INK, '#ffffff')).toBeGreaterThanOrEqual(MIN_AA_CONTRAST_ON_WHITE);

    const screenIdx = indexOfSelectorRule(css, '.kit-wall-default-badge');
    const printIdx = css.indexOf('.kit-wall-default-badge', css.indexOf('@media print'));
    expect(screenIdx).toBeGreaterThanOrEqual(0);
    expect(printIdx).toBeGreaterThan(screenIdx);
  });

  it('requests exact color for amber Stock out band in print media', () => {
    expect(
      printBlock.replace(/\s+/g, ' '),
    ).toMatch(
      /\.kit-wall-panel-out\s*,[\s\S]*print-color-adjust:\s*exact/,
    );
  });
});
