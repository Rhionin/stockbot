import { mkdirSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { expect, test, type Page } from '@playwright/test'
import { createKnownProduct, resetToStockIn, scanBarcode, setScannerMode } from './helpers'

const shotDir = process.env.NOTHING_ON_HAND_SHOTS ?? join(tmpdir(), 'nothing-on-hand')
const reviewDir = process.env.REVIEW_SHOTS
mkdirSync(shotDir, { recursive: true })
if (reviewDir !== undefined) mkdirSync(reviewDir, { recursive: true })

const keepReview = (slug: 'offer' | 'banner' | 'toast', width: number, scheme: string): boolean => {
  if (scheme === 'dark') return slug === 'offer' && width === 390
  if (slug === 'offer') return width === 390 || width === 1280
  return width === 390 || width === 1280
}

const title = 'Nothing on hand to take out.'
const skipTwo = 'Confirm skips the 2 with nothing on hand.'
const bodyOne = 'Stockbot thinks you have none. If you had one anyway, this adds it and takes it out in one step.'
const banner = '1 taken out. 2 with nothing on hand are still in the queue. Each one has its own Add it and take it out button.'
const added = 'Added and taken out.'

const settle = async (page: Page) => {
  await expect(page.locator('.scan-entry-card--changed-animated, .scan-entry-card--just-captured')).toHaveCount(0)
  await page.evaluate(async () => {
    const pending = document.getAnimations().filter((animation) => {
      return animation.effect?.getComputedTiming().iterations !== Infinity
    })
    await Promise.all(pending.map((animation) => animation.finished.catch(() => undefined)))
  })
}

const commitScan = async (page: Page, barcode: string, direction: 'stock_in' | 'stock_out') => {
  const created = await page.request.post('/api/scans', {
    data: { barcode, direction, userId: 'user-1' },
  })
  expect(created.ok(), await created.text()).toBe(true)
  const body = await created.json() as { id: string }
  const committed = await page.request.post(`/api/scans/${body.id}/commit`, { data: {} })
  expect(committed.ok(), await committed.text()).toBe(true)
}

const shots = [
  { width: 390, height: 844, scheme: 'light' },
  { width: 1280, height: 800, scheme: 'light' },
  { width: 390, height: 844, scheme: 'dark' },
  { width: 1280, height: 800, scheme: 'dark' },
] as const

for (const shot of shots) {
  test(`nothing on hand queue at ${shot.width} ${shot.scheme}`, async ({ page, request }) => {
    test.setTimeout(90_000)
    const suffix = `${shot.width}${shot.scheme === 'dark' ? '1' : '0'}`
    const stocked = { barcode: `324${suffix}01`, name: 'Stocked oats' }
    const missing = { barcode: `324${suffix}02`, name: 'Missing tea' }
    const empty = { barcode: `324${suffix}03`, name: 'Empty rice' }
    for (const item of [stocked, missing, empty]) {
      await createKnownProduct(request, {
        barcode: item.barcode,
        name: item.name,
        category: 'Grocery',
        unitOfMeasure: 'bag',
      })
    }
    const pending = await request.get('/api/scans?userId=user-1&status=pending')
    expect(pending.ok()).toBe(true)
    const rows = await pending.json() as { id: string }[]
    for (const row of rows) {
      const cancelled = await request.patch(`/api/scans/${row.id}`, { data: { status: 'cancelled' } })
      expect(cancelled.ok()).toBe(true)
    }
    await commitScan(page, stocked.barcode, 'stock_in')
    for (const item of [missing, empty]) {
      await commitScan(page, item.barcode, 'stock_in')
      await commitScan(page, item.barcode, 'stock_out')
    }

    await page.emulateMedia({ colorScheme: shot.scheme })
    await page.setViewportSize({ width: shot.width, height: shot.height })
    await page.goto('/')
    await expect.poll(() => page.evaluate(() => document.documentElement.getAttribute('data-mantine-color-scheme'))).toBe(shot.scheme)
    await resetToStockIn(page)
    await setScannerMode(page, 'stock_out')
    await scanBarcode(page, missing.barcode)
    await scanBarcode(page, empty.barcode)
    const normal = await scanBarcode(page, stocked.barcode)
    await normal.getByRole('checkbox', { name: 'Select scan for batch approval' }).check()

    const missingCard = page.getByRole('article', { name: `Scan ${missing.barcode}, ${title}` })
    const emptyCard = page.getByRole('article', { name: `Scan ${empty.barcode}, ${title}` })
    await expect(missingCard).toBeVisible()
    await expect(emptyCard).toBeVisible()
    await expect(missingCard.getByRole('button', { name: 'Confirm' })).toHaveCount(0)
    await expect(missingCard.getByRole('button', { name: 'Add it and take it out' })).toBeVisible()
    await expect(missingCard.getByText(bodyOne)).toBeVisible()
    await expect(missingCard.getByRole('button', { name: 'Increase unit count' })).toBeEnabled()
    await expect(missingCard.getByRole('button', { name: 'Decrease unit count' })).toBeDisabled()
    await expect(missingCard.getByRole('button', { name: 'Remove' })).toHaveCount(0)
    const offerBox = await missingCard.getByRole('button', { name: 'Add it and take it out' }).boundingBox()
    const cardBox = await missingCard.boundingBox()
    expect(offerBox && cardBox && offerBox.width > cardBox.width * 0.55).toBeTruthy()
    await expect(normal.getByRole('button', { name: 'Confirm' })).toBeVisible()
    await expect(page.getByText('2 with nothing on hand', { exact: true })).toBeVisible()
    await expect(page.getByText(skipTwo)).toBeVisible()
    await expect(page.getByRole('button', { name: 'Confirm 1 selected scans' })).toBeVisible()
    await settle(page)
    await page.screenshot({
      path: `${shotDir}/queue-${shot.width}-${shot.scheme}.png`,
      fullPage: true,
    })
    if (reviewDir !== undefined && keepReview('offer', shot.width, shot.scheme)) {
      await page.screenshot({
        path: join(reviewDir, `offer-${shot.width}-${shot.scheme}.png`),
        fullPage: true,
      })
    }

    if (shot.width === 390) {
      await page.locator('.scan-page-queue').evaluate((node) => {
        const queue = node as HTMLElement
        const filler = document.createElement('div')
        filler.setAttribute('data-scroll-spacer', 'true')
        filler.style.height = '1600px'
        queue.append(filler)
        queue.scrollTop = queue.scrollHeight
      })
    }
    await page.getByRole('button', { name: 'Confirm 1 selected scans' }).click()
    await expect(page.locator('.scan-nothing-skipped')).toHaveText(banner)
    if (shot.width === 390) {
      await expect(page.locator('.scan-nothing-skipped')).toBeInViewport()
      const toolbar = await page.locator('.scan-page-tools').boundingBox()
      const bannerBox = await page.locator('.scan-nothing-skipped').boundingBox()
      expect(toolbar !== null && bannerBox !== null && bannerBox.y >= toolbar.y + toolbar.height - 1).toBe(true)
      await page.locator('[data-scroll-spacer]').evaluate((node) => node.remove())
    }
    await expect(missingCard.getByRole('button', { name: 'Add it and take it out' })).toBeVisible()
    await settle(page)
    await page.screenshot({
      path: `${shotDir}/banner-${shot.width}-${shot.scheme}.png`,
      fullPage: true,
    })
    if (reviewDir !== undefined && keepReview('banner', shot.width, shot.scheme)) {
      await page.screenshot({
        path: join(reviewDir, `banner-${shot.width}-${shot.scheme}.png`),
        fullPage: true,
      })
    }

    await missingCard.getByRole('button', { name: 'Add it and take it out' }).click()
    await expect(page.locator('.restock-toast').filter({ hasText: added })).toBeVisible()
    await settle(page)
    await page.screenshot({
      path: `${shotDir}/toast-${shot.width}-${shot.scheme}.png`,
      fullPage: true,
    })
    if (reviewDir !== undefined && keepReview('toast', shot.width, shot.scheme)) {
      await page.screenshot({
        path: join(reviewDir, `toast-${shot.width}-${shot.scheme}.png`),
        fullPage: true,
      })
    }
  })
}
