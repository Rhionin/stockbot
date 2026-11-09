import { expect, test } from '@playwright/test'
import { createKnownProduct, resetSharedState } from './helpers'

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
    test.describe(`${mode.width} ${mode.app}`, () => {
      test.use({ colorScheme: mode.app })

      test('remove button layout and label', async ({ page, request }, testInfo) => {
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
          path: testInfo.outputPath(`w1-remove-button-${mode.width}-${mode.app}.png`),
        })
      })
    })
  }

  test('after-remove status line at 390 light', async ({ page, request }, testInfo) => {
    await page.setViewportSize({ width: 390, height: 800 })
    await seedList(page, request)
    await page.getByRole('button', { name: `Remove ${productName} for this trip` }).click()
    const status = page.getByRole('status')
    await expect(status).toContainText('is off the list for this trip')
    await expect(status).toBeFocused()
    await expect(page.getByRole('button', { name: /undo/i })).toHaveCount(0)
    await status.screenshot({ path: testInfo.outputPath('w1-after-remove-status-390-light.png') })
  })

  test('skip status copy fixtures', async ({ page }, testInfo) => {
    await page.setViewportSize({ width: 390, height: 200 })
    await page.setContent(`
      <main style="font-family: system-ui; padding: 16px; max-width: 390px;">
        <p id="legacy" style="margin: 0 0 12px;">Removed for this trip. It comes back next trip.</p>
        <p id="actor" style="margin: 0;">Alex removed it for this trip Mon. It comes back next trip.</p>
      </main>
    `)
    await page.locator('#legacy').screenshot({ path: testInfo.outputPath('w1-skip-legacy-390-light.png') })
    await page
      .locator('#actor')
      .screenshot({ path: testInfo.outputPath('w1-skip-actor-time-390-light.png') })
  })
})

async function stock(
  request: import('@playwright/test').APIRequestContext,
  direction: 'stock_in' | 'stock_out',
  unitCount = 1,
) {
  const scan = await request.post('/api/scans', {
    data: { barcode, direction, userId: 'user-1', unitCount },
  })
  expect(scan.ok()).toBe(true)
  const entry = (await scan.json()) as { id: string }
  const commit = await request.post(`/api/scans/${entry.id}/commit`, { data: {} })
  expect(commit.ok(), await commit.text()).toBe(true)
}

async function seedList(page: import('@playwright/test').Page, request: import('@playwright/test').APIRequestContext) {
  await createKnownProduct(request, {
    barcode,
    name: productName,
    category: 'Dry goods',
    unitOfMeasure: 'bag',
  })
  await stock(request, 'stock_in', 1)
  const inventory = await request.get('/api/inventory')
  expect(inventory.ok()).toBe(true)
  const rows = (await inventory.json()) as { item: { id: string; product: { name: string } } }[]
  const row = rows.find((candidate) => candidate.item.product.name === productName)
  if (row === undefined) throw new Error(`inventory is missing ${productName}`)
  const added = await request.post('/api/shopping-list/items', {
    data: { itemId: row.item.id, quantity: 1 },
  })
  expect(added.ok()).toBe(true)
  await page.goto('/shopping')
  await expect(page.getByRole('row').filter({ hasText: productName })).toBeVisible()
}
