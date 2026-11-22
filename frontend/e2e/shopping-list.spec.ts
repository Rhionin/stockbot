import { expect, test } from '@playwright/test'
import { commitSelectedScan, createKnownProduct, inventoryRow, resetSharedState, resetToStockIn, scanBarcode, setScannerMode } from './helpers'

const barcode = '210000000004'
const productName = 'E2E Shopping Rice'

test.beforeEach(async ({ request }) => {
  await resetSharedState(request)
})

test.afterEach(async ({ request }) => {
  await resetSharedState(request)
})

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

  // Opening is one household snapshot. Another spec may already have finished
  // it. Read the settings response the inventory page loads, instead of
  // sampling the banner once before it paints.
  const supplySettings = page.waitForResponse((response) => {
    const url = new URL(response.url())
    return url.pathname === '/api/settings/supply' && response.request().method() === 'GET' && response.ok()
  })
  await page.getByRole('link', { name: 'Inventory' }).click()
  const riceRow = inventoryRow(page, productName)
  await expect(riceRow.getByText('2 bag', { exact: true })).toBeVisible()
  const settings = await supplySettings.then((response) => response.json() as Promise<{ opening?: boolean }>)
  if (settings.opening === true) {
    await page.getByRole('button', { name: 'This scan is complete' }).click()
    await expect(page.getByRole('alert', { name: 'Opening inventory' })).toBeHidden()
  }

  // After the snapshot, using one bag is consumption. The list replaces it.
  await page.getByRole('link', { name: 'Scan Queue' }).click()
  await setScannerMode(page, 'stock_out')
  await commitSelectedScan(page, await scanBarcode(page, barcode))

  // The stock-out coalescer may stage the derived line before this page's GET.
  // The fill button is then "Update the list", not "Build the list". Wait until
  // that load finishes, then click whichever label is showing.
  await page.getByRole('link', { name: 'Shopping List' }).click()
  await expect(page.getByRole('heading', { name: 'Shopping plan' })).toBeVisible()
  await expect(page.getByLabel('Loading shopping plan')).toBeHidden()
  const fillList = page.getByRole('button', { name: /Build the list|Update the list/ })
  await expect(fillList).toBeEnabled()
  await fillList.click()

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
