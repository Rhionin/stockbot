import { createCanvas } from '@napi-rs/canvas'
import { describe, expect, it } from 'vitest'
import { rulerWidthInToleranceMm, rulerWidthMmFromPng } from './kitPrintRulerMeasure'

const CSS_PX_PER_MM = 96 / 25.4

function syntheticRulerPng(barMm: number, scale = 2): Buffer {
  const widthPx = Math.ceil(220 * CSS_PX_PER_MM * scale)
  const heightPx = Math.ceil(280 * CSS_PX_PER_MM * scale)
  const barPx = Math.round(barMm * CSS_PX_PER_MM * scale)
  const canvas = createCanvas(widthPx, heightPx)
  const ctx = canvas.getContext('2d')
  ctx.fillStyle = '#fff'
  ctx.fillRect(0, 0, widthPx, heightPx)
  ctx.fillStyle = '#000'
  const y = heightPx - Math.round(8 * CSS_PX_PER_MM * scale)
  const x = Math.round(20 * CSS_PX_PER_MM * scale)
  ctx.fillRect(x, y, barPx, Math.max(2, Math.round(0.45 * CSS_PX_PER_MM * scale)))
  return canvas.toBuffer('image/png')
}

describe('kitPrintRulerMeasure', () => {
  it('detects a 50mm bar in a synthetic footer raster', () => {
    const mm = rulerWidthMmFromPng(syntheticRulerPng(50), 2)
    expect(rulerWidthInToleranceMm(mm)).toBe(true)
  })

  it('does not accept a 40mm bar as the 50mm ruler', () => {
    const mm = rulerWidthMmFromPng(syntheticRulerPng(40), 2)
    expect(rulerWidthInToleranceMm(mm)).toBe(false)
  })
})
