import { mkdirSync } from 'node:fs'
import { expect, test, type Page } from '@playwright/test'
import { createKnownProduct, resetToStockIn, scanBarcode, setScannerMode } from './helpers'

const shotDir = '/opt/cursor/artifacts/nothing-on-hand'
mkdirSync(shotDir, { recursive: true })

const title = 'Nothing on hand to take out.'
const skipTwo = 'Confirm skips the 2 with nothing on hand.'

const settle = async (page: Page) => {
  await expect(page.locator('.scan-entry-card--changed-animated, .scan-entry-card--just-captured')).toHaveCount(0)
  await page.evaluate(async () => {
    const pending = document.getAnimations().filter((animation) => {
      return animation.effect?.getComputedTiming().iterations !== Infinity
    })
    await Promise.all(pending.map((animation) => animation.finished.catch(() => undefined)))
  })
}

const stockIn = async (page: Page, barcode: string) => {
  const created = await page.request.post('/api/scans', {
    data: { barcode, direction: 'stock_in', userId: 'user-1' },
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
    await stockIn(page, stocked.barcode)

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
    await expect(missingCard.getByRole('button', { name: 'Remove' })).toBeVisible()
    await expect(normal.getByRole('button', { name: 'Confirm' })).toBeVisible()
    await expect(page.getByText('2 with nothing on hand', { exact: true })).toBeVisible()
    await expect(page.getByText(skipTwo)).toBeVisible()
    await expect(page.getByRole('button', { name: 'Confirm 1 selected scans' })).toBeVisible()
    await settle(page)
    await page.screenshot({
      path: `${shotDir}/queue-${shot.width}-${shot.scheme}.png`,
      fullPage: true,
    })
  })
}
