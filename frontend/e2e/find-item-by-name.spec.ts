import { expect, test } from '@playwright/test'
import { resetToStockIn, setScannerMode } from './helpers'

test.describe('find item by name', () => {
  test('inventory add, stock in search, and stock-out backfill', async ({ page, request }) => {
    test.setTimeout(120_000)
    const unique = `Jam ${Date.now()}`

    await page.goto('/inventory')
    await page.getByRole('button', { name: 'Add an item' }).click()
    await page.getByLabel('Name').fill(unique)
    await page.getByLabel('Brand').fill('Pantry')
    await page.getByLabel('Category').fill('Preserves')
    await page.getByRole('button', { name: 'Add item' }).click()
    await expect(page.getByRole('dialog', { name: 'Add an item' })).toBeHidden()
    await expect(page.getByText(unique)).toBeVisible()

    await page.goto('/')
    await resetToStockIn(page)
    await page.getByRole('button', { name: 'Find an item' }).click()
    await page.getByLabel('Find an item').fill(unique.slice(0, 6))
    await expect(page.getByText(unique)).toBeVisible()
    await expect(page.getByText('0 on hand')).toBeVisible()
    await page.getByText(unique).click()
    const stockedToast = page.locator('.restock-toast').filter({ hasText: `Added 1 ${unique}.` })
    await expect(stockedToast).toBeVisible()
    await stockedToast.getByRole('button', { name: 'Undo' }).click()

    await setScannerMode(page, 'stock_out')
    await page.getByRole('button', { name: 'Find an item' }).click()
    await page.getByLabel('Find an item').fill(unique.slice(0, 6))
    await page.getByText(unique).click()
    await expect(page.getByText('None on hand')).toBeVisible()
    await page.getByRole('button', { name: 'Add it and take it out' }).click()
    const backfillToast = page.locator('.restock-toast').filter({ hasText: `Added and took out 1 ${unique}.` })
    await expect(backfillToast).toBeVisible()

    const search = await request.get(`/api/products/search?q=${encodeURIComponent(unique.slice(0, 6))}`)
    expect(search.ok()).toBe(true)
    const hits = await search.json() as { name: string; onHand: number }[]
    const row = hits.find((hit) => hit.name === unique)
    expect(row).toBeDefined()
    expect(row?.onHand).toBe(0)
  })
})
