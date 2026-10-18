import { expect, test, type Locator, type Page } from '@playwright/test'
import { createKnownProduct, resetToStockIn, scanBarcode, setScannerMode } from './helpers'

const partialNote = 'Only 1 was on hand, so 1 was taken out.'
const failureNote = 'Nothing on hand to take out.'

const items = Array.from({ length: 10 }, (_, index) => ({
  barcode: `2922000000${String(index).padStart(2, '0')}`,
  name: `Toast Item ${index}`,
}))

const partialBarcode = items[2].barcode

const readScroll = (page: Page) => page.evaluate(() => ({
  windowY: window.scrollY,
  queue: document.querySelector('.scan-page-queue')?.scrollTop ?? null,
}))

const raiseCount = async (page: Page, card: Locator, barcode: string) => {
  for (let step = 0; step < 3; step += 1) {
    await card.getByRole('button', { name: 'Increase unit count' }).click()
  }
  await expect(card.getByRole('textbox', { name: 'Unit count', exact: true })).toHaveValue('4')
  await expect.poll(async () => {
    const response = await page.request.get('/api/scans?userId=user-1&status=pending')
    const rows = await response.json() as { barcode: string; unitCount: number }[]
    return rows.find((row) => row.barcode === barcode)?.unitCount
  }).toBe(4)
}

const scrollLowCardIntoView = async (page: Page, barcode: string) => {
  const queue = page.locator('.scan-page-queue')
  const max = await queue.evaluate((element) => element.scrollHeight - element.clientHeight)
  expect(max).toBeGreaterThan(80)
  const card = page.getByRole('article', { name: `Scan ${barcode}`, exact: true })
  for (const hidden of [240, 160, 80, 40]) {
    await queue.evaluate((element, slack) => {
      const limit = element.scrollHeight - element.clientHeight
      element.scrollTop = Math.max(0, limit - slack)
    }, hidden)
    const box = await card.boundingBox()
    const viewport = page.viewportSize()
    if (box === null || viewport === null) continue
    const scroll = await readScroll(page)
    const inView = box.y >= 70 && box.y + box.height <= viewport.height - 8
    if (inView && (scroll.queue ?? 0) > 40 && (scroll.queue ?? 0) < max - 30) return card
  }
  throw new Error(`could not park ${barcode} in view with scroll slack`)
}

const expectTextInsideViewport = async (page: Page, text: string) => {
  const note = page.locator('.restock-toast').getByText(text, { exact: true })
  await expect(note).toBeVisible()
  await expect.poll(async () => {
    const box = await note.boundingBox()
    const viewport = page.viewportSize()
    if (box === null || viewport === null) return false
    return box.x >= 0
      && box.y >= 0
      && box.x + box.width <= viewport.width + 1
      && box.y + box.height <= viewport.height + 1
  }).toBe(true)
  const topAlerts = page.locator('.scan-page-queue > [role="alert"]').filter({ hasText: text })
  await expect(topAlerts).toHaveCount(0)
}

test('a low single confirm keeps its result in the toast and leaves the scroll alone', async ({ page, request }) => {
  test.setTimeout(120_000)
  await page.setViewportSize({ width: 390, height: 844 })
  for (const item of items) {
    await createKnownProduct(request, {
      barcode: item.barcode,
      name: item.name,
      category: 'Grocery',
      unitOfMeasure: 'carton',
    })
  }

  await page.goto('/')
  await resetToStockIn(page)
  const stocked = await scanBarcode(page, partialBarcode)
  await raiseCount(page, stocked, partialBarcode)
  await stocked.getByRole('button', { name: 'Confirm', exact: true }).click()
  await expect(stocked).toHaveCount(0)

  await setScannerMode(page, 'stock_out')
  for (const item of items) {
    const card = await scanBarcode(page, item.barcode)
    if (item.barcode !== partialBarcode) continue
    await raiseCount(page, card, item.barcode)
  }

  let mode: 'partial' | 'fail' = 'partial'
  await page.route('**/api/scans/*/commit', async (route) => {
    if (route.request().method() !== 'POST') {
      await route.continue()
      return
    }
    if (mode === 'fail') {
      await route.fulfill({
        status: 409,
        contentType: 'application/json',
        body: JSON.stringify({ error: failureNote, code: 'none_on_hand' }),
      })
      return
    }
    // Fulfill the note without committing. A real stock-out would leave a
    // derived shopping row for the shared e2e database.
    const scanId = new URL(route.request().url()).pathname.split('/').at(-2)
    const cancelled = await route.fetch({
      url: new URL(`/api/scans/${scanId}`, route.request().url()).toString(),
      method: 'PATCH',
      headers: { 'content-type': 'application/json' },
      postData: JSON.stringify({ status: 'cancelled' }),
    })
    if (!cancelled.ok()) throw new Error(`could not clear scan ${scanId}`)
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({ removedCount: 1 }),
    })
  })

  const partialCard = await scrollLowCardIntoView(page, partialBarcode)
  const beforePartial = await readScroll(page)
  await partialCard.getByRole('button', { name: 'Confirm', exact: true }).click()
  await expect(partialCard).toHaveCount(0)
  await expectTextInsideViewport(page, partialNote)
  expect(await readScroll(page)).toEqual(beforePartial)

  mode = 'fail'
  const failureBarcode = items[6].barcode
  const failureCard = await scrollLowCardIntoView(page, failureBarcode)
  const beforeFailure = await readScroll(page)
  await failureCard.getByRole('button', { name: 'Confirm', exact: true }).click()
  await expect(failureCard).toBeVisible()
  await expectTextInsideViewport(page, failureNote)
  expect(await readScroll(page)).toEqual(beforeFailure)
})
