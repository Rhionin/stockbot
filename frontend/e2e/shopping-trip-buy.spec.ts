import { expect, test, type Page } from '@playwright/test'

const membersFixture = [
  {
    itemId: '664-e2e-a',
    productId: 'prod-664-e2e-a',
    name: 'GENERIC CANNED BEANS',
    netAmount: 14.5,
    netUnit: 'oz',
    onHand: 2,
    lastStockedAt: '2026-01-15T12:00:00Z',
  },
  {
    itemId: '664-e2e-b',
    productId: 'prod-664-e2e-b',
    name: 'Generic Canned Beans',
    netAmount: 8,
    netUnit: 'oz',
    onHand: 0,
    lastStockedAt: '2026-02-01T12:00:00Z',
  },
]

const shoppingListBody = JSON.stringify([
  {
    id: 'line-664-e2e',
    itemId: '664-e2e-a',
    quantity: 1,
    source: 'manual',
    purchasedAt: null,
    group: {
      id: 'grp-664-e2e',
      name: '664 E2E Group',
      rule: 'favorite',
      ruleConfirmed: true,
      members: membersFixture,
    },
  },
])

async function stubShoppingApis(page: Page, swapStatus = 200) {
  await page.route('**/api/providers', (route) => route.fulfill({
    status: 200,
    contentType: 'application/json',
    body: '[]',
  }))
  await page.route('**/api/inventory', (route) => route.fulfill({
    status: 200,
    contentType: 'application/json',
    body: '[]',
  }))
  await page.route('**/api/restock', (route) => route.fulfill({
    status: 200,
    contentType: 'application/json',
    body: 'null',
  }))
  await page.route('**/api/shopping-list**', async (route) => {
    const request = route.request()
    const url = new URL(request.url())
    if (request.method() === 'GET' && url.pathname === '/api/shopping-list') {
      await route.fulfill({ status: 200, contentType: 'application/json', body: shoppingListBody })
      return
    }
    if (request.method() === 'POST' && url.pathname === '/api/shopping-list/items/line-664-e2e/swap') {
      if (swapStatus >= 400) {
        await route.fulfill({ status: swapStatus, contentType: 'application/json', body: '{"error":"swap failed"}' })
        return
      }
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({ itemId: '664-e2e-b' }),
      })
      return
    }
    await route.continue()
  })
}

test.describe('shopping trip buy picker', () => {
  test('shows size differentiators at 390px with 44px targets', async ({ page }) => {
    await page.setViewportSize({ width: 390, height: 844 })
    await stubShoppingApis(page)
    await page.goto('/shopping')
    const group = page.getByRole('radiogroup', { name: 'This trip, buy' })
    await expect(group).toBeVisible()
    const option = page.getByRole('radio', { name: 'Generic Canned Beans, 8 oz' })
    await expect(option).toBeVisible()
    const box = await option.locator('..').boundingBox()
    expect(box).not.toBeNull()
    expect(box!.height).toBeGreaterThanOrEqual(44)
    expect(box!.width).toBeGreaterThanOrEqual(44)
  })

  test('keeps the table visible after swap and posts swap then refresh', async ({ page }) => {
    await page.setViewportSize({ width: 1280, height: 800 })
    let listLoads = 0
    await stubShoppingApis(page)
    await page.route('**/api/shopping-list', async (route) => {
      const request = route.request()
      if (request.method() === 'GET') {
        listLoads += 1
        await route.fulfill({ status: 200, contentType: 'application/json', body: shoppingListBody })
        return
      }
      await route.continue()
    })
    await page.goto('/shopping')
    await expect(page.getByRole('table', { name: 'Shopping plan entries' })).toBeVisible()
    const beforeLoads = listLoads
    await page.getByRole('radio', { name: 'Generic Canned Beans, 8 oz' }).click()
    await expect.poll(() => listLoads).toBeGreaterThan(beforeLoads)
    await expect(page.getByRole('table', { name: 'Shopping plan entries' })).toBeVisible()
    await expect(page.getByLabel('Loading shopping plan')).toBeHidden()
  })

  test('shows inline swap error copy', async ({ page }) => {
    await page.setViewportSize({ width: 390, height: 844 })
    await stubShoppingApis(page, 500)
    await page.goto('/shopping')
    await page.getByRole('radio', { name: 'Generic Canned Beans, 8 oz' }).click()
    await expect(page.locator('.trip-buy-picker__error')).toHaveText("Couldn't change what to buy. Try again.")
  })
})