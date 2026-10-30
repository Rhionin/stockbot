import { PNG } from 'pngjs'

const RULER_TARGET_MM = 50
const RULER_TOLERANCE_MM = 0.5
/** Footer band in full-page Letter/A4 rasters (pdftoppm 300dpi). */
const RULER_FOOTER_ROW_START = 0.62
const RULER_FOOTER_ROW_END = 0.88
/** Pixels at or above this average RGB luminance are not ruler ink (rejects light grey lines). */
export const RULER_DARK_LUMINANCE_MAX = 127

function horizontalPxPerMm(renderScale: number): number {
  return (72 * renderScale) / 25.4
}

function mmFromHorizontalPx(px: number, renderScale: number): number {
  return px / horizontalPxPerMm(renderScale)
}

function isDark(data: Buffer, width: number, x: number, y: number, threshold = RULER_DARK_LUMINANCE_MAX): boolean {
  const offset = (y * width + x) * 4
  const lum = (data[offset] + data[offset + 1] + data[offset + 2]) / 3
  return lum <= threshold
}

export function longestRunMmInBand(png: PNG, renderScale: number, rowStartRatio = RULER_FOOTER_ROW_START): number {
  const { height } = png
  const rowStart = Math.floor(height * rowStartRatio)
  const rowEnd = Math.floor(height * RULER_FOOTER_ROW_END)
  let bestMm = 0
  for (let row = rowStart; row < rowEnd; row += 1) {
    for (const run of darkRunWidthAtRow(png, row)) {
      const mm = mmFromHorizontalPx(run.right - run.left + 1, renderScale)
      if (mm > bestMm) bestMm = mm
    }
  }
  return bestMm
}

function darkRunWidthAtRow(png: PNG, row: number, threshold = RULER_DARK_LUMINANCE_MAX): { left: number; right: number }[] {
  const { width, data } = png
  const runs: { left: number; right: number }[] = []
  let left = -1
  for (let x = 0; x < width; x += 1) {
    const dark = isDark(data, width, x, row, threshold)
    if (dark) {
      if (left < 0) left = x
    } else if (left >= 0) {
      runs.push({ left, right: x - 1 })
      left = -1
    }
  }
  if (left >= 0) runs.push({ left, right: width - 1 })
  return runs
}

function rulerWidthViaColumnProjection(png: PNG, renderScale: number): number {
  const { width, height, data } = png
  const rowStart = Math.floor(height * RULER_FOOTER_ROW_START)
  const rowEnd = Math.floor(height * RULER_FOOTER_ROW_END)
  const minPx = Math.floor(mmFromHorizontalPx(RULER_TARGET_MM - RULER_TOLERANCE_MM, renderScale))
  const maxPx = Math.ceil(mmFromHorizontalPx(RULER_TARGET_MM + RULER_TOLERANCE_MM, renderScale))
  const colHasInk = new Uint8Array(width)
  for (let y = rowStart; y < rowEnd; y += 1) {
    for (let x = 0; x < width; x += 1) {
      if (isDark(data, width, x, y)) colHasInk[x] = 1
    }
  }
  let bestMm = 0
  let bestDelta = Number.POSITIVE_INFINITY
  for (let start = 0; start < width; start += 1) {
    if (!colHasInk[start]) continue
    for (let span = minPx; span <= maxPx; span += 1) {
      const end = start + span - 1
      if (end >= width) break
      if (!colHasInk[end]) continue
      let filled = true
      for (let x = start; x <= end; x += 1) {
        if (!colHasInk[x]) {
          filled = false
          break
        }
      }
      if (!filled) continue
      const mm = mmFromHorizontalPx(span, renderScale)
      const delta = Math.abs(mm - RULER_TARGET_MM)
      if (delta < bestDelta) {
        bestDelta = delta
        bestMm = mm
      }
    }
  }
  return bestMm
}

/** Measure a ~50mm footer ruler as a continuous horizontal ink run on one scan line (ignores tick-only artifacts). */
export function rulerContinuousLineMmFromPng(pngBuffer: Buffer, renderScale = 1): number {
  const png = PNG.sync.read(pngBuffer)
  const { height } = png
  const rowStart = Math.floor(height * RULER_FOOTER_ROW_START)
  const rowEnd = Math.floor(height * RULER_FOOTER_ROW_END)
  let bestMm = 0
  let bestDelta = Number.POSITIVE_INFINITY
  for (let row = rowStart; row < rowEnd; row += 1) {
    for (const run of darkRunWidthAtRow(png, row)) {
      const px = run.right - run.left + 1
      const mm = mmFromHorizontalPx(px, renderScale)
      const delta = Math.abs(mm - RULER_TARGET_MM)
      if (mm >= RULER_TARGET_MM - RULER_TOLERANCE_MM && mm <= RULER_TARGET_MM + RULER_TOLERANCE_MM && delta < bestDelta) {
        bestDelta = delta
        bestMm = mm
      }
    }
  }
  return bestMm
}

/** Measure a ~50mm footer ruler bar in a raster image (used by kit print e2e helpers). */
export function rulerWidthMmFromPng(pngBuffer: Buffer, renderScale = 1): number {
  const continuous = rulerContinuousLineMmFromPng(pngBuffer, renderScale)
  if (continuous > 0) return continuous
  const png = PNG.sync.read(pngBuffer)
  return rulerWidthViaColumnProjection(png, renderScale)
}

export function rulerWidthInToleranceMm(mm: number): boolean {
  return mm >= RULER_TARGET_MM - RULER_TOLERANCE_MM && mm <= RULER_TARGET_MM + RULER_TOLERANCE_MM
}
