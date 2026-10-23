import { expect, test, type Locator, type Page } from '@playwright/test'
import { createKnownProduct, resetToStockIn, scanBarcode, setScannerMode } from './helpers'

const partialNote = 'Only 1 was on hand, so 1 was taken out.'
const failureNote = 'Nothing on hand to take out.'

const viewports = [
  { width: 390, height: 844 },
  { width: 1280, height: 800 },
] as const

const itemsFor = (width: number) => Array.from({ length: 12 }, (_, index) => ({
  barcode: `293${width}${String(index).padStart(4, '0')}`,
  name: `Toast ${width} ${index}`,
}))

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
  const card = page.getByRole('article', { name: new RegExp(`^Scan ${barcode}(,|$)`) })
  const offsets = Array.from({ length: 25 }, (_, step) => Math.round((max * step) / 24))
  for (const hidden of offsets) {
    await queue.evaluate((element, slack) => {
      const limit = element.scrollHeight - element.clientHeight
      element.scrollTop = Math.max(0, limit - slack)
    }, hidden)
    const box = await card.boundingBox()
    const viewport = page.viewportSize()
    if (box === null || viewport === null) continue
    const scroll = await readScroll(page)
    const inView = box.y >= 70 && box.y + box.height <= viewport.height - 8
    const slack = max - (scroll.queue ?? 0)
    // Keep the card in the lower half, with enough list below it that removing
    // the row does not clamp scrollTop or make the browser anchor upward.
    const inLowerHalf = box.y + box.height >= viewport.height * 0.55
    if (inView && inLowerHalf && (scroll.queue ?? 0) > 40 && slack >= box.height) return card
  }
  throw new Error(`could not park ${barcode} in view with scroll slack`)
}

const expectToastInside = async (page: Page, text: string, borderToken?: string) => {
  const toast = page.locator('.restock-toast').filter({ hasText: text })
  await expect(toast).toBeVisible()
  await expect.poll(() => page.evaluate(({ sentence, token }) => {
    const leaves = [...document.querySelectorAll('body *')].filter((node) =>
      node.childElementCount === 0 && node.textContent === sentence,
    )
    if (leaves.length !== 1) return false
    const root = leaves[0]?.closest('.restock-toast')
    if (!(root instanceof HTMLElement)) return false
    const box = root.getBoundingClientRect()
    const fits = box.left >= 0
      && box.right <= window.innerWidth
      && box.bottom <= window.innerHeight
      && box.top >= 0
      && box.width > 0
    if (!fits) return false
    // Side margins, and every control inside the toast (Undo, close) on screen.
    if (box.left < 8 || box.right > window.innerWidth - 8) return false
    if (token !== undefined) {
      const probe = document.createElement('span')
      probe.style.borderLeftColor = `var(${token})`
      document.body.appendChild(probe)
      const expected = getComputedStyle(probe).borderLeftColor
      probe.remove()
      if (getComputedStyle(root).borderLeftColor !== expected) return false
    }
    return [...root.querySelectorAll('button, .restock-toast-detail')].every((node) => {
      const rect = node.getBoundingClientRect()
      if (rect.width === 0 && rect.height === 0) return true
      return rect.left >= 0
        && rect.right <= window.innerWidth
        && rect.bottom <= window.innerHeight
        && rect.top >= 0
    })
  }, { sentence: text, token: borderToken })).toBe(true)
}

const expectQueueClearsToast = async (page: Page) => {
  const queue = page.locator('.scan-page-queue')
  await queue.evaluate((element) => {
    element.scrollTop = element.scrollHeight
  })
  await expect.poll(() => page.evaluate(() => {
    const list = document.querySelector('.scan-page-queue')
    const toast = document.querySelector('.restock-toast')
    const cards = list?.querySelectorAll('article')
    const card = cards === undefined || cards.length === 0 ? undefined : cards[cards.length - 1]
    if (list === null || list === undefined || toast === null || card === undefined) return false
    const cardBox = card.getBoundingClientRect()
    const toastBox = toast.getBoundingClientRect()
    return cardBox.bottom <= toastBox.top + 1
  })).toBe(true)
}

for (const viewport of viewports) {
test(`a low single confirm at ${viewport.width}px keeps its result in the toast and leaves the scroll alone`, async ({ page, request }) => {
  test.setTimeout(120_000)
  const items = itemsFor(viewport.width)
  const partialBarcode = items[2].barcode
  await page.setViewportSize(viewport)
  for (const item of items) {
    await createKnownProduct(request, {
      barcode: item.barcode,
      name: item.name,
      category: 'Grocery',
      unitOfMeasure: 'carton',
    })
  }

  for (const item of items) {
    if (item.barcode === partialBarcode) continue
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
      body: JSON.stringify({ removedCount: 1, requestedCount: 4 }),
    })
  })

  const partialCard = await scrollLowCardIntoView(page, partialBarcode)
  const beforePartial = await readScroll(page)
  await partialCard.getByRole('button', { name: 'Confirm', exact: true }).click()
  await expect(partialCard).toHaveCount(0)
  await expectToastInside(page, partialNote)
  expect(await readScroll(page)).toEqual(beforePartial)
  await expectQueueClearsToast(page)

  mode = 'fail'
  const failureBarcode = items[6].barcode
  const failureCard = await scrollLowCardIntoView(page, failureBarcode)
  const beforeFailure = await readScroll(page)
  await failureCard.getByRole('button', { name: 'Confirm', exact: true }).click()
  await expect(failureCard).toBeVisible()
  await expect(failureCard).toHaveClass(/scan-entry-card--nothing/)
  await expect(failureCard.getByText(failureNote, { exact: true })).toBeVisible()
  await expect(page.locator('.restock-toast').filter({ hasText: failureNote })).toHaveCount(0)
  expect(await readScroll(page)).toEqual(beforeFailure)
})
}
