import { expect, test } from '@playwright/test'
import { commitSelectedScan, createKnownProduct, resetSharedState, resetToStockIn, scanBarcode, setScannerMode } from './helpers'

const barcode = '210000000011'
const productName = 'W1 Remove Demo Rice'

const contrastModes = [
  { app: 'light' as const, width: 390 },
  { app: 'dark' as const, width: 390 },
  { app: 'light' as const, width: 320 },
  { app: 'dark' as const, width: 320 },
]

test.describe('W1 shopping Remove copy', () => {
  test.beforeEach(async ({ request }) => {
    await resetSharedState(request)
  })

  test.afterEach(async ({ request }) => {
    await resetSharedState(request)
  })

  for (const mode of contrastModes) {
    test(`remove button at ${mode.width} ${mode.app}`, async ({ page, request }) => {
      test.use({ colorScheme: mode.app })
      await page.setViewportSize({ width: mode.width, height: 800 })
      await seedList(page, request)
      const remove = page.getByRole('button', { name: `Remove ${productName} for this trip` })
      await expect(remove).toBeVisible()
      await expect(remove).toHaveText('Remove for this trip')
      const overflow = await page.evaluate(
        () => document.documentElement.scrollWidth > document.documentElement.clientWidth,
      )
      expect(overflow).toBe(false)
      await remove.screenshot({
        path: `/opt/cursor/artifacts/w1-remove-button-${mode.width}-${mode.app}.png`,
      })
    })
  }

  test('after-remove status line at 390 light', async ({ page, request }) => {
    await page.setViewportSize({ width: 390, height: 800 })
    await seedList(page, request)
    await page.getByRole('button', { name: `Remove ${productName} for this trip` }).click()
    const status = page.getByRole('status')
    await expect(status).toContainText('is off the list for this trip')
    await expect(status).toBeFocused()
    await expect(page.getByRole('button', { name: /undo/i })).toHaveCount(0)
    await status.screenshot({ path: '/opt/cursor/artifacts/w1-after-remove-status-390-light.png' })
  })

  test('skip status copy fixtures', async ({ page }) => {
    await page.setViewportSize({ width: 390, height: 200 })
    await page.setContent(`
      <main style="font-family: system-ui; padding: 16px; max-width: 390px;">
        <p id="legacy" style="margin: 0 0 12px;">Removed for this trip. It comes back next trip.</p>
        <p id="actor" style="margin: 0;">Alex removed it for this trip Mon. It comes back next trip.</p>
      </main>
    `)
    await page.locator('#legacy').screenshot({ path: '/opt/cursor/artifacts/w1-skip-legacy-390-light.png' })
    await page.locator('#actor').screenshot({ path: '/opt/cursor/artifacts/w1-skip-actor-time-390-light.png' })
  })
})

async function seedList(page: import('@playwright/test').Page, request: import('@playwright/test').APIRequestContext) {
  await createKnownProduct(request, {
    barcode,
    name: productName,
    category: 'Dry goods',
    unitOfMeasure: 'bag',
  })
  await page.goto('/shopping')
  await resetToStockIn(page)
  await page.getByRole('link', { name: 'Scan Queue' }).click()
  await setScannerMode(page, 'stock_in')
  await commitSelectedScan(page, await scanBarcode(page, barcode), '2032-04-10')
  await commitSelectedScan(page, await scanBarcode(page, barcode), '2032-08-20')
  await page.getByRole('link', { name: 'Scan Queue' }).click()
  await setScannerMode(page, 'stock_out')
  await commitSelectedScan(page, await scanBarcode(page, barcode))
  await page.getByRole('link', { name: 'Shopping List' }).click()
  await page.getByRole('button', { name: 'Build the list' }).click()
  await expect(page.getByRole('row').filter({ hasText: productName })).toBeVisible()
}
