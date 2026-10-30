import { describe, expect, it } from 'vitest';
import { code128Bars, code128SetBPattern } from './code128';

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
  it('matches spec table entries for Start B, Stop, and A', () => {
    expect(code128SetBPattern(104)).toBe('211214');
    expect(code128SetBPattern(106)).toBe('2331112');
    expect(code128SetBPattern(33)).toBe('111323');
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
    const swappedChar = expandPattern('211331');
    expect(bars.slice(11, 11 + swappedChar.length)).not.toEqual(swappedChar);
  });
});
