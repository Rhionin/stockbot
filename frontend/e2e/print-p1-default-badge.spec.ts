/**
 * P1: DEFAULT badge legibility with background graphics off (printBackground: false).
 * Artifact capture: STOCKBOT_PRINT_ARTIFACTS_DIR=/path npx playwright test e2e/print-p1-default-badge.spec.ts
 */
import { mkdirSync, writeFileSync } from 'node:fs'
import { join } from 'node:path'
import { expect, test } from '@playwright/test'
import { countPdfPages, install, resetStage, stubWindowPrint } from './householdStageFixtures'

const PDF_PRINT_OPTIONS = {
  preferCSSPageSize: true,
  printBackground: false,
  margin: { top: '0', right: '0', bottom: '0', left: '0' },
} as const
const PRINT_LAYOUT_VIEWPORT_WIDTH = 720
const OUT_DIR = process.env.STOCKBOT_PRINT_ARTIFACTS_DIR

async function routeDevices(page: import('@playwright/test').Page, defaultDirection: 'stock_in' | 'stock_out') {
  await page.route('**/api/devices', async (route) => {
    if (route.request().method() !== 'GET') return route.fallback()
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({
        devices: [{
          id: 'scan-synth',
          label: 'Synthetic scanner',
          createdAt: '2026-04-01T00:00:00Z',
          lastSeenAt: '2026-04-02T15:00:00Z',
          revokedAt: null,
          kind: 'esp32_scanner',
          online: true,
          inactivityRevertSeconds: 300,
          defaultDirection,
        }],
      }),
    })
  })
}

async function openSetupPrintRoot(page: import('@playwright/test').Page) {
  await page.goto('/')
  await page.getByLabel('Email').fill('ada@example.com')
  await page.getByRole('textbox', { name: 'Password' }).fill('correct-horse-battery')
  await page.getByRole('button', { name: 'Sign in' }).click()
  await page.goto('/household')
  await page.getByRole('button', { name: 'Connect a scanner' }).first().click()
  await expect(page.getByRole('dialog', { name: 'Connect a scanner' })).toBeVisible()
  await stubWindowPrint(page)
  await page.getByRole('button', { name: 'Print setup & wall sheet' }).click()
  const printRoot = page.locator('.setup-kit-print-root[data-printing="true"]')
  await expect(printRoot).toBeVisible()
  await page.emulateMedia({ media: 'print' })
  await page.setViewportSize({ width: PRINT_LAYOUT_VIEWPORT_WIDTH, height: 900 })
  return printRoot
}

async function assertDefaultBadgePrintInk(badge: import('@playwright/test').Locator) {
  const styles = await badge.evaluate((el) => {
    const cs = getComputedStyle(el)
    return { color: cs.color, backgroundColor: cs.backgroundColor, borderTopColor: cs.borderTopColor, borderTopWidth: cs.borderTopWidth }
  })
  expect(styles.color).toBe('rgb(17, 24, 39)')
  expect(styles.backgroundColor).toBe('rgb(255, 255, 255)')
  expect(styles.borderTopWidth).not.toBe('0px')
  expect(styles.borderTopColor).toBe('rgb(17, 24, 39)')
}

function writeP1Pdf(suffix: string, format: 'Letter' | 'A4', pdf: Buffer) {
  if (!OUT_DIR) return
  mkdirSync(OUT_DIR, { recursive: true })
  writeFileSync(join(OUT_DIR, `setup-sheet-p1-${suffix}-${format}.pdf`), pdf)
}

for (const scenario of [
  { suffix: 'stock-out-default', defaultDirection: 'stock_out' as const, badgeSelector: '.kit-wall-panel-out .kit-wall-default-badge' },
  { suffix: 'stock-in-default', defaultDirection: 'stock_in' as const, badgeSelector: '.kit-wall-panel-in .kit-wall-default-badge-in' },
] as const) {
  test(`P1 DEFAULT badge (${scenario.suffix}) Letter/A4 backgrounds off`, async ({ page }) => {
    test.setTimeout(90_000)
    resetStage()
    await page.setViewportSize({ width: 1280, height: 900 })
    await install(page)
    await routeDevices(page, scenario.defaultDirection)
    const printRoot = await openSetupPrintRoot(page)
    await assertDefaultBadgePrintInk(printRoot.locator(scenario.badgeSelector))
    for (const format of ['Letter', 'A4'] as const) {
      const pdf = Buffer.from(await page.pdf({ format, ...PDF_PRINT_OPTIONS }))
      expect(pdf.length).toBeGreaterThan(5000)
      expect(countPdfPages(pdf)).toBe(1)
      writeP1Pdf(scenario.suffix, format, pdf)
      if (format === 'Letter') {
        await page.emulateMedia({ media: 'screen' })
        await stubWindowPrint(page)
        await page.getByRole('button', { name: 'Print setup & wall sheet' }).click()
        await expect(printRoot).toBeVisible()
        await page.emulateMedia({ media: 'print' })
        await page.setViewportSize({ width: PRINT_LAYOUT_VIEWPORT_WIDTH, height: 900 })
      }
    }
  })
}
