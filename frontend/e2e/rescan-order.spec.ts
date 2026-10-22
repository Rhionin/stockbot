import { expect, test, type APIRequestContext, type Page } from '@playwright/test'
import { createKnownProduct, resetToStockIn, scanBarcode } from './helpers'

const articleLabels = (page: Page) =>
  page.locator('.scan-page-queue article').evaluateAll((nodes) =>
    nodes.map((node) => node.getAttribute('aria-label')))

// Other specs in this run share the database, so older cards may sit below.
const topTwo = async (page: Page) => (await articleLabels(page)).slice(0, 2)

const cancelBarcode = async (request: APIRequestContext, barcode: string) => {
  const response = await request.get('/api/scans', { params: { userId: 'user-1', status: 'pending' } })
  expect(response.ok()).toBe(true)
  const entries = await response.json() as { id: string; barcode: string }[]
  for (const entry of entries) {
    if (entry.barcode !== barcode) continue
    const removed = await request.patch(`/api/scans/${entry.id}`, { data: { status: 'cancelled' } })
    expect(removed.ok()).toBe(true)
  }
}

const flashClass = /scan-entry-card--(?:just-captured|changed-animated|changed-static)/

test('rescanning an open item flashes its card, moves it to the top, and keeps that order after reload', async ({ page, request }) => {
  test.setTimeout(90_000)
  const oats = { barcode: '810000000011', name: 'Reorder Oats' }
  const milk = { barcode: '810000000012', name: 'Reorder Milk' }
  const oatsName = `Scan ${oats.barcode}`
  const milkName = `Scan ${milk.barcode}`
  try {
    for (const item of [oats, milk]) {
      await createKnownProduct(request, {
        barcode: item.barcode,
        name: item.name,
        category: 'Grocery',
        unitOfMeasure: 'bag',
      })
    }

    await page.goto('/')
    await resetToStockIn(page)
    await scanBarcode(page, oats.barcode)
    await scanBarcode(page, milk.barcode)
    await expect.poll(() => topTwo(page)).toEqual([milkName, oatsName])

    const oatsCard = page.getByRole('article', { name: oatsName, exact: true })
    await scanBarcode(page, oats.barcode)

    // The card is already on screen, so the scan helper returns before the
    // merge lands. Order and the flash class update together when it does.
    await expect.poll(async () => {
      const labels = await topTwo(page)
      const className = (await oatsCard.getAttribute('class')) ?? ''
      return {
        order: labels.join(','),
        flashed: flashClass.test(className),
      }
    }).toEqual({ order: `${oatsName},${milkName}`, flashed: true })
    await expect(oatsCard.getByRole('textbox', { name: 'Unit count', exact: true })).toHaveValue('2')

    await page.reload()
    await resetToStockIn(page)
    await expect.poll(() => topTwo(page)).toEqual([oatsName, milkName])
    await expect(page.getByRole('article', { name: oatsName, exact: true }).getByRole('textbox', { name: 'Unit count', exact: true })).toHaveValue('2')
  } finally {
    await cancelBarcode(request, oats.barcode)
    await cancelBarcode(request, milk.barcode)
  }
})
