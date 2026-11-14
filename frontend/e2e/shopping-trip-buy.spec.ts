import { expect, test, type Page } from '@playwright/test'
import { TRIP_BUY_SAME_NAME_SENTENCE, TRIP_BUY_SWAP_ERROR } from '../src/components/shopping/tripBuyCopy'

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

const fallbackMembers = [
  {
    itemId: '664-fb-a',
    productId: 'prod-664-fb-a',
    name: 'DUPLICATE PANTRY ITEM',
    onHand: 0,
  },
  {
    itemId: '664-fb-b',
    productId: 'prod-664-fb-b',
    name: 'duplicate pantry item',
    onHand: 0,
  },
]

function listBody(itemId: string, members: typeof membersFixture | typeof fallbackMembers) {
  return JSON.stringify([
    {
      id: 'line-664-e2e',
      itemId,
      quantity: 1,
      source: 'manual',
      purchasedAt: null,
      group: {
        id: 'grp-664-e2e',
        name: '664 E2E Group',
        rule: 'favorite',
        ruleConfirmed: true,
        members,
      },
    },
  ])
}

type ShoppingStubState = {
  listLoads: number
  selectedItemId: string
  swapPosts: number
}

async function stubShoppingApis(
  page: Page,
  options: {
    swapStatus?: number
    swapDelayMs?: number
    members?: typeof membersFixture | typeof fallbackMembers
  } = {},
): Promise<ShoppingStubState> {
  const {
    swapStatus = 200,
    swapDelayMs = 0,
    members = membersFixture,
  } = options
  const state: ShoppingStubState = {
    listLoads: 0,
    selectedItemId: members === fallbackMembers ? '664-fb-a' : '664-e2e-a',
    swapPosts: 0,
  }

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
      state.listLoads += 1
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: listBody(state.selectedItemId, members),
      })
      return
    }
    if (request.method() === 'POST' && url.pathname === '/api/shopping-list/items/line-664-e2e/swap') {
      state.swapPosts += 1
      if (swapStatus >= 400) {
        await route.fulfill({ status: swapStatus, contentType: 'application/json', body: '{"error":"swap failed"}' })
        return
      }
      await new Promise((resolve) => setTimeout(resolve, swapDelayMs))
      state.selectedItemId = members === fallbackMembers ? '664-fb-b' : '664-e2e-b'
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({ itemId: state.selectedItemId }),
      })
      return
    }
    await route.continue()
  })

  return state
}

test.describe('shopping trip buy picker', () => {
  test('styles no-image tiles at 48×48 with bin tokens', async ({ page }) => {
    await page.setViewportSize({ width: 390, height: 844 })
    await stubShoppingApis(page)
    await page.goto('/shopping')
    await expect(page.getByRole('group', { name: 'This trip, buy' })).toBeVisible()
    const tile = page.locator('.trip-buy-picker .shelf-photo-fallback').first()
    await expect(tile).toBeVisible()
    const styles = await tile.evaluate((el) => {
      const computed = getComputedStyle(el)
      return {
        backgroundColor: computed.backgroundColor,
        borderTopWidth: computed.borderTopWidth,
      }
    })
    expect(styles.backgroundColor).toBe('rgb(241, 243, 245)')
    expect(styles.borderTopWidth).toBe('1px')
    const photo = page.locator('.trip-buy-picker .shelf-photo').first()
    const box = await photo.evaluate((el) => {
      const style = getComputedStyle(el)
      const rect = el.getBoundingClientRect()
      const rootPx = parseFloat(getComputedStyle(document.documentElement).fontSize)
      return {
        cssWidth: style.width,
        cssHeight: style.height,
        width: rect.width,
        height: rect.height,
        rootPx,
      }
    })
    expect(box.cssWidth).toBe(`${box.rootPx * 3}px`)
    expect(box.cssHeight).toBe(`${box.rootPx * 3}px`)
    expect(box.width).toBeCloseTo(box.rootPx * 3, 0)
    expect(box.height).toBeCloseTo(box.rootPx * 3, 0)
  })

  test('shows size differentiators at 390px with 44px targets', async ({ page }) => {
    await page.setViewportSize({ width: 390, height: 844 })
    await stubShoppingApis(page)
    await page.goto('/shopping')
    const group = page.getByRole('group', { name: 'This trip, buy' })
    await expect(group).toBeVisible()
    const option = page.getByRole('radio', { name: 'Generic Canned Beans, 8 oz' })
    await expect(option).toBeVisible()
    const box = await option.locator('..').boundingBox()
    expect(box).not.toBeNull()
    expect(box!.height).toBeGreaterThanOrEqual(44)
    expect(box!.width).toBeGreaterThanOrEqual(44)
  })

  test('keeps focus on the chosen option after save settles', async ({ page }) => {
    await page.setViewportSize({ width: 390, height: 844 })
    await stubShoppingApis(page, { swapDelayMs: 300 })
    await page.goto('/shopping')
    const target = page.getByRole('radio', { name: 'Generic Canned Beans, 8 oz' })
    await target.focus()
    await page.keyboard.press('Space')
    await expect(target).toBeChecked({ timeout: 15_000 })
    await expect(target).toBeFocused()
  })

  test('arrow keys move focus without swapping; Enter saves once', async ({ page }) => {
    const state = await stubShoppingApis(page)
    await page.setViewportSize({ width: 1280, height: 800 })
    await page.goto('/shopping')
    const first = page.getByRole('radio', { name: 'Generic Canned Beans, 14.5 oz' })
    const second = page.getByRole('radio', { name: 'Generic Canned Beans, 8 oz' })
    await first.focus()
    await page.keyboard.press('ArrowRight')
    await expect(second).toBeFocused()
    expect(state.swapPosts).toBe(0)
    await page.keyboard.press('Enter')
    await expect.poll(() => state.swapPosts).toBe(1)
    await expect(second).toBeChecked()
  })

  test('does not clip the fallback sentence at narrow widths', async ({ page }) => {
    for (const width of [320, 390, 1280]) {
      await page.setViewportSize({ width, height: width === 1280 ? 800 : 844 })
      await stubShoppingApis(page, { members: fallbackMembers })
      await page.goto('/shopping')
      const diff = page.locator('.trip-buy-picker__diff').first()
      await expect(diff).toHaveText(TRIP_BUY_SAME_NAME_SENTENCE)
      const metrics = await diff.evaluate((el) => ({
        scrollWidth: el.scrollWidth,
        clientWidth: el.clientWidth,
        text: el.textContent,
      }))
      expect(metrics.text).toBe(TRIP_BUY_SAME_NAME_SENTENCE)
      expect(metrics.scrollWidth).toBeLessThanOrEqual(metrics.clientWidth + 1)
      const pageWidth = await page.evaluate(() => ({
        scrollWidth: document.documentElement.scrollWidth,
        innerWidth: window.innerWidth,
      }))
      expect(pageWidth.scrollWidth).toBeLessThanOrEqual(pageWidth.innerWidth + 1)
    }
  })

  test('keeps the table visible after swap and posts swap then refresh', async ({ page }) => {
    const state = await stubShoppingApis(page)
    await page.setViewportSize({ width: 1280, height: 800 })
    await page.goto('/shopping')
    await expect(page.getByRole('table', { name: 'Shopping plan entries' })).toBeVisible()
    const beforeLoads = state.listLoads
    await page.getByRole('radio', { name: 'Generic Canned Beans, 8 oz' }).click()
    await expect.poll(() => state.listLoads).toBeGreaterThan(beforeLoads)
    await expect(page.getByRole('radio', { name: 'Generic Canned Beans, 8 oz' })).toBeChecked()
    await expect(page.getByRole('table', { name: 'Shopping plan entries' })).toBeVisible()
    await expect(page.getByLabel('Loading shopping plan')).toBeHidden()
  })

  test('shows inline swap error copy without refreshing the list', async ({ page }) => {
    const state = await stubShoppingApis(page, { swapStatus: 500 })
    await page.setViewportSize({ width: 390, height: 844 })
    await page.goto('/shopping')
    await expect(page.getByRole('group', { name: 'This trip, buy' })).toBeVisible()
    const before = state.listLoads
    await page.getByRole('radio', { name: 'Generic Canned Beans, 8 oz' }).click()
    await expect(page.locator('.trip-buy-picker__error')).toHaveText(TRIP_BUY_SWAP_ERROR)
    expect(state.listLoads).toBe(before)
    await expect(page.getByRole('radio', { name: 'Generic Canned Beans, 14.5 oz' })).toBeChecked()
  })
})