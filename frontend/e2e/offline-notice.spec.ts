import { mkdirSync } from 'node:fs'
import { expect, test, type Page } from '@playwright/test'
import { createKnownProduct, resetToStockIn, scanBarcode } from './helpers'

const sentence = "Can't reach Stockbot. Check your connection and try again."

const shots = [
  { width: 390, height: 844, scheme: 'light' as const, action: 'confirm' as const, file: 'web_offline_confirm_390_light.png' },
  { width: 390, height: 844, scheme: 'dark' as const, action: 'confirm' as const, file: 'web_offline_confirm_390_dark.png' },
  { width: 1280, height: 800, scheme: 'light' as const, action: 'confirm' as const, file: 'web_offline_confirm_1280_light.png' },
  { width: 390, height: 844, scheme: 'light' as const, action: 'remove' as const, file: 'web_offline_remove_390_light.png' },
]

const cancelPending = async (page: Page) => {
  const pending = await page.request.get('/api/scans?userId=user-1&status=pending')
  expect(pending.ok()).toBe(true)
  const rows = await pending.json() as { id: string }[]
  for (const row of rows) {
    const cancelled = await page.request.patch(`/api/scans/${row.id}`, { data: { status: 'cancelled' } })
    expect(cancelled.ok()).toBe(true)
  }
}

for (const shot of shots) {
  test(`offline ${shot.action} at ${shot.width} ${shot.scheme}`, async ({ page, request }, testInfo) => {
    test.setTimeout(90_000)
    const shotDir = process.env.OFFLINE_SHOT_DIR ?? testInfo.outputDir
    mkdirSync(shotDir, { recursive: true })
    const barcode = `373${shot.width}${shot.scheme === 'dark' ? '1' : '0'}${shot.action === 'remove' ? '2' : '1'}`
    await createKnownProduct(request, {
      barcode,
      name: shot.action === 'remove' ? 'Offline oats' : 'Offline milk',
      category: 'Grocery',
      unitOfMeasure: 'carton',
    })
    await cancelPending(page)

    await page.emulateMedia({ colorScheme: shot.scheme })
    await page.setViewportSize({ width: shot.width, height: shot.height })
    await page.goto('/')
    await expect.poll(() => page.evaluate(() => document.documentElement.getAttribute('data-mantine-color-scheme'))).toBe(shot.scheme)
    await resetToStockIn(page)
    const card = await scanBarcode(page, barcode)

    await page.context().setOffline(true)
    await card.getByRole('button', { name: shot.action === 'remove' ? 'Remove' : 'Confirm', exact: true }).click()
    await expect(page.getByText(sentence)).toBeVisible()
    await expect(card).toBeVisible()
    await expect(page.getByText('Failed to fetch')).toHaveCount(0)
    await expect(page.getByText('Network request failed')).toHaveCount(0)
    await page.screenshot({ path: `${shotDir}/${shot.file}` })

    await page.context().setOffline(false)
    await card.getByRole('button', { name: shot.action === 'remove' ? 'Remove' : 'Confirm', exact: true }).click()
    await expect(page.getByText(sentence)).toHaveCount(0)
    await expect(card).toHaveCount(0)
    const pending = await page.request.get('/api/scans?userId=user-1&status=pending')
    const rows = await pending.json() as { barcode: string; status: string }[]
    expect(rows.some((row) => row.barcode === barcode)).toBe(false)
  })
}
