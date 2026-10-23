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
