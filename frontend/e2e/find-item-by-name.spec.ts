import { expect, test } from '@playwright/test'
import { resetToStockIn, setScannerMode } from './helpers'

test.describe('find item by name', () => {
  test('inventory add, stock in search, and stock-out backfill', async ({ page, request }) => {
    test.setTimeout(120_000)
    const unique = `Jam ${Date.now()}`
    const newName = `Berry ${Date.now()}`
    const searchPrefix = unique.slice(0, 6)

    await page.goto('/inventory')
    await page.getByRole('button', { name: 'Add an item' }).click()
    await page.getByLabel('Name').fill(unique)
    await page.getByLabel('Brand').fill('Pantry')
    await page.getByLabel('Category').fill('Preserves')
    await page.getByRole('button', { name: 'Add item' }).click()
    await expect(page.getByRole('dialog', { name: 'Add an item' })).toBeHidden()
    await expect(page.getByRole('heading', { name: unique })).toBeVisible()
    await expect(page.locator('.restock-toast').filter({ hasText: `Added ${unique}.` })).toBeVisible()

    const openFindItem = async () => {
      await page.getByRole('button', { name: 'Find an item' }).click()
      const dialog = page.getByRole('dialog', { name: 'Find an item' })
      await expect(dialog).toBeVisible()
      return dialog
    }

    await page.goto('/')
    await resetToStockIn(page)

    let dialog = await openFindItem()
    await dialog.getByLabel('Find an item').fill(searchPrefix)
    await expect(dialog.getByText(unique, { exact: true })).toBeVisible()
    await expect(dialog.getByText('None on hand')).toBeVisible()
    await dialog.getByText(unique, { exact: true }).click()
    const stockedToast = page.locator('.restock-toast').filter({ hasText: `Added 1 ${unique}.` })
    await expect(stockedToast).toBeVisible()
    await stockedToast.getByRole('button', { name: 'Undo' }).click()
    await expect(stockedToast).toBeHidden()

    await setScannerMode(page, 'stock_out')
    dialog = await openFindItem()
    await dialog.getByLabel('Find an item').fill(searchPrefix)
    await dialog.getByText(unique, { exact: true }).click()
    await expect(dialog.getByText('None on hand')).toBeVisible()
    await dialog.getByRole('button', { name: 'Add it and take it out' }).click()
    const backfillToast = page.locator('.restock-toast').filter({ hasText: `Added and took out 1 ${unique}.` })
    await expect(backfillToast).toBeVisible()

    await resetToStockIn(page)
    dialog = await openFindItem()
    await dialog.getByLabel('Find an item').fill(searchPrefix)
    await dialog.getByText(unique, { exact: true }).click()
    await expect(page.locator('.restock-toast').filter({ hasText: `Added 1 ${unique}.` })).toBeVisible()

    await setScannerMode(page, 'stock_out')
    dialog = await openFindItem()
    await dialog.getByLabel('Find an item').fill(searchPrefix)
    await dialog.getByText(unique, { exact: true }).click()
    await expect(page.locator('.restock-toast').filter({ hasText: `Took out 1 ${unique}.` })).toBeVisible()

    await resetToStockIn(page)
    dialog = await openFindItem()
    await dialog.getByLabel('Find an item').fill(newName)
    await expect(dialog.getByRole('button', { name: `Add "${newName}"` })).toBeVisible()
    await dialog.getByRole('button', { name: `Add "${newName}"` }).click()
    const addDialog = page.getByRole('dialog', { name: 'Add an item' })
    await addDialog.getByLabel('Name').fill(newName)
    await addDialog.getByRole('button', { name: 'Add and stock in' }).click()
    await expect(addDialog).toBeHidden()
    await expect(page.locator('.restock-toast').filter({ hasText: `Added 1 ${newName}.` })).toBeVisible()

    await setScannerMode(page, 'stock_out')
    dialog = await openFindItem()
    await dialog.getByLabel('Find an item').fill(newName.slice(0, 6))
    await dialog.getByText(newName, { exact: true }).click()
    await expect(page.locator('.restock-toast').filter({ hasText: `Took out 1 ${newName}.` })).toBeVisible()

    const search = await request.get(`/api/products/search?q=${encodeURIComponent(searchPrefix)}`)
    expect(search.ok()).toBe(true)
    const hits = await search.json() as { name: string; onHand: number }[]
    const row = hits.find((hit) => hit.name === unique)
    expect(row).toBeDefined()
    expect(row?.onHand).toBe(0)

    await resetToStockIn(page)
  })
})
