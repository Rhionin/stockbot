import { readFileSync } from 'node:fs'
import { join } from 'node:path'
import { describe, expect, it } from 'vitest'
import { contrastRatio, flatten, parseHex, readableLightShade, readableShade, towardContrast } from './contrast'

// Pairs are independent of the theme resolver. #0a0a0a sits on the sRGB linear
// branch; the swap and the 4.5 boundary keep a wrong gamma or a strict >
// from agreeing with these numbers.
const referencePairs: readonly [string, string, number][] = [
  ['#777', '#fff', 4.48],
  ['#fff', '#777', 4.48],
  ['#767676', '#fff', 4.54],
  ['#000000', '#ffffff', 21],
  ['#595959', '#fff', 7],
  ['#0a0a0a', '#000', 1.0607],
]

// WCAG ratio of #767676 on #fff, computed outside this module. A strict >
// misses the shade that sits on this boundary.
const grayBoundary = 4.542224959605253

const shadeCases: readonly ['darker' | 'lighter', readonly string[], string, number, number | undefined, number][] = [
  ['darker', ['#ffffff', '#eeeeee'], '#ffffff', 0, undefined, -1],
  ['lighter', ['#111111', '#000000'], '#000000', 1, undefined, -1],
  ['darker', ['#777777', '#767676'], '#ffffff', 0, grayBoundary, 1],
  ['lighter', ['#767676', '#777777'], '#ffffff', 1, grayBoundary, 0],
  ['lighter', ['#777777', '#767676'], '#ffffff', 9, grayBoundary, 1],
]

// Mantine yellow-9 on its 10% wash. The search has to stop at #b15b00; with
// the bisection removed the same call walks to pure black.
const inkMoves: readonly [string, string, string, string][] = [
  ['#e67700', '#000000', '#fff7e6', '#b15b00'],
  ['#000000', '#ffffff', '#ffffff', '#000000'],
]

const unreachable: readonly [string, string, string][] = [
  ['#ffffff', '#eeeeee', '#ffffff'],
  ['#000000', '#111111', '#000000'],
]

const rejectedHex = ['not-a-color', '#12', '#gggggg', 'x#123456', '#1234567']

describe('contrast', () => {
  it.each(referencePairs)('%s on %s is %s', (foreground, background, ratio) => {
    const precision = ratio < 2 ? 4 : 2
    expect(contrastRatio(foreground, background)).toBeCloseTo(ratio, precision)
  })

  it('flattens a half-black wash over white to gray', () => {
    expect(flatten('#000000', 0.5, '#ffffff')).toBe('#808080')
  })

  it.each(shadeCases)('%s shade %j on %s from %s', (direction, colors, background, start, minimum, want) => {
    const found = direction === 'darker'
      ? readableShade(colors, background, start, minimum)
      : readableLightShade(colors, background, start, minimum)
    expect(found).toBe(want)
  })

  it.each(inkMoves)('moves %s toward %s on %s to %s', (ink, target, background, want) => {
    expect(towardContrast(ink, target, background)).toBe(want)
  })

  it.each(unreachable)('throws when %s cannot reach %s on %s', (ink, target, background) => {
    expect(() => towardContrast(ink, target, background)).toThrow(/no color in this range/)
  })

  it.each(rejectedHex)('rejects %s', (color) => {
    expect(() => parseHex(color)).toThrow(/expected a hex color/)
  })
})

const nothingOnHandCss = readFileSync(join(process.cwd(), 'src/index.css'), 'utf8')

// Ink is body text (4.5). The border is a UI boundary (3). Page colors are
// white and Mantine dark[7], the surfaces the skip line sits on.
const nothingOnHandPairs: readonly [string, string, number][] = [
  ['#92400e', '#fffbeb', 4.5],
  ['#92400e', '#ffffff', 4.5],
  ['#fde68a', '#2c2116', 4.5],
  ['#fde68a', '#1A1B1E', 4.5],
  ['#b45309', '#fffbeb', 3],
  ['#b45309', '#ffffff', 3],
  ['#fbbf24', '#2c2116', 3],
  ['#fbbf24', '#1A1B1E', 3],
]

describe('nothing on hand amber', () => {
  it.each(nothingOnHandPairs)('%s on %s clears %s', (ink, paper, minimum) => {
    expect(nothingOnHandCss).toContain(ink)
    expect(contrastRatio(ink, paper)).toBeGreaterThanOrEqual(minimum)
  })
})
