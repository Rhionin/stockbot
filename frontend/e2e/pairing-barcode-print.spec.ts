import { expect, test } from '@playwright/test'
import {
  PAIRING,
  PAIRING_ON_SCREEN_TOO_SMALL_HINT,
  countPdfPages,
  install,
  resetStage,
  signInAndOpenHouseholdScannerPairing,
  stage,
  stubWindowPrint,
} from './householdStageFixtures'
import { pairingScannerScreenFallback, pairingDialogTitle } from '../src/components/household/kitCopy456'
import {
  assertPdfDecodesCode128,
  assertPdfRulerContinuousLineMm,
  pdfContainsVisibleText,
  pdfVisibleText,
} from './pdfCode128'
import { PAIRING_CODE128_MIN_MODULE_DEVICE_PX } from '../src/components/household/pairingScreenCode128'

const SCREEN_FALLBACK_COPY = pairingScannerScreenFallback()
const PDF_PRINT_OPTIONS = { preferCSSPageSize: true, printBackground: false, margin: { top: '0', right: '0', bottom: '0', left: '0' } } as const
const PAIRING_DIALOG_TITLE = pairingDialogTitle('scanner')
const MOBILE_TOUCH_WIDTHS = [320, 390] as const
const MIN_TOUCH_TARGET_CSS_PX = 44
const RULER_CAPTION_SNIPPET = '50 mm (about 2 inches)'

const MOBILE_WIDTHS = [320, 360, 390, 412] as const
const MATRIX_WIDTHS = [...MOBILE_WIDTHS, 1280] as const
const DPRS = [1, 1.5, 2, 2.625, 3] as const
const PRINT_LAYOUT_VIEWPORT_WIDTH = 720

const CSS_PX_PER_MM = 96 / 25.4

function assertPrintBarcodeMetrics(box: { width: number; height: number }, totalModules: number) {
  const moduleMm = (box.width / totalModules) / CSS_PX_PER_MM
  const heightMm = box.height / CSS_PX_PER_MM
  expect(moduleMm).toBeGreaterThanOrEqual(0.49)
  expect(moduleMm).toBeLessThanOrEqual(0.51)
  expect(heightMm).toBeGreaterThanOrEqual(17.99)
  expect(heightMm).toBeLessThanOrEqual(22.01)
}

async function assertNoDocumentHorizontalOverflow(page: import('@playwright/test').Page) {
  await expect(page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).resolves.toBe(true)
}

async function assertRenderedQuietModules(svg: import('@playwright/test').Locator, expectedQuiet = 10) {
  await expect(svg.evaluate((el, quietModules) => {
    const barModules = Number(el.getAttribute('data-modules'))
    const quietAttr = Number(el.getAttribute('data-quiet-modules'))
    const quiet = Number.isFinite(quietAttr) ? quietAttr : quietModules
    const totalModules = barModules + 2 * quiet
    const rects = [...el.querySelectorAll('rect')]
    if (rects.length === 0) return { ok: false, reason: 'no rects' }
    const firstX = Math.min(...rects.map((rect) => Number(rect.getAttribute('x'))))
    const lastEnd = Math.max(...rects.map((rect) => Number(rect.getAttribute('x')) + Number(rect.getAttribute('width'))))
    const leftQuietModules = firstX
    const rightQuietModules = totalModules - lastEnd
    const rect = el.getBoundingClientRect()
    const moduleCssPx = rect.width / totalModules
    const leftQuietPx = leftQuietModules * moduleCssPx
    const rightQuietPx = rightQuietModules * moduleCssPx
    return {
      ok: leftQuietModules >= quietModules && rightQuietModules >= quietModules,
      leftQuietModules,
      rightQuietModules,
      leftQuietPx,
      rightQuietPx,
      moduleCssPx,
    }
  }, expectedQuiet)).resolves.toMatchObject({
    ok: true,
    leftQuietModules: expectedQuiet,
    rightQuietModules: expectedQuiet,
  })
}

async function assertPrintRuler50mm(printRoot: import('@playwright/test').Locator) {
  const rulerMm = await printRoot.locator('.kit-print-ruler').evaluate(
    (el) => el.getBoundingClientRect().width / (96 / 25.4),
  )
  expect(rulerMm).toBeGreaterThanOrEqual(49.5)
  expect(rulerMm).toBeLessThanOrEqual(50.5)
}

async function assertTouchTargetSize(locator: import('@playwright/test').Locator) {
  const box = await locator.boundingBox()
  expect(box).not.toBeNull()
  expect(Math.ceil(box!.width)).toBeGreaterThanOrEqual(MIN_TOUCH_TARGET_CSS_PX)
  expect(Math.ceil(box!.height)).toBeGreaterThanOrEqual(MIN_TOUCH_TARGET_CSS_PX)
}

async function assertOnScreenPairingBarcode(page: import('@playwright/test').Page, viewportWidth: number, dpr: number) {
  const dialog = page.getByRole('dialog', { name: 'Connect a scanner' })
  const isNarrowPhone = viewportWidth <= 399
  const openModalRoot = page.locator('.mantine-Modal-root:visible')
  if (isNarrowPhone) {
    await expect(page.getByRole('dialog', { name: 'Connect a scanner' })).toBeVisible()
    await expect(page.getByRole('dialog', { name: 'Connect a scanner' })).toHaveAttribute('data-full-screen', /.*/)
  } else if ((MOBILE_WIDTHS as readonly number[]).includes(viewportWidth)) {
    await expect(page.getByRole('dialog', { name: 'Connect a scanner' })).toBeVisible()
    await expect(page.getByRole('dialog', { name: 'Connect a scanner' })).not.toHaveAttribute('data-full-screen')
  }
  const svg = dialog.locator('.pairing-screen-only svg.pairing-code128')
  await expect(svg).toBeVisible()
  const frame = dialog.locator('.pairing-code128-frame')
  const metrics = await svg.evaluate((el) => {
    const quiet = Number(el.getAttribute('data-quiet-modules'))
    const barModules = Number(el.getAttribute('data-modules'))
    const totalModules = barModules + 2 * quiet
    const rect = el.getBoundingClientRect()
    const dprValue = window.devicePixelRatio || 1
    const devicePxPerModule = (rect.width * dprValue) / totalModules
    const moduleDevicePx = Number(el.getAttribute('data-module-device-px'))
    return {
      devicePxPerModule,
      moduleDevicePx,
      dpr: dprValue,
      totalModules,
      quiet,
      width: rect.width,
    }
  })
  expect(metrics.quiet).toBe(10)
  expect(Number.isInteger(metrics.moduleDevicePx)).toBe(true)
  expect(Math.abs(metrics.devicePxPerModule - metrics.moduleDevicePx)).toBeLessThan(0.05)
  expect(Math.abs(metrics.devicePxPerModule - Math.round(metrics.devicePxPerModule))).toBeLessThan(0.01)

  const isMobileWidth = (MOBILE_WIDTHS as readonly number[]).includes(viewportWidth)
  const isDpr1Phone = dpr === 1 && isMobileWidth
  const requiresTwoDevicePx = isMobileWidth && dpr !== 1

  if (requiresTwoDevicePx) {
    expect(metrics.moduleDevicePx).toBeGreaterThanOrEqual(PAIRING_CODE128_MIN_MODULE_DEVICE_PX)
    await expect(dialog.getByText(PAIRING_ON_SCREEN_TOO_SMALL_HINT)).toHaveCount(0)
  } else if (metrics.moduleDevicePx < PAIRING_CODE128_MIN_MODULE_DEVICE_PX) {
    await expect(dialog.getByText(PAIRING_ON_SCREEN_TOO_SMALL_HINT)).toBeVisible()
    if (isDpr1Phone) {
      expect(metrics.moduleDevicePx).toBe(1)
      expect(metrics.devicePxPerModule).toBeGreaterThanOrEqual(0.99)
      expect(metrics.devicePxPerModule).toBeLessThanOrEqual(1.01)
    } else {
      throw new Error(`Expected >= ${PAIRING_CODE128_MIN_MODULE_DEVICE_PX} device px at ${viewportWidth}px DPR ${dpr}`)
    }
  } else {
    await expect(dialog.getByText(PAIRING_ON_SCREEN_TOO_SMALL_HINT)).toHaveCount(0)
    expect(metrics.moduleDevicePx).toBeGreaterThanOrEqual(PAIRING_CODE128_MIN_MODULE_DEVICE_PX)
  }

  if (viewportWidth >= 1280 && dpr === 1) {
    expect(metrics.moduleDevicePx).toBeGreaterThanOrEqual(PAIRING_CODE128_MIN_MODULE_DEVICE_PX)
  }

  await assertRenderedQuietModules(svg)

  await expect(svg.evaluate((el) => {
    const attrWidth = Number(el.getAttribute('width'))
    const rendered = el.getBoundingClientRect().width
    return Math.abs(rendered - attrWidth) < 1.5
  })).resolves.toBe(true)

  const frameScroll = await frame.evaluate((el) => ({
    scrollWidth: el.scrollWidth,
    clientWidth: el.clientWidth,
    scrollLeft: el.scrollLeft,
  }))
  expect(frameScroll.scrollWidth).toBeLessThanOrEqual(frameScroll.clientWidth + 1)
  expect(frameScroll.scrollLeft).toBe(0)

  await expect(page.evaluate(() => document.documentElement.scrollHeight <= window.innerHeight + 1)).resolves.toBe(true)
}

async function signInOpenWallSheet(page: import('@playwright/test').Page) {
  await page.goto('/')
  await page.getByLabel('Email').fill('ada@example.com')
  await page.getByRole('textbox', { name: 'Password' }).fill('correct-horse-battery')
  await page.getByRole('button', { name: 'Sign in' }).click()
  await page.getByRole('button', { name: 'Menu' }).click()
  await page.getByRole('menuitem', { name: 'Devices' }).click()
  await page.getByRole('button', { name: 'Print wall barcodes' }).click()
  return page.getByRole('dialog', { name: 'Wall barcodes' })
}

for (const width of MATRIX_WIDTHS) {
  for (const dpr of DPRS) {
    test(`on-screen pairing Code128 module px at ${width}px DPR ${dpr}`, async ({ browser }) => {
      resetStage()
      const context = await browser.newContext({
        viewport: { width, height: 700 },
        deviceScaleFactor: dpr,
      })
      const page = await context.newPage()
      await install(page)
      await signInAndOpenHouseholdScannerPairing(page)
      await expect(page.getByRole('dialog', { name: 'Connect a scanner' })).toBeVisible()
      await assertOnScreenPairingBarcode(page, width, dpr)
      await context.close()
    })
  }
}

for (const width of MOBILE_TOUCH_WIDTHS) {
  test(`pairing dialog touch targets at ${width}px wide`, async ({ page }) => {
    resetStage()
    await page.setViewportSize({ width, height: 844 })
    await install(page)
    await signInAndOpenHouseholdScannerPairing(page)
    const dialog = page.getByRole('dialog', { name: 'Connect a scanner' })
    await expect(dialog).toBeVisible()
    await assertTouchTargetSize(dialog.locator('.pairing-dialog-close'))
    await assertTouchTargetSize(dialog.getByRole('button', { name: 'Print setup & wall sheet' }))
    await assertTouchTargetSize(dialog.locator('.pairing-dialog-actions').getByRole('button', { name: 'Close' }))
  })
}

test('setup sheet prints one Letter/A4 page through the real Print button', async ({ page }) => {
  test.setTimeout(90_000)
  resetStage()
  stage.householdName = 'Test Household'
  await page.setViewportSize({ width: 1280, height: 900 })
  await install(page)
  await signInAndOpenHouseholdScannerPairing(page)
  const pairingDialog = page.getByRole('dialog', { name: 'Connect a scanner' })
  await expect(pairingDialog).toBeVisible()
  await expect(pairingDialog.locator('.pairing-screen-only').first()).not.toContainText(SCREEN_FALLBACK_COPY)
  await expect(pairingDialog.locator('.setup-kit-print-root')).toContainText(SCREEN_FALLBACK_COPY)
  await expect(pairingDialog.locator('.pairing-screen-only svg.pairing-code128')).toBeVisible()
  await stubWindowPrint(page)
  await page.getByRole('button', { name: 'Print setup & wall sheet' }).click()
  const printRoot = page.locator('.setup-kit-print-root[data-printing="true"]')
  await expect(printRoot).toBeVisible()
  await expect(printRoot).not.toHaveAttribute('hidden')
  await expect(page.locator('.setup-kit-print-root svg[data-barcode]').first()).toBeAttached()
  await page.emulateMedia({ media: 'print' })
  await page.setViewportSize({ width: PRINT_LAYOUT_VIEWPORT_WIDTH, height: 900 })
  await assertNoDocumentHorizontalOverflow(page)
  const rootHeight = await printRoot.evaluate((el) => el.getBoundingClientRect().height)
  expect(rootHeight).toBeGreaterThan(100)
  const hintBottom = await printRoot.locator('.kit-print-footer-instruction').evaluate((el) => el.getBoundingClientRect().bottom)
  const rulerTop = await printRoot.locator('.kit-print-ruler').evaluate((el) => el.getBoundingClientRect().top)
  expect(rulerTop).toBeGreaterThanOrEqual(hintBottom - 0.5)
  await assertPrintRuler50mm(printRoot)
  const sheetText = await printRoot.evaluate((el) => el.textContent ?? '')
  expect(sheetText).toMatch(/STOCK-OUT/)
  expect(sheetText).toMatch(/STOCK-IN/)
  expect(sheetText).toMatch(new RegExp(PAIRING.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')))
  expect(sheetText).toMatch(/Print at Actual size/)
  for (const svg of await page.locator('.setup-kit-print-root svg[data-barcode]').all()) {
    const box = await svg.boundingBox()
    const barModules = Number(await svg.getAttribute('data-modules'))
    const totalModules = Number(await svg.getAttribute('data-total-modules') ?? barModules)
    expect(box).not.toBeNull()
    assertPrintBarcodeMetrics(box!, totalModules)
  }
  for (const format of ['Letter', 'A4'] as const) {
    const pdf = Buffer.from(await page.pdf({ format, ...PDF_PRINT_OPTIONS }))
    expect(pdf.length).toBeGreaterThan(5000)
    expect(countPdfPages(pdf)).toBe(1)
    await assertPdfDecodesCode128(pdf, [PAIRING, 'STOCK-OUT', 'STOCK-IN'])
    expect(await pdfContainsVisibleText(pdf, 'Print at Actual size')).toBe(true)
    await assertPdfRulerContinuousLineMm(pdf)
    expect(await pdfVisibleText(pdf)).not.toMatch(/\bMenu\b/)
    await page.emulateMedia({ media: 'screen' })
    await stubWindowPrint(page)
    await page.getByRole('button', { name: 'Print setup & wall sheet' }).click()
    await expect(page.locator('.setup-kit-print-root[data-printing="true"]')).toBeVisible()
    await page.emulateMedia({ media: 'print' })
    await page.setViewportSize({ width: PRINT_LAYOUT_VIEWPORT_WIDTH, height: 900 })
    await assertNoDocumentHorizontalOverflow(page)
  }
  await page.emulateMedia({ media: 'screen' })
})

test('setup sheet print layout fits at 390px without horizontal overflow', async ({ page }) => {
  resetStage()
  await page.setViewportSize({ width: 390, height: 844 })
  await install(page)
  await signInAndOpenHouseholdScannerPairing(page)
  await stubWindowPrint(page)
  await page.getByRole('button', { name: 'Print setup & wall sheet' }).click()
  await expect(page.locator('.setup-kit-print-root[data-printing="true"]')).toBeVisible()
  await page.emulateMedia({ media: 'print' })
  await assertNoDocumentHorizontalOverflow(page)
  await page.emulateMedia({ media: 'screen' })
})

test('collapsed wall sheet prints one Letter/A4 page through the real Print button', async ({ page }) => {
  test.setTimeout(90_000)
  resetStage()
  await page.setViewportSize({ width: 1280, height: 900 })
  await install(page)
  const wallSheet = await signInOpenWallSheet(page)
  await stubWindowPrint(page)
  await wallSheet.getByRole('button', { name: 'Print' }).click()
  const wallPrintRoot = page.locator('.wall-kit-print-root[data-printing="true"]')
  await expect(wallPrintRoot).toBeVisible()
  await page.emulateMedia({ media: 'print' })
  await page.setViewportSize({ width: PRINT_LAYOUT_VIEWPORT_WIDTH, height: 900 })
  await assertNoDocumentHorizontalOverflow(page)
  const wallText = await wallPrintRoot.evaluate((el) => el.textContent ?? '')
  expect(wallText).toMatch(/STOCK-OUT/)
  expect(wallText).toMatch(/STOCK-IN/)
  expect(wallText).toMatch(/Storage room/)
  expect(wallText).toMatch(/Print at Actual size/)
  await assertPrintRuler50mm(wallPrintRoot)
  for (const format of ['Letter', 'A4'] as const) {
    const pdf = Buffer.from(await page.pdf({ format, ...PDF_PRINT_OPTIONS }))
    expect(countPdfPages(pdf)).toBe(1)
    await assertPdfDecodesCode128(pdf, ['STOCK-OUT', 'STOCK-IN'])
    expect(await pdfContainsVisibleText(pdf, 'Print at Actual size')).toBe(true)
    await assertPdfRulerContinuousLineMm(pdf)
    await page.emulateMedia({ media: 'screen' })
    await stubWindowPrint(page)
    await wallSheet.getByRole('button', { name: 'Print' }).click()
    await expect(page.locator('.wall-kit-print-root[data-printing="true"]')).toBeVisible()
    await page.emulateMedia({ media: 'print' })
    await page.setViewportSize({ width: PRINT_LAYOUT_VIEWPORT_WIDTH, height: 900 })
    await assertNoDocumentHorizontalOverflow(page)
  }
  await page.emulateMedia({ media: 'screen' })
})

test('wall sheet print includes expanded Symcode on one page', async ({ page }) => {
  test.setTimeout(90_000)
  resetStage()
  await page.setViewportSize({ width: 1280, height: 900 })
  await install(page)
  const wallSheet = await signInOpenWallSheet(page)
  await wallSheet.getByRole('button', { name: 'Using a Symcode handheld?' }).click()
  await expect(wallSheet.getByRole('img', { name: 'Code 128 %%SpecCode99' })).toBeVisible()
  await stubWindowPrint(page)
  await wallSheet.getByRole('button', { name: 'Print' }).click()
  const wallPrintRoot = page.locator('.wall-kit-print-root[data-printing="true"]')
  await expect(wallPrintRoot).toBeVisible()
  await page.emulateMedia({ media: 'print' })
  await page.setViewportSize({ width: PRINT_LAYOUT_VIEWPORT_WIDTH, height: 900 })
  await assertNoDocumentHorizontalOverflow(page)
  const wallText = await wallPrintRoot.evaluate((el) => el.textContent ?? '')
  expect(wallText).toMatch(/SpecCode99|%%SpecCode99/)
  expect(wallText).toMatch(/Storage room/)
  expect(wallText).toMatch(/Print at Actual size/)
  await expect(wallPrintRoot.locator('.kit-print-footer-instruction')).toContainText('Print at Actual size')
  await expect(wallPrintRoot.locator('.kit-print-ruler')).toBeVisible()
  await expect(wallPrintRoot.locator('.kit-print-ruler-caption')).toContainText(RULER_CAPTION_SNIPPET)
  await assertPrintRuler50mm(wallPrintRoot)
  for (const format of ['Letter', 'A4'] as const) {
    const pdf = Buffer.from(await page.pdf({ format, ...PDF_PRINT_OPTIONS }))
    expect(countPdfPages(pdf)).toBe(1)
    await assertPdfDecodesCode128(pdf, ['%%SpecCode99', 'STOCK-OUT', 'STOCK-IN'])
    expect(await pdfContainsVisibleText(pdf, RULER_CAPTION_SNIPPET)).toBe(true)
    await assertPdfRulerContinuousLineMm(pdf)
    await page.emulateMedia({ media: 'screen' })
    await stubWindowPrint(page)
    await wallSheet.getByRole('button', { name: 'Print' }).click()
    await expect(page.locator('.wall-kit-print-root[data-printing="true"]')).toBeVisible()
    await page.emulateMedia({ media: 'print' })
    await page.setViewportSize({ width: PRINT_LAYOUT_VIEWPORT_WIDTH, height: 900 })
    await assertNoDocumentHorizontalOverflow(page)
  }
  await page.emulateMedia({ media: 'screen' })
})

test('print media hides app chrome on the setup sheet', async ({ page }) => {
  resetStage()
  await page.setViewportSize({ width: 1280, height: 900 })
  await install(page)
  await signInAndOpenHouseholdScannerPairing(page)
  await stubWindowPrint(page)
  await page.getByRole('button', { name: 'Print setup & wall sheet' }).click()
  await expect(page.locator('.setup-kit-print-root[data-printing="true"]')).toBeVisible()
  await page.emulateMedia({ media: 'print' })
  await expect(page.getByRole('button', { name: 'Menu' })).toBeHidden()
  await expect(page.getByRole('dialog', { name: 'Connect a scanner' }).locator('.mantine-Modal-header')).toBeHidden()
  const pdf = Buffer.from(await page.pdf({ format: 'Letter', ...PDF_PRINT_OPTIONS }))
  const pdfText = await pdfVisibleText(pdf)
  expect(pdfText).not.toMatch(/\bMenu\b/)
  expect(pdfText).not.toMatch(/Sign in/)
  expect(pdfText).not.toContain(PAIRING_DIALOG_TITLE)
  await page.emulateMedia({ media: 'screen' })
})
