import { expect, test } from '@playwright/test'
import { commitSelectedScan, createKnownProduct, inventoryRow, resetToStockIn, scanBarcode, setScannerMode } from './helpers'

const barcode = '210000000004'
const productName = 'E2E Shopping Rice'

test('inventory gap is derived, purchased, and leaves inventory unchanged', async ({ page, request }) => {
  await createKnownProduct(request, {
    barcode,
    name: productName,
    category: 'Dry goods',
    unitOfMeasure: 'bag',
  })
  await page.goto('/')
  await resetToStockIn(page)
  await commitSelectedScan(page, await scanBarcode(page, barcode), '2032-04-10')
  await commitSelectedScan(page, await scanBarcode(page, barcode), '2032-08-20')

  await page.getByRole('link', { name: 'Inventory' }).click()
  const riceRow = inventoryRow(page, productName)
  await expect(riceRow.getByText('2 bag', { exact: true })).toBeVisible()
  // Opening is one household snapshot. Another spec may already have finished
  // it; completing it here only matters when these stock-ins are still the
  // first scan.
  const finishOpening = page.getByRole('button', { name: 'This scan is complete' })
  if (await finishOpening.isVisible()) {
    await finishOpening.click()
    await expect(page.getByRole('alert', { name: 'Opening inventory' })).toBeHidden()
  }

  // After the snapshot, using one bag is consumption. The list replaces it.
  await page.getByRole('link', { name: 'Scan Queue' }).click()
  await setScannerMode(page, 'stock_out')
  await commitSelectedScan(page, await scanBarcode(page, barcode))

  await page.getByRole('link', { name: 'Shopping List' }).click()
  await page.getByRole('button', { name: 'Build the list' }).click()

  const derivedRow = page
    .getByRole('row')
    .filter({ hasText: productName })
    .filter({ has: page.getByText('Derived', { exact: true }) })
  await expect(derivedRow.getByText('1 bag', { exact: true })).toBeVisible()
  await expect(derivedRow.getByText('replacing 1 you used')).toBeVisible()
  await expect(derivedRow.getByText('Derived', { exact: true })).toBeVisible()

  // Marking the shortfall purchased dismisses it (purchased gaps stay hidden
  // until the quantity changes) and must NOT change on-hand inventory.
  await derivedRow.getByRole('button', { name: `Mark ${productName} purchased` }).click()
  await expect(derivedRow).toHaveCount(0)
  await expect(page.getByText('Build the list before sending it to Kroger.')).toBeVisible()
  await expect(page.getByRole('button', { name: 'Send to Kroger' })).toBeDisabled()

  // A purchase records intent; on-hand inventory is unchanged (still 1 bag).
  await page.getByRole('link', { name: 'Inventory' }).click()
  await expect(inventoryRow(page, productName).getByText('1 bag', { exact: true })).toBeVisible()
})

const longStore = 'West Valley Neighborhood Market and Fresh Foods Cooperative'

test('cart control stays beside a long store name and Remove keeps an edge', async ({ page }) => {
  const json = (body: unknown) => ({
    status: 200,
    contentType: 'application/json',
    body: JSON.stringify(body),
  })
  await page.route((url) => url.pathname === '/api/providers', (route) => route.fulfill(json([{
    id: 'kroger',
    displayName: longStore,
    capabilities: {
      auth: 'oauth2_authorization_code',
      delivery: 'server_push',
      confirmation: 'per_request',
      mutation: 'add_only',
      identity: 'derived',
    },
    connectionState: 'connected',
    credentialsConfigured: true,
  }])))
  await page.route(
    (url) => url.pathname === '/api/shopping-list',
    (route) => route.fulfill(json([{
      id: 'manual-beans',
      itemId: 'beans',
      quantity: 1,
      source: 'manual',
      purchasedAt: null,
    }])),
  )
  await page.route((url) => url.pathname === '/api/inventory', (route) => route.fulfill(json([{
    item: {
      id: 'beans',
      userId: 'user-1',
      productId: 'product-beans',
      product: {
        id: 'product-beans',
        name: 'Cut green beans',
        category: 'Pantry',
        unitOfMeasure: '14.5 oz',
      },
      targetQuantity: null,
      createdAt: '2026-01-01T00:00:00Z',
    },
    instanceCount: 2,
    nearExpiryCount: 0,
    expiredCount: 0,
    needsAttention: false,
  }])))

  await page.setViewportSize({ width: 390, height: 800 })
  await page.goto('/shopping')
  const cart = page.getByRole('button', { name: 'Start a new cart' })
  const name = page.getByText(longStore)
  const remove = page.getByRole('button', { name: 'Remove Cut green beans' })
  await expect(cart).toBeVisible()
  await expect(remove).toBeVisible()

  for (const width of [320, 390]) {
    await page.setViewportSize({ width, height: 800 })
    const cartBox = await cart.boundingBox()
    const nameBox = await name.boundingBox()
    expect(cartBox, `cart box at ${width}`).not.toBeNull()
    expect(nameBox, `name box at ${width}`).not.toBeNull()
    expect(Math.round(cartBox!.y), `shared top at ${width}`).toBe(Math.round(nameBox!.y))
    expect(cartBox!.x + cartBox!.width, `cart right edge at ${width}`).toBeLessThanOrEqual(width)
  }

  const border = await remove.evaluate((element) => getComputedStyle(element).borderTopColor)
  expect(border).not.toBe('rgba(0, 0, 0, 0)')
})
