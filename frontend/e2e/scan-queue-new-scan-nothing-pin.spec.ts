import { expect, test, type APIRequestContext, type Locator, type Page } from '@playwright/test'
import { createKnownProduct, resetToStockIn, scanBarcode, setScannerMode } from './helpers'

const failureNote = 'Nothing on hand to take out.'

const viewports = [
  { width: 390, height: 844 },
  { width: 1280, height: 800 },
] as const

const cancelPending = async (request: APIRequestContext, barcodes: string[]) => {
  const response = await request.get('/api/scans', { params: { userId: 'user-1', status: 'pending' } })
  expect(response.ok()).toBe(true)
  const entries = await response.json() as { id: string; barcode: string }[]
  for (const entry of entries) {
    if (!barcodes.includes(entry.barcode)) continue
    const removed = await request.patch(`/api/scans/${entry.id}`, { data: { status: 'cancelled' } })
    expect(removed.ok()).toBe(true)
  }
}

const itemsFor = (width: number, repeat: number) => Array.from({ length: 14 }, (_, index) => ({
  barcode: `297${width}${repeat}${String(index).padStart(2, '0')}`,
  name: `Nothing pin ${width} r${repeat} ${index}`,
}))

const markerTopInQueue = async (page: Page, marker: Locator) =>
  page.evaluate(({ markerAria }) => {
    const queue = document.querySelector('.scan-page-queue')
    if (queue === null) throw new Error('missing scan queue')
    const articles = [...queue.querySelectorAll('article')]
    const markerEl = articles.find((node) => node.getAttribute('aria-label') === markerAria)
    if (markerEl === undefined) throw new Error(`missing marker ${markerAria}`)
    return markerEl.getBoundingClientRect().top - queue.getBoundingClientRect().top
  }, { markerAria: (await marker.getAttribute('aria-label')) ?? '' })

const parkMidViewport = async (page: Page, markerBarcode: string) => {
  const queue = page.locator('.scan-page-queue')
  const max = await queue.evaluate((element) => element.scrollHeight - element.clientHeight)
  expect(max).toBeGreaterThan(120)
  const marker = page.getByRole('article', { name: new RegExp(`^Scan ${markerBarcode}(,|$)`), exact: false })
  await expect(marker).toBeVisible()
  const viewport = page.viewportSize()
  expect(viewport).not.toBeNull()

  const offsets = Array.from({ length: 30 }, (_, step) => Math.round((max * step) / 29))
  for (const scrollTop of offsets) {
    await queue.evaluate((element, top) => {
      element.scrollTop = top
    }, scrollTop)
    const box = await marker.boundingBox()
    if (box === null) continue
    const mid = viewport!.height * 0.5
    const centerY = box.y + box.height / 2
    if (centerY >= mid - 40 && centerY <= mid + 80 && box.y >= 64) {
      return marker
    }
  }
  throw new Error(`could not park ${markerBarcode} mid-viewport`)
}

for (const viewport of viewports) {
  test(`a new scan at the top does not shift a mid-queue row when a nothing card is in the queue at ${viewport.width}px`, async ({ page, request }, testInfo) => {
    test.setTimeout(120_000)
    const repeat = testInfo.repeatEachIndex
    const items = itemsFor(viewport.width, repeat)
    const failureBarcode = items[8].barcode
    const marker = items[9]
    const incoming = {
      barcode: `297${viewport.width}${repeat}99`,
      name: `Nothing pin new ${viewport.width} r${repeat}`,
    }
    const barcodes = [...items.map((item) => item.barcode), incoming.barcode]

    try {
      await cancelPending(request, barcodes)
      await page.setViewportSize(viewport)
      for (const item of [...items, incoming]) {
        await createKnownProduct(request, {
          barcode: item.barcode,
          name: item.name,
          category: 'Grocery',
          unitOfMeasure: 'carton',
        })
      }

      for (const item of items) {
        const created = await request.post('/api/scans', {
          data: { barcode: item.barcode, direction: 'stock_in', userId: 'user-1' },
        })
        expect(created.ok(), await created.text()).toBe(true)
        const createdBody = await created.json() as { id: string }
        const committed = await request.post(`/api/scans/${createdBody.id}/commit`, { data: {} })
        expect(committed.ok(), await committed.text()).toBe(true)
      }

      await page.goto('/')
      await resetToStockIn(page)
      await setScannerMode(page, 'stock_out')
      for (const item of items) {
        const card = await scanBarcode(page, item.barcode)
        if (item.barcode === failureBarcode) {
          for (let step = 0; step < 3; step += 1) {
            await card.getByRole('button', { name: 'Increase unit count' }).click()
          }
        }
      }

      const failureCard = page.getByRole('article', { name: new RegExp(`^Scan ${failureBarcode}(,|$)`) })
      await page.route('**/api/scans/*/commit', async (route) => {
        if (route.request().method() !== 'POST') {
          await route.continue()
          return
        }
        await route.fulfill({
          status: 409,
          contentType: 'application/json',
          body: JSON.stringify({ error: failureNote, code: 'none_on_hand' }),
        })
      })
      await failureCard.getByRole('button', { name: 'Confirm', exact: true }).click()
      await expect.poll(async () => (await failureCard.getAttribute('class')) ?? '').toMatch(/scan-entry-card--nothing/)
      await page.unroute('**/api/scans/*/commit')

      const queue = page.locator('.scan-page-queue')
      await expect.poll(() => queue.evaluate((element) => element.scrollHeight > element.clientHeight + 1)).toBe(true)

      const markerCard = await parkMidViewport(page, marker.barcode)
      const beforeTop = await markerTopInQueue(page, markerCard)

      await scanBarcode(page, incoming.barcode)
      await expect(page.getByRole('article', { name: `Scan ${incoming.barcode}`, exact: true })).toBeVisible()
      await expect(page.locator('.scan-page-queue article').first()).toHaveAttribute(
        'aria-label',
        `Scan ${incoming.barcode}`,
      )
      await expect.poll(async () => {
        const afterTop = await markerTopInQueue(page, markerCard)
        return Math.abs(afterTop - beforeTop)
      }).toBeLessThanOrEqual(1)
    } finally {
      await page.unroute('**/api/scans/*/commit')
      await cancelPending(request, barcodes)
    }
  })
}