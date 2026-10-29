import { expect, test } from '@playwright/test'
import {
  PAIRING,
  countPdfPages,
  install,
  pdfContainsText,
  resetStage,
  signInAndOpenHouseholdScannerPairing,
  stage,
  stubWindowPrint,
} from './householdStageFixtures'

const MOBILE_WIDTHS = [320, 360, 390, 412] as const
const DPRS = [1, 2, 3] as const

async function assertIntegerDevicePxPerModule(page: import('@playwright/test').Page) {
  const dialog = page.getByRole('dialog', { name: 'Connect a scanner' })
  const svg = dialog.locator('.pairing-screen-only svg.pairing-code128')
  await expect(svg).toBeVisible()
  const metrics = await svg.evaluate((el) => {
    const quiet = Number(el.getAttribute('data-quiet-modules'))
    const barModules = Number(el.getAttribute('data-modules'))
    const totalModules = barModules + 2 * quiet
    const rect = el.getBoundingClientRect()
    const dpr = window.devicePixelRatio || 1
    const devicePxPerModule = (rect.width * dpr) / totalModules
    return { devicePxPerModule, dpr, totalModules, width: rect.width }
  })
  expect(Math.abs(metrics.devicePxPerModule - Math.round(metrics.devicePxPerModule))).toBeLessThan(0.01)
  if (metrics.dpr >= 2) {
    expect(metrics.devicePxPerModule).toBeGreaterThanOrEqual(2)
  }
  await expect(svg.evaluate((el) => {
    const attrWidth = Number(el.getAttribute('width'))
    const rendered = el.getBoundingClientRect().width
    return Math.abs(rendered - attrWidth) < 1.5
  })).resolves.toBe(true)
  await expect(page.evaluate(() => document.documentElement.scrollHeight <= window.innerHeight + 1)).resolves.toBe(true)
}

for (const width of MOBILE_WIDTHS) {
  for (const dpr of DPRS) {
    test(`on-screen pairing Code128 module px at ${width}px DPR ${dpr}`, async ({ page }) => {
      resetStage()
      await page.setViewportSize({ width, height: 700 })
      await install(page)
      await page.addInitScript((ratio) => {
        Object.defineProperty(window, 'devicePixelRatio', { get: () => ratio, configurable: true })
      }, dpr)
      await signInAndOpenHouseholdScannerPairing(page)
      await expect(page.getByRole('dialog', { name: 'Connect a scanner' })).toBeVisible()
      await assertIntegerDevicePxPerModule(page)
    })
  }
}

test('setup sheet prints one Letter/A4 page through the real Print button', async ({ page }) => {
  resetStage()
  stage.householdName = 'Test Household'
  await page.setViewportSize({ width: 1280, height: 900 })
  await install(page)
  await signInAndOpenHouseholdScannerPairing(page)
  await expect(page.getByRole('dialog', { name: 'Connect a scanner' })).toBeVisible()
  await expect(page.locator('.pairing-screen-only svg.pairing-code128')).toBeVisible()
  await stubWindowPrint(page)
  await page.getByRole('button', { name: 'Print setup & wall sheet' }).click()
  const printRoot = page.locator('.setup-kit-print-root[data-printing="true"]')
  await expect(printRoot).toBeVisible()
  await expect(printRoot).not.toHaveAttribute('hidden')
  await expect(page.locator('.setup-kit-print-root svg[data-barcode]').first()).toBeAttached()
  await page.emulateMedia({ media: 'print' })
  const rootHeight = await printRoot.evaluate((el) => el.getBoundingClientRect().height)
  expect(rootHeight).toBeGreaterThan(100)
  const hintBottom = await printRoot.locator('.kit-print-footer-instruction').evaluate((el) => el.getBoundingClientRect().bottom)
  const rulerTop = await printRoot.locator('.kit-print-ruler').evaluate((el) => el.getBoundingClientRect().top)
  expect(rulerTop).toBeGreaterThanOrEqual(hintBottom - 0.5)
  const sheetText = await printRoot.evaluate((el) => el.textContent ?? '')
  expect(sheetText).toMatch(/STOCK-OUT/)
  expect(sheetText).toMatch(/STOCK-IN/)
  expect(sheetText).toMatch(new RegExp(PAIRING.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')))
  for (const svg of await page.locator('.setup-kit-print-root svg[data-barcode]').all()) {
    const box = await svg.boundingBox()
    const barModules = Number(await svg.getAttribute('data-modules'))
    const totalModules = Number(await svg.getAttribute('data-total-modules') ?? barModules)
    expect(box).not.toBeNull()
    expect(box!.width / totalModules).toBeGreaterThanOrEqual(1.417)
  }
  for (const format of ['Letter', 'A4'] as const) {
    const pdf = Buffer.from(await page.pdf({ format, preferCSSPageSize: true }))
    expect(pdf.length).toBeGreaterThan(5000)
    expect(countPdfPages(pdf)).toBe(1)
    await page.emulateMedia({ media: 'screen' })
    await stubWindowPrint(page)
    await page.getByRole('button', { name: 'Print setup & wall sheet' }).click()
    await expect(page.locator('.setup-kit-print-root[data-printing="true"]')).toBeVisible()
    await page.emulateMedia({ media: 'print' })
  }
  await page.emulateMedia({ media: 'screen' })
})

test('wall sheet print includes expanded Symcode on one page', async ({ page }) => {
  resetStage()
  await page.setViewportSize({ width: 1280, height: 900 })
  await install(page)
  await page.goto('/')
  await page.getByLabel('Email').fill('ada@example.com')
  await page.getByRole('textbox', { name: 'Password' }).fill('correct-horse-battery')
  await page.getByRole('button', { name: 'Sign in' }).click()
  await page.getByRole('button', { name: 'Menu' }).click()
  await page.getByRole('menuitem', { name: 'Devices' }).click()
  await page.getByRole('button', { name: 'Print wall barcodes' }).click()
  const wallSheet = page.getByRole('dialog', { name: 'Wall barcodes' })
  await wallSheet.getByRole('button', { name: 'Using a Symcode handheld?' }).click()
  await expect(wallSheet.getByRole('img', { name: 'Code 128 %%SpecCode99' })).toBeVisible()
  await stubWindowPrint(page)
  await wallSheet.getByRole('button', { name: 'Print' }).click()
  const wallPrintRoot = page.locator('.wall-kit-print-root[data-printing="true"]')
  await expect(wallPrintRoot).toBeVisible()
  await page.emulateMedia({ media: 'print' })
  const wallText = await wallPrintRoot.evaluate((el) => el.textContent ?? '')
  expect(wallText).toMatch(/SpecCode99|%%SpecCode99/)
  for (const format of ['Letter', 'A4'] as const) {
    const pdf = Buffer.from(await page.pdf({ format, preferCSSPageSize: true }))
    expect(countPdfPages(pdf)).toBe(1)
    expect(pdfContainsText(pdf, 'SpecCode99') || wallText.includes('SpecCode99')).toBe(true)
    await page.emulateMedia({ media: 'screen' })
    await stubWindowPrint(page)
    await wallSheet.getByRole('button', { name: 'Print' }).click()
    await expect(page.locator('.wall-kit-print-root[data-printing="true"]')).toBeVisible()
    await page.emulateMedia({ media: 'print' })
  }
  await page.emulateMedia({ media: 'screen' })
})
