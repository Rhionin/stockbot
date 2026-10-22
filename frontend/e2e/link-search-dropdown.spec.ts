import { expect, test, type APIRequestContext } from '@playwright/test'
import { createKnownProduct, resetToStockIn, setScannerMode } from './helpers'

test.use({ viewport: { width: 390, height: 844 } })

const pantry = [
  { barcode: '390000000001', name: 'Amber Rice' },
  { barcode: '390000000002', name: 'Blue Lentils' },
  { barcode: '390000000003', name: 'Canned Peaches' },
  { barcode: '390000000004', name: 'Dried Mango' },
  { barcode: '390000000005', name: 'Earl Grey' },
  { barcode: '390000000006', name: 'Farro' },
]
const fillers = [
  { barcode: '390000000011', name: 'Garden Peas' },
  { barcode: '390000000012', name: 'Honey Oats' },
  { barcode: '390000000013', name: 'Instant Coffee' },
  { barcode: '390000000014', name: 'Jasmine Rice' },
  { barcode: '390000000015', name: 'Kidney Beans' },
  { barcode: '390000000016', name: 'Lemon Tea' },
  { barcode: '390000000017', name: 'Macaroni' },
  { barcode: '390000000018', name: 'Navy Beans' },
]
const mystery = { barcode: '390000000099', name: 'Unstocked Millet' }

const createScan = async (
  request: APIRequestContext,
  barcode: string,
  direction: 'stock_in' | 'stock_out',
): Promise<string> => {
  const response = await request.post('/api/scans', {
    data: { barcode, direction, userId: 'user-1' },
  })
  expect(response.ok(), await response.text()).toBe(true)
  const body = await response.json() as { id: string }
  return body.id
}

const stockIn = async (request: APIRequestContext, barcode: string): Promise<void> => {
  const id = await createScan(request, barcode, 'stock_in')
  const committed = await request.post(`/api/scans/${id}/commit`, { data: {} })
  expect(committed.ok(), await committed.text()).toBe(true)
}

test('link search shows at least five rows at 390px when the field is at the bottom', async ({ page, request }) => {
  for (const item of pantry) {
    await createKnownProduct(request, {
      barcode: item.barcode,
      name: item.name,
      category: 'Dry goods',
      unitOfMeasure: 'box',
    })
    await stockIn(request, item.barcode)
  }
  await createKnownProduct(request, {
    barcode: mystery.barcode,
    name: mystery.name,
    category: 'Dry goods',
    unitOfMeasure: 'box',
  })
  await stockIn(request, mystery.barcode)
  for (const item of fillers) {
    await createKnownProduct(request, {
      barcode: item.barcode,
      name: item.name,
      category: 'Dry goods',
      unitOfMeasure: 'box',
    })
    await stockIn(request, item.barcode)
  }
  await createScan(request, mystery.barcode, 'stock_out')
  for (const item of fillers) {
    await createScan(request, item.barcode, 'stock_out')
  }

  await page.route('**/api/scans/*/commit', async (route) => {
    if (route.request().method() !== 'POST') {
      await route.continue()
      return
    }
    await route.fulfill({
      status: 409,
      contentType: 'application/json',
      body: JSON.stringify({ error: 'barcode is not linked', code: 'no_item', suggestions: [] }),
    })
  })

  await page.goto('/')
  await resetToStockIn(page)
  await setScannerMode(page, 'stock_out')

  const card = page.getByRole('article', { name: `Scan ${mystery.barcode}`, exact: true })
  await card.scrollIntoViewIfNeeded()
  await card.getByRole('button', { name: 'Confirm', exact: true }).click()
  const search = card.getByLabel('Search products')
  await expect(search).toBeVisible()
  await search.evaluate((element) => {
    element.scrollIntoView({ block: 'end', inline: 'nearest' })
  })
  const gapBelow = await search.evaluate((element) => {
    const rect = element.getBoundingClientRect()
    let node = element.parentElement
    while (node) {
      const overflowY = getComputedStyle(node).overflowY
      if (overflowY === 'auto' || overflowY === 'scroll' || overflowY === 'hidden' || overflowY === 'clip') {
        return node.getBoundingClientRect().bottom - rect.bottom
      }
      node = node.parentElement
    }
    return window.innerHeight - rect.bottom
  })
  expect(gapBelow).toBeLessThan(80)

  await search.click()
  const dropdown = card.locator('.mantine-Combobox-dropdown')
  const option = dropdown.getByRole('option').first()
  await expect(option).toBeVisible()
  const visibleHeight = await dropdown.evaluate((element) => {
    const rect = element.getBoundingClientRect()
    const top = Math.max(rect.top, 0)
    const bottom = Math.min(rect.bottom, window.innerHeight)
    return Math.max(0, bottom - top)
  })
  const optionBox = await option.boundingBox()
  expect(optionBox).not.toBeNull()
  expect(visibleHeight).toBeGreaterThanOrEqual(optionBox!.height * 5 - 2)
})
