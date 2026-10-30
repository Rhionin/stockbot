import { describe, expect, it } from 'vitest';
import { code128Bars, code128SetBPattern } from './code128';

/** Code 128 symbol patterns for values 0–106, transcribed from ISO/IEC 15417 for golden tests. */
const CODE128_SPEC_PATTERNS = [
  '212222', '222122', '222221', '121223', '121322', '131222', '122213', '122312', '132212', '221213',
  '221312', '231212', '112232', '122132', '122231', '113222', '123122', '123221', '223211', '221132',
  '221231', '213212', '223112', '312131', '311222', '321122', '321221', '312212', '322112', '322211',
  '212123', '212321', '232121', '111323', '131123', '131321', '112313', '132113', '132311', '211313',
  '231113', '231311', '112133', '112331', '132131', '113123', '113321', '133121', '313121', '211331',
  '231131', '213113', '213311', '213131', '311123', '311321', '331121', '312113', '312311', '332111',
  '314111', '221411', '431111', '111224', '111422', '121124', '121421', '141122', '141221', '112214',
  '112412', '122114', '122411', '142112', '142211', '241211', '221114', '413111', '241112', '134111',
  '111242', '121142', '121241', '114212', '124112', '124211', '411212', '421112', '421211', '212141',
  '214121', '412121', '111143', '111341', '131141', '114113', '114311', '411113', '411311', '113141',
  '114131', '311141', '411131', '211412', '211214', '211232', '2331112',
] as const;

function expandPattern(pattern: string): boolean[] {
  const bars: boolean[] = [];
  let on = true;
  for (const digit of pattern) {
    const width = Number(digit);
    for (let i = 0; i < width; i += 1) bars.push(on);
    on = !on;
  }
  return bars;
}

function patternRunLengths(bars: boolean[]): number[] {
  const runs: number[] = [];
  let index = 0;
  while (index < bars.length) {
    const current = bars[index];
    let width = 0;
    while (index < bars.length && bars[index] === current) {
      width += 1;
      index += 1;
    }
    runs.push(width);
  }
  return runs;
}

describe('code128 set B patterns', () => {
  it('matches the full ISO pattern table for every symbol value 0–106', () => {
    expect(CODE128_SPEC_PATTERNS).toHaveLength(107);
    for (let index = 0; index < CODE128_SPEC_PATTERNS.length; index += 1) {
      expect(code128SetBPattern(index)).toBe(CODE128_SPEC_PATTERNS[index]);
    }
  });

  it('expands Start B and Stop patterns into alternating bar runs', () => {
    expect(patternRunLengths(expandPattern('211214'))).toEqual([2, 1, 1, 2, 1, 4]);
    expect(patternRunLengths(expandPattern('2331112'))).toEqual([2, 3, 3, 1, 1, 1, 2]);
  });

  it('encodes "A" with Start B, symbol, checksum, and Stop bar runs from the table', () => {
    const bars = code128Bars('A');
    const startRuns = patternRunLengths(expandPattern(code128SetBPattern(104)));
    expect(patternRunLengths(bars.slice(0, startRuns.reduce((sum, w) => sum + w, 0)))).toEqual(startRuns);

    const startWidth = startRuns.reduce((sum, width) => sum + width, 0);
    const charRuns = patternRunLengths(expandPattern(code128SetBPattern(33)));
    const charWidth = charRuns.reduce((sum, width) => sum + width, 0);
    expect(patternRunLengths(bars.slice(startWidth, startWidth + charWidth))).toEqual(charRuns);

    const checksumIndex = (104 + 33) % 103;
    const checksumRuns = patternRunLengths(expandPattern(code128SetBPattern(checksumIndex)));
    const checksumWidth = checksumRuns.reduce((sum, width) => sum + width, 0);
    const afterChar = startWidth + charWidth;
    expect(patternRunLengths(bars.slice(afterChar, afterChar + checksumWidth))).toEqual(checksumRuns);

    const stopRuns = patternRunLengths(expandPattern(code128SetBPattern(106)));
    expect(patternRunLengths(bars.slice(-stopRuns.reduce((sum, w) => sum + w, 0)))).toEqual(stopRuns);
  });

  it('fails golden checks when two table entries are swapped', () => {
    const bars = code128Bars('A');
    const startRuns = patternRunLengths(expandPattern(code128SetBPattern(104)));
    const startWidth = startRuns.reduce((sum, width) => sum + width, 0);
    const charIndex = 33;
    const swappedIndex = 52;
    const charRuns = patternRunLengths(expandPattern(CODE128_SPEC_PATTERNS[charIndex]));
    const charWidth = charRuns.reduce((sum, width) => sum + width, 0);
    const swappedBars = expandPattern(CODE128_SPEC_PATTERNS[swappedIndex]);
    expect(bars.slice(startWidth, startWidth + charWidth)).not.toEqual(swappedBars.slice(0, charWidth));
    expect(CODE128_SPEC_PATTERNS[charIndex]).not.toBe(CODE128_SPEC_PATTERNS[swappedIndex]);
    expect(code128SetBPattern(charIndex)).toBe(CODE128_SPEC_PATTERNS[charIndex]);
  });
});
