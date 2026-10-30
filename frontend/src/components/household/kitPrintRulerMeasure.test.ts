import { createCanvas } from '@napi-rs/canvas'
import { readFileSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'
import { describe, expect, it } from 'vitest'
import { rulerContinuousLineMmFromPng, rulerWidthInToleranceMm, rulerWidthMmFromPng } from './kitPrintRulerMeasure'

function syntheticRulerPng(barMm: number, renderScale = 2, fillStyle = '#000'): Buffer {
  const pxPerMm = (72 * renderScale) / 25.4
  const widthPx = Math.ceil(220 * pxPerMm)
  const heightPx = Math.ceil(280 * pxPerMm)
  const barPx = Math.round(barMm * pxPerMm)
  const canvas = createCanvas(widthPx, heightPx)
  const ctx = canvas.getContext('2d')
  ctx.fillStyle = '#fff'
  ctx.fillRect(0, 0, widthPx, heightPx)
  ctx.fillStyle = fillStyle
  const y = Math.floor(heightPx * 0.71)
  const x = Math.round(20 * pxPerMm)
  ctx.fillRect(x, y, barPx, Math.max(2, Math.round(0.45 * pxPerMm)))
  return canvas.toBuffer('image/png')
}

describe('kitPrintRulerMeasure', () => {
  it('detects a 50mm bar in a synthetic footer raster', () => {
    const mm = rulerContinuousLineMmFromPng(syntheticRulerPng(50), 2)
    expect(rulerWidthInToleranceMm(mm)).toBe(true)
  })

  it('does not accept a 40mm bar as the 50mm ruler', () => {
    const mm = rulerContinuousLineMmFromPng(syntheticRulerPng(40), 2)
    expect(rulerWidthInToleranceMm(mm)).toBe(false)
  })

  it('does not treat a light grey bar as the 50mm ruler', () => {
    const mm = rulerWidthMmFromPng(syntheticRulerPng(50, 2, '#ccc'), 2)
    expect(rulerWidthInToleranceMm(mm)).toBe(false)
  })

  it('requires the kit print ruler CSS to use a border line, not a background fill', () => {
    const cssPath = join(dirname(fileURLToPath(import.meta.url)), 'kitPrint.css')
    const css = readFileSync(cssPath, 'utf8')
    const screenRule = css.match(/\.kit-print-ruler\s*\{([^}]+)\}/)?.[1] ?? ''
    expect(screenRule).toMatch(/border-top:\s*2px\s+solid/)
    expect(screenRule).not.toMatch(/background:\s*(?!none)/)
    expect(css).toContain('border-top: 0.45mm solid #000 !important')
    expect(css).toMatch(/@media print[\s\S]*\.kit-print-ruler[\s\S]*background:\s*none !important/)
  })

  it('falls back to column projection when no single scan line spans the bar', () => {
    const png = syntheticRulerPng(50, 2.5)
    const mm = rulerWidthMmFromPng(png, 2.5)
    expect(rulerWidthInToleranceMm(mm)).toBe(true)
  })
})
