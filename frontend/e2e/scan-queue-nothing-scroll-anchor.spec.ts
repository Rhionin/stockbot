import { expect, test, type APIRequestContext, type Locator } from '@playwright/test'
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

const itemsFor = (width: number, repeat: number) => Array.from({ length: 12 }, (_, index) => ({
  barcode: `296${width}${repeat}${String(index).padStart(2, '0')}`,
  name: `Nothing anchor ${width} r${repeat} ${index}`,
}))

const parkFirstFullyVisibleWithBelow = async (page: Page, barcode: string) => {
  const queue = page.locator('.scan-page-queue')
  const card = page.getByRole('article', { name: new RegExp(`^Scan ${barcode}(,|$)`) })
  const max = await queue.evaluate((element) => element.scrollHeight - element.clientHeight)
  expect(max).toBeGreaterThan(100)

  for (let step = 0; step <= 50; step += 1) {
    const scrollTop = Math.round((max * step) / 50)
    await queue.evaluate((element, top) => {
      element.scrollTop = top
    }, scrollTop)

    const pose = await page.evaluate(({ target }) => {
      const queue = document.querySelector('.scan-page-queue')
      if (queue === null) return { ok: false }
      const bounds = queue.getBoundingClientRect()
      const articles = [...queue.querySelectorAll('article')].filter((node) => {
        const box = node.getBoundingClientRect()
        return box.height > 0 && box.width > 0
      })
      const index = articles.findIndex((node) => {
        const label = node.getAttribute('aria-label') ?? ''
        return label === `Scan ${target}` || label.startsWith(`Scan ${target},`)
      })
      if (index < 0) return { ok: false }
      const topCard = articles[index]
      const topBox = topCard.getBoundingClientRect()
      const fullyVisible = topBox.top >= bounds.top - 0.5 && topBox.bottom <= bounds.bottom + 0.5
      const firstIndex = articles.findIndex((node) => {
        const box = node.getBoundingClientRect()
        return box.top >= bounds.top - 0.5 && box.bottom <= bounds.bottom + 0.5
      })
      const below = articles[index + 1]
      const hasBelow = below !== undefined && below.getBoundingClientRect().bottom > topBox.bottom + 2
      return {
        ok: fullyVisible && firstIndex === index && hasBelow,
      }
    }, { target: barcode })

    if (pose.ok) return card
  }
  throw new Error(`could not park ${barcode} as the first fully visible row with rows below`)
}

const controlsInsideScrollport = async (
  card: Locator,
  message: string,
  maxShiftPx: number,
  baselineTop: number,
) => {
  await expect.poll(async () => card.evaluate((root, { msg, maxShift, baseTop }) => {
    const queue = root.closest('.scan-page-queue')
    if (!(queue instanceof HTMLElement)) return { ready: false as const, reason: 'no queue' }
    const q = queue.getBoundingClientRect()
    const messageEl = [...root.querySelectorAll('*')].find((node) =>
      node.childElementCount === 0 && node.textContent === msg) as HTMLElement | undefined
    const control = [...root.querySelectorAll('button')].find((btn) =>
      btn.textContent === 'Add it and take it out' || btn.textContent === 'Confirm') as HTMLElement | undefined
    if (messageEl === undefined || control === undefined) {
      return { ready: false as const, reason: 'missing nodes' }
    }
    const inPort = (box: DOMRect) => box.top >= q.top - 0.5
      && box.bottom <= q.bottom + 0.5
      && box.left >= q.left - 0.5
      && box.right <= q.right + 0.5
    const m = messageEl.getBoundingClientRect()
    const c = control.getBoundingClientRect()
    if (!inPort(m) || !inPort(c)) return { ready: false as const, reason: 'outside scrollport' }
    const pulledUp = baseTop - c.top
    if (pulledUp > maxShift) return { ready: false as const, reason: `pulled up ${pulledUp}` }
    return { ready: true as const }
  }, { msg: message, maxShift: maxShiftPx, baseTop: baselineTop })).toEqual({ ready: true })
}

for (const viewport of viewports) {
  test(`nothing-on-hand growth at the top of the scrollport keeps controls in view at ${viewport.width}px`, async ({ page, request }, testInfo) => {
    test.setTimeout(120_000)
    const repeat = testInfo.repeatEachIndex
    const items = itemsFor(viewport.width, repeat)
    const failureBarcode = items[8].barcode
    const barcodes = items.map((item) => item.barcode)

    try {
      await cancelPending(request, barcodes)
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

      await page.unroute('**/api/scans/*/commit')
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

      const queue = page.locator('.scan-page-queue')
      await expect.poll(() => queue.evaluate((element) => element.scrollHeight > element.clientHeight + 1)).toBe(true)

      const failureCard = await parkFirstFullyVisibleWithBelow(page, failureBarcode)
      const confirm = failureCard.getByRole('button', { name: 'Confirm', exact: true })
      const baselineTop = await confirm.evaluate((element) => element.getBoundingClientRect().top)

      await confirm.click()
      await expect.poll(async () => (await failureCard.getAttribute('class')) ?? '').toMatch(/scan-entry-card--nothing/)
      await expect(failureCard.getByText(failureNote, { exact: true })).toBeVisible()
      await expect(failureCard.getByRole('button', { name: 'Add it and take it out' })).toBeVisible()

      await controlsInsideScrollport(failureCard, failureNote, 4, baselineTop)
    } finally {
      await page.unroute('**/api/scans/*/commit')
      await cancelPending(request, barcodes)
    }
  })
}
