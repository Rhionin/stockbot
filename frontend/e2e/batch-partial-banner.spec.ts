import { expect, test, type APIRequestContext, type Page } from '@playwright/test'
import { createKnownProduct, resetToStockIn, scanBarcode, setScannerMode } from './helpers'

const fallback = 'Some scans could not be confirmed.'
const wasLine = 'Only 1 of 3 Tortilla chips was on hand, so 1 was taken out.'
const manyLine = '2 cards had fewer on hand than scanned, so only what was on hand was taken out.'
const failureNote = 'Nothing on hand to take out.'

const viewports = [
  { width: 390, height: 844 },
  { width: 1280, height: 800 },
] as const

interface KnownScan {
  id: string
  barcode: string
}

const cancelScan = async (request: APIRequestContext, id: string) => {
  const response = await request.patch(`/api/scans/${id}`, { data: { status: 'cancelled' } })
  expect(response.ok()).toBe(true)
}

const seedStockOuts = async (
  page: Page,
  request: APIRequestContext,
  items: { barcode: string; name: string }[],
): Promise<KnownScan[]> => {
  for (const item of items) {
    await createKnownProduct(request, {
      barcode: item.barcode,
      name: item.name,
      category: 'Grocery',
      unitOfMeasure: 'bag',
    })
  }
  await page.goto('/')
  await resetToStockIn(page)
  await setScannerMode(page, 'stock_out')
  for (const item of items) {
    await scanBarcode(page, item.barcode)
  }
  const pending = await request.get('/api/scans?userId=user-1&status=pending')
  expect(pending.ok()).toBe(true)
  const rows = await pending.json() as { id: string; barcode: string }[]
  return items.map((item) => {
    const row = rows.find((entry) => entry.barcode === item.barcode)
    if (row === undefined) throw new Error(`missing scan ${item.barcode}`)
    return { id: row.id, barcode: item.barcode }
  })
}

const confirmBatch = async (
  page: Page,
  scans: KnownScan[],
  fulfill: (ids: Map<string, string>) => Record<string, unknown>,
) => {
  const ids = new Map(scans.map((scan) => [scan.barcode, scan.id]))
  await page.route('**/api/scans/batch-commit', async (route) => {
    if (route.request().method() !== 'POST') {
      await route.continue()
      return
    }
    const body = fulfill(ids)
    const committed = Array.isArray(body.committedIds) ? body.committedIds : []
    for (const id of committed) {
      if (typeof id !== 'string') continue
      const cancelled = await route.fetch({
        url: new URL(`/api/scans/${id}`, route.request().url()).toString(),
        method: 'PATCH',
        headers: { 'content-type': 'application/json' },
        postData: JSON.stringify({ status: 'cancelled' }),
      })
      if (!cancelled.ok()) throw new Error(`could not clear scan ${id}`)
    }
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify(body),
    })
  })
  for (const scan of scans) {
    const card = page.getByRole('article', { name: `Scan ${scan.barcode}`, exact: true })
    await card.getByRole('checkbox', { name: 'Select scan for batch approval' }).check()
  }
  await page.getByRole('button', { name: `Confirm ${scans.length} selected scans` }).click()
}

const expectBannerFits = async (page: Page, lines: string[]) => {
  const banner = page.locator('.scan-batch-banner')
  await banner.scrollIntoViewIfNeeded()
  await expect(banner).toBeVisible()
  for (const line of lines) await expect(banner).toContainText(line)
  await expect(page.locator('.restock-toast').filter({ hasText: lines[0] ?? '' })).toHaveCount(0)
  await expect.poll(() => banner.evaluate((root, sentences) => {
    const box = root.getBoundingClientRect()
    const fits = box.width > 0
      && box.height > 0
      && box.left >= 0
      && box.right <= window.innerWidth + 0.5
      && box.top >= 0
      && box.bottom <= window.innerHeight + 0.5
      && root.scrollWidth <= root.clientWidth + 1
    if (!fits) return false
    return sentences.every((sentence) => {
      const node = [...root.querySelectorAll('div')].find((element) => element.textContent === sentence)
      if (node === undefined) return false
      const rect = node.getBoundingClientRect()
      return rect.width > 0
        && rect.height > 0
        && rect.left >= 0
        && rect.right <= window.innerWidth + 0.5
        && rect.top >= box.top - 1
        && rect.bottom <= window.innerHeight + 0.5
    })
  }, lines)).toBe(true)
}

for (const viewport of viewports) {
  test(`the batch banner fits the partial line at ${viewport.width}px`, async ({ page, request }) => {
    test.setTimeout(120_000)
    await page.setViewportSize(viewport)
    const chipsBarcode = `301${viewport.width}01`
    const missBarcode = `301${viewport.width}02`
    const scans = await seedStockOuts(page, request, [
      { barcode: chipsBarcode, name: 'Tortilla chips' },
      { barcode: missBarcode, name: `Short ${viewport.width}` },
    ])
    const chips = scans[0]
    const miss = scans[1]
    if (chips === undefined || miss === undefined) throw new Error('missing seeded scans')
    try {
      await page.route('**/api/scans/batch-commit', async (route) => {
        if (route.request().method() !== 'POST') {
          await route.continue()
          return
        }
        const cancelled = await route.fetch({
          url: new URL(`/api/scans/${chips.id}`, route.request().url()).toString(),
          method: 'PATCH',
          headers: { 'content-type': 'application/json' },
          postData: JSON.stringify({ status: 'cancelled' }),
        })
        if (!cancelled.ok()) throw new Error(`could not clear scan ${chips.id}`)
        await route.fulfill({
          status: 200,
          contentType: 'application/json',
          body: JSON.stringify({
            updatedCount: 2,
            committedIds: [chips.id],
            failedIds: [miss.id],
            failedReasons: [failureNote],
            failedCodes: ['none_on_hand'],
            stockOutCounts: {
              [chips.id]: { removedCount: 1, requestedCount: 3 },
            },
          }),
        })
      })
      for (const scan of scans) {
        const card = page.getByRole('article', { name: `Scan ${scan.barcode}`, exact: true })
        await card.getByRole('checkbox', { name: 'Select scan for batch approval' }).check()
      }
      await page.getByRole('button', { name: 'Confirm 2 selected scans' }).click()
      await expectBannerFits(page, [fallback, wasLine])
      await expect(page.getByRole('article', { name: `Scan ${missBarcode}`, exact: true })).toContainText(failureNote)
    } finally {
      await page.unroute('**/api/scans/batch-commit')
      await cancelScan(request, miss.id).catch(() => undefined)
    }
  })
}

const shotDir = process.env.BATCH_BANNER_SHOTS

test.describe('batch banner screenshots', () => {
  test.skip(shotDir === undefined, 'set BATCH_BANNER_SHOTS to save viewport shots')

  test('saves one partial, multiple partial, and none at both widths and schemes', async ({ page, request }) => {
    test.setTimeout(300_000)
    const schemes = ['light', 'dark'] as const
    let serial = 0
    for (const viewport of viewports) {
      for (const scheme of schemes) {
        serial += 1
        await page.emulateMedia({ colorScheme: scheme })
        await page.setViewportSize(viewport)
        const tag = `${viewport.width}${scheme}${serial}`

        const one = await seedStockOuts(page, request, [
          { barcode: `311${tag}`, name: 'Tortilla chips' },
        ])
        const oneScan = one[0]
        if (oneScan === undefined) throw new Error('missing one-partial scan')
        await confirmBatch(page, one, (ids) => ({
          updatedCount: 1,
          committedIds: [ids.get(oneScan.barcode)],
          stockOutCounts: {
            [oneScan.id]: { removedCount: 1, requestedCount: 3 },
          },
        }))
        await expectBannerFits(page, [wasLine])
        await page.screenshot({ path: `${shotDir}/banner_one_partial_${viewport.width}_${scheme}.png` })
        await page.unroute('**/api/scans/batch-commit')

        const manyA = `312${tag}a`
        const manyB = `312${tag}b`
        const many = await seedStockOuts(page, request, [
          { barcode: manyA, name: 'Tortilla chips' },
          { barcode: manyB, name: 'Rolled oats' },
        ])
        const left = many[0]
        const right = many[1]
        if (left === undefined || right === undefined) throw new Error('missing multi scans')
        await confirmBatch(page, many, () => ({
          updatedCount: 2,
          committedIds: [left.id, right.id],
          stockOutCounts: {
            [left.id]: { removedCount: 1, requestedCount: 3 },
            [right.id]: { removedCount: 2, requestedCount: 4 },
          },
        }))
        await expectBannerFits(page, [manyLine])
        await expect(page.getByText(wasLine)).toHaveCount(0)
        await page.screenshot({ path: `${shotDir}/banner_multiple_partial_${viewport.width}_${scheme}.png` })
        await page.unroute('**/api/scans/batch-commit')

        const fullBarcode = `313${tag}a`
        const noneBarcode = `313${tag}b`
        const none = await seedStockOuts(page, request, [
          { barcode: fullBarcode, name: 'Batch Full' },
          { barcode: noneBarcode, name: 'Batch Empty' },
        ])
        const full = none[0]
        const empty = none[1]
        if (full === undefined || empty === undefined) throw new Error('missing none scans')
        await confirmBatch(page, none, () => ({
          updatedCount: 2,
          committedIds: [full.id],
          failedIds: [empty.id],
          failedReasons: [failureNote],
          failedCodes: ['none_on_hand'],
          stockOutCounts: {
            [full.id]: { removedCount: 2, requestedCount: 2 },
          },
        }))
        await expectBannerFits(page, [fallback])
        await expect(page.getByText(wasLine)).toHaveCount(0)
        await expect(page.getByText(manyLine)).toHaveCount(0)
        await page.screenshot({ path: `${shotDir}/banner_none_${viewport.width}_${scheme}.png` })
        await page.unroute('**/api/scans/batch-commit')
        await cancelScan(request, empty.id)
      }
    }
  })
})
