import { PNG } from 'pngjs'

const CSS_PX_PER_MM = 96 / 25.4
const RULER_TARGET_MM = 50
const RULER_TOLERANCE_MM = 0.5

function isDark(data: Buffer, width: number, x: number, y: number, threshold = 80): boolean {
  const offset = (y * width + x) * 4
  const lum = (data[offset] + data[offset + 1] + data[offset + 2]) / 3
  return lum < threshold
}

function darkRunWidthAtRow(png: PNG, row: number, threshold = 80): { left: number; right: number }[] {
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

function rulerCandidatesFromBand(png: PNG, rowStart: number, renderScale: number, threshold: number): number[] {
  const { height } = png
  const mmValues: number[] = []
  for (let row = rowStart; row < height; row += 1) {
    for (const run of darkRunWidthAtRow(png, row, threshold)) {
      const px = run.right - run.left + 1
      const mm = px / renderScale / CSS_PX_PER_MM
      if (mm >= 40 && mm <= 60) mmValues.push(mm)
    }
  }
  return mmValues
}

function rulerWidthViaColumnProjection(png: PNG, renderScale: number): number {
  const { width, height, data } = png
  const rowStart = Math.floor(height * 0.75)
  const minPx = Math.floor((RULER_TARGET_MM - RULER_TOLERANCE_MM) * CSS_PX_PER_MM * renderScale)
  const maxPx = Math.ceil((RULER_TARGET_MM + RULER_TOLERANCE_MM) * CSS_PX_PER_MM * renderScale)
  const colHasInk = new Uint8Array(width)
  for (let y = rowStart; y < height; y += 1) {
    for (let x = 0; x < width; x += 1) {
      if (isDark(data, width, x, y, 120)) colHasInk[x] = 1
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
      const mm = span / renderScale / CSS_PX_PER_MM
      const delta = Math.abs(mm - RULER_TARGET_MM)
      if (delta < bestDelta) {
        bestDelta = delta
        bestMm = mm
      }
    }
  }
  return bestMm
}

/** Measure a ~50mm footer ruler bar in a raster image (used by kit print e2e helpers). */
export function rulerWidthMmFromPng(pngBuffer: Buffer, renderScale = 1): number {
  const png = PNG.sync.read(pngBuffer)
  const { height } = png
  const rowStart = Math.floor(height * 0.68)
  let bestMm = 0
  let bestDelta = Number.POSITIVE_INFINITY
  for (const threshold of [64, 80, 96, 112]) {
    for (const mm of rulerCandidatesFromBand(png, rowStart, renderScale, threshold)) {
      const delta = Math.abs(mm - RULER_TARGET_MM)
      if (mm >= RULER_TARGET_MM - RULER_TOLERANCE_MM && mm <= RULER_TARGET_MM + RULER_TOLERANCE_MM && delta < bestDelta) {
        bestDelta = delta
        bestMm = mm
      }
    }
  }
  if (bestMm > 0) return bestMm
  return rulerWidthViaColumnProjection(png, renderScale)
}

export function rulerWidthInToleranceMm(mm: number): boolean {
  return mm >= RULER_TARGET_MM - RULER_TOLERANCE_MM && mm <= RULER_TARGET_MM + RULER_TOLERANCE_MM
}
