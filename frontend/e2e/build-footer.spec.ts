import { expect, test, type Locator, type Page } from '@playwright/test'
import { commitSelectedScan, createKnownProduct, inventoryRow, resetToStockIn, scanBarcode } from './helpers'

interface Box {
  x: number
  y: number
  width: number
  height: number
}

const requireBox = async (locator: Locator): Promise<Box> => {
  const box = await locator.boundingBox()
  expect(box).not.toBeNull()
  return box!
}

// Diagnostics and the store connection live in the menu. The build id sits in
// that menu's Build section, not in the action list. The default page has no
// footer strip, so main runs to the bottom of the viewport.
const expectNoFooterBar = async (page: Page, viewportHeight: number) => {
  await expect(page.getByRole('contentinfo')).toHaveCount(0)
  const mainBox = await requireBox(page.getByRole('main'))
  expect(mainBox.y + mainBox.height).toBeGreaterThanOrEqual(viewportHeight - 1)
}

test('phone pages give the footer strip back to the page', async ({ page }) => {
  const viewport = { width: 390, height: 844 }
  await page.setViewportSize(viewport)

  for (const path of ['/', '/inventory', '/shopping']) {
    await page.goto(path)
    await expectNoFooterBar(page, viewport.height)
  }
})

test('the menu keeps the build id and diagnostics reachable', async ({ page }) => {
  const viewport = { width: 390, height: 844 }
  await page.setViewportSize(viewport)
  await page.goto('/shopping')
  await expectNoFooterBar(page, viewport.height)

  await page.getByRole('button', { name: 'Menu' }).click()
  await expect(page.getByRole('menuitem', { name: 'Diagnostics' })).toBeVisible()
  await expect(page.getByText('Build', { exact: true })).toBeVisible()
  const note = page.getByRole('note')
  await expect(note).toBeVisible()
  await expect(note).toContainText('build')
  await expect(page.getByRole('menuitem', { name: /build/i })).toHaveCount(0)

  const noteBox = await requireBox(note)
  expect(noteBox.x).toBeGreaterThanOrEqual(-1)
  expect(noteBox.x + noteBox.width).toBeLessThanOrEqual(viewport.width + 1)
})

test('inventory cards can use the bottom of the screen', async ({ page, request }) => {
  const viewport = { width: 390, height: 700 }
  await page.setViewportSize(viewport)

  const items: Array<[string, string]> = [
    ['210000000101', 'Footer Check Beans'],
    ['210000000102', 'Footer Check Rice'],
    ['210000000103', 'Footer Check Oats'],
    ['210000000104', 'Footer Check Pasta'],
  ]
  for (const [barcode, name] of items) {
    await createKnownProduct(request, {
      barcode,
      name,
      category: 'Dry goods',
      unitOfMeasure: 'bag',
    })
  }

  await page.goto('/')
  await resetToStockIn(page)
  for (const [barcode] of items) {
    await commitSelectedScan(page, await scanBarcode(page, barcode))
  }

  await page.getByRole('link', { name: 'Inventory' }).click()
  await expectNoFooterBar(page, viewport.height)

  const main = page.getByRole('main')
  await main.evaluate((element) => {
    element.scrollTop = element.scrollHeight
  })

  for (const [, name] of items) {
    const row = inventoryRow(page, name)
    await row.scrollIntoViewIfNeeded()
    const rowBox = await requireBox(row)
    expect(rowBox.y + rowBox.height).toBeLessThanOrEqual(viewport.height + 1)
  }
})
