import { readFileSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'
import { createCanvas } from '@napi-rs/canvas'
import {
  BarcodeFormat,
  BinaryBitmap,
  DecodeHintType,
  HybridBinarizer,
  MultiFormatReader,
  NotFoundException,
  RGBLuminanceSource,
} from '@zxing/library'
import { getDocument, GlobalWorkerOptions } from 'pdfjs-dist/legacy/build/pdf.mjs'
import { PNG } from 'pngjs'
import { rulerWidthMmFromPng } from '../src/components/household/kitPrintRulerMeasure.ts'

const pdfjsDir = join(dirname(fileURLToPath(import.meta.url)), '../node_modules/pdfjs-dist/legacy/build')
GlobalWorkerOptions.workerSrc = join(pdfjsDir, 'pdf.worker.mjs')

async function renderPdfPageToPng(pdf: Buffer, scale = 3.5): Promise<Buffer> {
  const doc = await getDocument({
    data: new Uint8Array(pdf),
    disableWorker: true,
    useSystemFonts: true,
  }).promise
  const page = await doc.getPage(1)
  const viewport = page.getViewport({ scale })
  const canvas = createCanvas(Math.ceil(viewport.width), Math.ceil(viewport.height))
  const context = canvas.getContext('2d')
  await page.render({
    canvasContext: context as unknown as CanvasRenderingContext2D,
    viewport,
  }).promise
  return canvas.toBuffer('image/png')
}

function luminanceFromPng(png: PNG): Uint8ClampedArray {
  const { width, height, data } = png
  const luminances = new Uint8ClampedArray(width * height)
  for (let y = 0; y < height; y += 1) {
    for (let x = 0; x < width; x += 1) {
      const offset = (y * width + x) * 4
      const r = data[offset]
      const g = data[offset + 1]
      const b = data[offset + 2]
      luminances[y * width + x] = Math.floor((r + g + g + b) / 4)
    }
  }
  return luminances
}

function decodeRegion(
  reader: MultiFormatReader,
  png: PNG,
  left: number,
  top: number,
  width: number,
  height: number,
): string | null {
  const regionWidth = Math.max(1, Math.min(width, png.width - left))
  const regionHeight = Math.max(1, Math.min(height, png.height - top))
  const full = luminanceFromPng(png)
  const slice = new Uint8ClampedArray(regionWidth * regionHeight)
  for (let y = 0; y < regionHeight; y += 1) {
    const srcStart = (top + y) * png.width + left
    slice.set(full.subarray(srcStart, srcStart + regionWidth), y * regionWidth)
  }
  try {
    const source = new RGBLuminanceSource(slice, regionWidth, regionHeight)
    const result = reader.decode(new BinaryBitmap(new HybridBinarizer(source)))
    return result.getText()
  } catch (error) {
    if (error instanceof NotFoundException) return null
    return null
  } finally {
    reader.reset()
  }
}

export async function decodeCode128ValuesFromPdf(pdf: Buffer): Promise<string[]> {
  const pngBuffer = await renderPdfPageToPng(pdf, 2.5)
  const png = PNG.sync.read(pngBuffer)
  const reader = new MultiFormatReader()
  const hints = new Map<DecodeHintType, unknown>()
  hints.set(DecodeHintType.POSSIBLE_FORMATS, [BarcodeFormat.CODE_128])
  hints.set(DecodeHintType.TRY_HARDER, true)
  reader.setHints(hints)

  const found = new Set<string>()
  const stripHeight = Math.max(16, Math.floor(png.height / 48))
  for (let top = 0; top < png.height; top += Math.max(8, Math.floor(stripHeight / 2))) {
    for (const height of [stripHeight, stripHeight * 2, stripHeight * 3]) {
      if (top + height > png.height) continue
      const value = decodeRegion(reader, png, 0, top, png.width, height)
      if (value) found.add(value)
    }
  }
  const quarters = [0, Math.floor(png.width * 0.25), Math.floor(png.width * 0.5), Math.floor(png.width * 0.75)]
  for (const left of quarters) {
    const value = decodeRegion(reader, png, left, 0, Math.floor(png.width * 0.75), png.height)
    if (value) found.add(value)
  }
  return [...found]
}

export async function assertPdfDecodesCode128(pdf: Buffer, expected: string | string[]) {
  const needles = Array.isArray(expected) ? expected : [expected]
  const decoded = await decodeCode128ValuesFromPdf(pdf)
  for (const needle of needles) {
    const match = decoded.some((value) => value === needle || value.includes(needle))
    if (!match) {
      throw new Error(`PDF barcode decode missing ${needle}; got ${decoded.join(', ')}`)
    }
  }
}

export async function rulerWidthMmFromPdf(pdf: Buffer, renderScale = 4): Promise<number> {
  const pngBuffer = await renderPdfPageToPng(pdf, renderScale)
  return rulerWidthMmFromPng(pngBuffer, renderScale)
}

export async function pdfVisibleText(pdf: Buffer): Promise<string> {
  const doc = await getDocument({
    data: new Uint8Array(pdf),
    disableWorker: true,
    useSystemFonts: true,
  }).promise
  const page = await doc.getPage(1)
  const content = await page.getTextContent()
  return content.items
    .map((item) => ('str' in item ? item.str : ''))
    .join(' ')
    .replace(/\s+/g, ' ')
    .trim()
}

export async function pdfContainsVisibleText(pdf: Buffer, needle: string): Promise<boolean> {
  const text = await pdfVisibleText(pdf)
  return text.includes(needle)
}

/** Smoke-test helper for local mutant verification. */
export function readCssMutantFlag(): boolean {
  try {
    const css = readFileSync(new URL('../src/components/household/kitPrint.css', import.meta.url), 'utf8')
    return /\.wall-sheet-symcode-print\s*\{[^}]*display:\s*none/.test(css)
  } catch {
    return false
  }
}
