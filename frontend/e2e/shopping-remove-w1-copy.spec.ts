import { mkdir } from 'node:fs/promises'
import { join } from 'node:path'
import { fileURLToPath } from 'node:url'
import { expect, test } from '@playwright/test'
import { createKnownProduct, resetSharedState } from './helpers'

const barcode = '210000000011'
const productName = 'W1 Remove Demo Rice'
const artifactsDir = join(fileURLToPath(new URL('.', import.meta.url)), 'artifacts', 'w1')

const contrastModes = [
  { app: 'light' as const, width: 390 },
  { app: 'dark' as const, width: 390 },
  { app: 'light' as const, width: 320 },
  { app: 'dark' as const, width: 320 },
] as const

type ListContext = {
  page: import('@playwright/test').Page
  request: import('@playwright/test').APIRequestContext
  pantryItemId: string
}

test.describe('W1 shopping Remove copy', () => {
  test.beforeAll(async () => {
    await mkdir(artifactsDir, { recursive: true })
  })

  test.beforeEach(async ({ request }) => {
    await resetSharedState(request)
  })

  test.afterEach(async ({ request }) => {
    await resetSharedState(request)
  })

  for (const mode of contrastModes) {
    test.describe(`${mode.width} ${mode.app}`, () => {
      test.use({ colorScheme: mode.app })

      test('remove control, after-remove with Undo, and restore', async ({ page, request }) => {
        await page.setViewportSize({ width: mode.width, height: 800 })
        const ctx = await seedList(page, request)
        await installUndoRoute(page, ctx.pantryItemId)

        const remove = page.getByRole('button', { name: `Remove ${productName} for this trip` })
        await expect(remove).toBeVisible()
        await expect(remove).toHaveText('Remove for this trip')

        const removeOverflow = await page.evaluate(
          () => document.documentElement.scrollWidth > document.documentElement.clientWidth,
        )
        expect(removeOverflow).toBe(false)
        const removeShot = join(artifactsDir, `w1-remove-button-${mode.width}-${mode.app}.png`)
        await remove.screenshot({ path: removeShot })

        await remove.click()
        const undo = page.getByRole('button', { name: 'Undo' })
        await expect(page.getByRole('status')).toContainText('is off the list for this trip')
        await expect(undo).toBeVisible()
        await expect(undo).toBeFocused()

        const undoBox = await undo.boundingBox()
        expect(undoBox).not.toBeNull()
        if (undoBox !== null) {
          expect(undoBox.width).toBeGreaterThanOrEqual(44)
          expect(undoBox.height).toBeGreaterThanOrEqual(44)
        }

        const statusOverflow = await page.evaluate(
          () => document.documentElement.scrollWidth > document.documentElement.clientWidth,
        )
        expect(statusOverflow).toBe(false)

        const afterRemoveShot = join(artifactsDir, `w1-after-remove-undo-${mode.width}-${mode.app}.png`)
        await page.getByRole('row').filter({ hasText: productName }).screenshot({ path: afterRemoveShot })

        await undo.click()
        await expect(page.getByRole('status')).toHaveCount(0)
        await expect(page.getByRole('row').filter({ hasText: productName })).toBeVisible()

        const afterUndoShot = join(artifactsDir, `w1-after-undo-${mode.width}-${mode.app}.png`)
        await page.getByRole('row').filter({ hasText: productName }).screenshot({ path: afterUndoShot })
      })
    })
  }

  test('skip status copy fixtures', async ({ page }) => {
    await page.setViewportSize({ width: 390, height: 200 })
    await page.setContent(`
      <main style="font-family: system-ui; padding: 16px; max-width: 390px;">
        <p id="legacy" style="margin: 0 0 12px;">Removed for this trip. It comes back next trip.</p>
        <p id="actor" style="margin: 0;">Alex removed it for this trip Mon. It comes back next trip.</p>
      </main>
    `)
    await page.locator('#legacy').screenshot({ path: join(artifactsDir, 'w1-skip-legacy-390-light.png') })
    await page
      .locator('#actor')
      .screenshot({ path: join(artifactsDir, 'w1-skip-actor-time-390-light.png') })
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

async function seedList(
  page: import('@playwright/test').Page,
  request: import('@playwright/test').APIRequestContext,
): Promise<ListContext> {
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
  return { page, request, pantryItemId: row.item.id }
}

async function installUndoRoute(page: import('@playwright/test').Page, pantryItemId: string) {
  await page.route(`**/api/shopping-list/items/${pantryItemId}/undo-remove`, async (route) => {
    if (route.request().method() !== 'POST') {
      await route.continue()
      return
    }
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({
        id: 'manual-w1-undo',
        itemId: pantryItemId,
        quantity: 1,
        source: 'manual',
        purchasedAt: null,
      }),
    })
  })
}
