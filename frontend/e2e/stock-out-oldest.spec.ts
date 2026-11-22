import { expect, test } from '@playwright/test'
import { commitSelectedScan, createKnownProduct, inventoryRow, resetToStockIn, scanBarcode, setScannerMode } from './helpers'

const barcode = '210000000003'
const productName = 'E2E Oldest-First Pasta'

test('stock-out without a selection removes the earliest-expiring instance', async ({ page, request }) => {
  await createKnownProduct(request, {
    barcode,
    name: productName,
    category: 'Dry goods',
    unitOfMeasure: 'box',
  })
  await page.goto('/')
  await resetToStockIn(page)

  await commitSelectedScan(page, await scanBarcode(page, barcode), '2030-01-10')
  await commitSelectedScan(page, await scanBarcode(page, barcode), '2030-12-20')

  // Switch the scanner to stock_out mode by scanning the control barcode, then
  // scan the product so the card is stamped stock_out.
  await setScannerMode(page, 'stock_out')
  const stockOutCard = await scanBarcode(page, barcode)
  // The date field and instance list stay behind More. Open it, leave the
  // default (Use oldest available automatically) selected, then approve.
  await expect(stockOutCard.getByLabel('Expiration date')).toHaveCount(0)
  await stockOutCard.getByRole('button', { name: 'More' }).click()
  await expect(
    stockOutCard.getByRole('radio', { name: 'Use oldest available automatically' }),
  ).toBeVisible()
  await stockOutCard.getByRole('button', { name: 'Confirm', exact: true }).click()
  await expect(stockOutCard).toHaveCount(0)
  await page.getByRole('link', { name: 'Inventory' }).click()

  const pastaRow = inventoryRow(page, productName)
  await expect(pastaRow.getByText('1 box', { exact: true })).toBeVisible()
  await pastaRow.getByRole('button', { name: `Actions for ${productName}` }).click()
  await page.getByRole('menuitem', { name: 'View details' }).click()
  await expect(page.getByRole('listitem').getByText('Expires Dec 20, 2030', { exact: true })).toBeVisible()
  await expect(page.getByText('Expires Jan 10, 2030', { exact: true })).toHaveCount(0)
})
