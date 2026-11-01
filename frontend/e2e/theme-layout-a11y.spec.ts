import AxeBuilder from '@axe-core/playwright'
import { expect, test, type Page } from '@playwright/test'
import { install, resetStage, stage } from './householdStageFixtures'
import { resetSharedState } from './helpers'

const MIN_TOUCH = 44

const krogerProviders = (
  source: 'none' | 'saved',
  connectionState: 'connected' | 'disconnected' = 'connected',
  displayName = 'Kroger',
) => ([
  {
    id: 'kroger',
    displayName,
    capabilities: {
      auth: 'oauth2_authorization_code',
      delivery: 'server_push',
      confirmation: 'per_request',
      mutation: 'add_only',
      identity: 'derived',
    },
    connectionState,
    credentialsConfigured: true,
    credentials: {
      clientId: source === 'none' ? '' : 'fixture-client',
      redirectUri: source === 'none' ? '' : 'https://test-household.example/cb',
      modality: 'PICKUP',
      secretSet: source !== 'none',
      source,
    },
  },
])

async function assertTouchTargetSize(locator: ReturnType<Page['locator']>) {
  const box = await locator.boundingBox()
  expect(box).not.toBeNull()
  expect(box!.width).toBeGreaterThanOrEqual(MIN_TOUCH - 0.01)
  expect(box!.height).toBeGreaterThanOrEqual(MIN_TOUCH - 0.01)
}

async function stubShopping(page: Page, displayName: string) {
  await page.route('**/api/shopping-list**', async (route) => {
    const incoming = route.request()
    if (incoming.method() !== 'GET' || new URL(incoming.url()).pathname !== '/api/shopping-list') {
      await route.continue()
      return
    }
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify([{
        itemId: 'item-1',
        quantity: 1,
        item: { id: 'item-1', product: { name: 'Plain rice', category: 'Dry goods' } },
        basis: { instanceCount: 1 },
      }]),
    })
  })
  await page.route('**/api/restock', (route) => route.fulfill({ status: 200, contentType: 'application/json', body: 'null' }))
  await page.route('**/api/inventory', (route) => route.fulfill({ status: 200, contentType: 'application/json', body: '[]' }))
  await page.route('**/api/providers', (route) => route.fulfill({
    status: 200,
    contentType: 'application/json',
    body: JSON.stringify(krogerProviders('saved', 'connected', displayName)),
  }))
}

async function assertSingleHouseholdEllipsis(page: Page, fullName: string) {
  const button = page.getByRole('button', { name: `Household: ${fullName}` })
  await expect(button).toBeVisible()
  await expect(button).toHaveAttribute('title', fullName)
  await expect(button.evaluate((el) => {
    const style = getComputedStyle(el)
    return el.scrollWidth > el.clientWidth + 0.5
      && style.textOverflow === 'ellipsis'
      && style.whiteSpace === 'nowrap'
  })).resolves.toBe(true)
}

test.beforeAll(async ({ request }) => {
  await resetSharedState(request)
})

test('320px with 125% text keeps the single-household name on one line', async ({ page }) => {
  resetStage()
  stage.kind = 'account'
  stage.operator = false
  stage.role = 'owner'
  stage.householdName = 'Test Household Pantry Storage'
  await install(page)
  await page.route('**/api/auth/session', async (route) => {
    if (route.request().method() !== 'GET') return route.continue()
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({
        user: { id: 'user-1', email: 'ada@example.com', name: 'Ada' },
        kind: 'account',
        memberships: [{ householdId: 'hh-1', name: stage.householdName, role: 'owner', deletedAt: null }],
        households: [{ id: 'hh-1', name: stage.householdName, role: 'owner', deletedAt: null }],
        activeHouseholdId: 'hh-1',
        operator: false,
      }),
    })
  })
  await stubShopping(page, 'Kroger')
  await page.setViewportSize({ width: 320, height: 844 })
  await page.addStyleTag({ content: 'html { font-size: 125% !important; }' })
  await page.goto('/shopping')
  await assertSingleHouseholdEllipsis(page, 'Test Household Pantry Storage')
})

test('shopping toolbar labels do not clip below 360px with 125% text', async ({ page }) => {
  await stubShopping(page, "Smith's")
  await page.setViewportSize({ width: 320, height: 844 })
  await page.addStyleTag({ content: 'html { font-size: 125% !important; }' })
  await page.goto('/shopping')
  const send = page.getByRole('button', { name: "Send to Smith's" })
  const fill = page.getByRole('button', { name: /Update the list|Build the list/ })
  await expect(send).toBeVisible()
  await expect(fill).toBeVisible()
  for (const button of [send, fill]) {
    await expect(button.evaluate((el) => el.scrollWidth <= el.clientWidth + 1)).resolves.toBe(true)
    const box = await button.boundingBox()
    expect(box).not.toBeNull()
    expect(box!.x).toBeGreaterThanOrEqual(0)
    expect(box!.x + box!.width).toBeLessThanOrEqual(320.5)
  }
})

for (const scheme of ['light', 'dark'] as const) {
  test(`header menu fits the viewport at 320px (${scheme})`, async ({ page }) => {
    await stubShopping(page, 'Kroger')
    await page.addInitScript((value) => {
      window.localStorage.setItem('mantine-color-scheme-value', value)
    }, scheme)
    await page.setViewportSize({ width: 320, height: 844 })
    await page.goto('/shopping')
    await expect.poll(() => page.evaluate(() => document.documentElement.getAttribute('data-mantine-color-scheme'))).toBe(scheme)
    await page.getByRole('button', { name: 'Menu' }).click()
    const menu = page.locator('.app-menu-dropdown')
    await expect(menu).toBeVisible()
    const box = await menu.boundingBox()
    expect(box).not.toBeNull()
    expect(box!.width).toBeLessThanOrEqual(320.5)
    expect(box!.x + box!.width).toBeLessThanOrEqual(320.5)
    const axe = await new AxeBuilder({ page })
      .include('.app-menu-dropdown')
      .withRules(['color-contrast', 'button-name', 'landmark-no-duplicate-banner'])
      .analyze()
    expect(axe.violations).toEqual([])
    for (const item of await page.getByRole('menuitem').all()) {
      if (!(await item.isVisible())) continue
      await assertTouchTargetSize(item)
    }
    await assertTouchTargetSize(page.locator('.app-menu-button'))
  })
}

test('admin Kroger credentials buttons meet 44px touch targets', async ({ page }) => {
  resetStage()
  stage.kind = 'account'
  stage.operator = true
  stage.role = 'owner'
  stage.householdName = 'Test Household'
  await install(page)
  await stubShopping(page, 'Kroger')
  await page.setViewportSize({ width: 390, height: 844 })
  await page.goto('/shopping')
  await page.getByRole('button', { name: 'Menu' }).click()
  await page.getByRole('menuitem', { name: 'Manage Kroger connection' }).click()
  const dialog = page.getByRole('dialog', { name: 'Kroger connection' })
  await expect(dialog).toBeVisible()
  await assertTouchTargetSize(dialog.getByRole('button', { name: 'Close' }))
  await assertTouchTargetSize(dialog.getByRole('button', { name: 'Save credentials' }))
  const axe = await new AxeBuilder({ page })
    .include('[role="dialog"]')
    .withRules(['color-contrast', 'button-name', 'landmark-no-duplicate-banner'])
    .analyze()
  expect(axe.violations).toEqual([])
})

test('nonadmin disconnect error keeps the Disconnect label visible while loading at 390px', async ({ page }) => {
  resetStage()
  stage.kind = 'account'
  stage.householdName = 'Test Household'
  stage.operator = false
  stage.role = 'member'
  await install(page)
  let calls = 0
  await page.route('**/api/providers/kroger/connection', async (route) => {
    if (route.request().method() !== 'DELETE') return route.continue()
    calls += 1
    await new Promise((resolve) => { setTimeout(resolve, calls === 1 ? 200 : 400) })
    return route.fulfill({
      status: calls === 1 ? 500 : 200,
      contentType: 'application/json',
      body: calls === 1 ? JSON.stringify({ error: 'Kroger could not be disconnected' }) : '{}',
    })
  })
  await stubShopping(page, 'Kroger')
  await page.setViewportSize({ width: 390, height: 844 })
  await page.goto('/shopping')
  await page.getByRole('button', { name: 'Menu' }).click()
  await page.getByRole('menuitem', { name: 'Disconnect Kroger' }).click()
  const dialog = page.getByRole('dialog', { name: 'Disconnect Kroger?' })
  const disconnect = dialog.getByRole('button', { name: 'Disconnect', exact: true })
  await disconnect.click()
  await expect(dialog.getByRole('alert')).toHaveText('Kroger could not be disconnected')
  const secondAttempt = disconnect.click()
  await expect(disconnect.locator('.credentials-disconnect-label-busy')).toBeVisible()
  await expect(disconnect.locator('.credentials-disconnect-label-busy')).toContainText('Disconnect')
  await secondAttempt
  await expect.poll(() => calls).toBeGreaterThanOrEqual(2)
})
