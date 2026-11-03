import { expect, test, type Page } from '@playwright/test'
import { install, resetStage, stage } from './householdStageFixtures'

const krogerApp = (status: string, overrides: Record<string, unknown> = {}) => ({
  configured: status !== 'not_set_up',
  clientIdLast4: status === 'not_set_up' ? null : '1234',
  redirectUri: 'https://test-household.example/cb',
  modality: 'PICKUP',
  productAccess: 'unknown',
  status,
  canManage: true,
  ...overrides,
})

const stubHousehold = async (page: Page, appBody: unknown) => {
  await page.route('**/api/households/hh-1/kroger-app', (route) => {
    if (route.request().method() === 'GET') {
      return route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify(appBody) })
    }
    return route.continue()
  })
  await page.route('**/api/households/hh-1', (route) => route.fulfill({
    status: 200,
    contentType: 'application/json',
    body: JSON.stringify({ id: 'hh-1', name: 'Test Household', role: 'owner', deletedAt: null, purgeAt: null }),
  }))
  await page.route('**/api/household/settings', (route) => route.fulfill({
    status: 200,
    contentType: 'application/json',
    body: JSON.stringify({ restockMode: 'auto' }),
  }))
  await page.route('**/api/invites', (route) => route.fulfill({
    status: 200,
    contentType: 'application/json',
    body: JSON.stringify({ invites: [] }),
  }))
  await page.route('**/api/households/hh-1/members', (route) => route.fulfill({
    status: 200,
    contentType: 'application/json',
    body: JSON.stringify({ members: [{ userId: 'user-1', email: 'owner@test.example', role: 'owner' }] }),
  }))
  await page.route('**/api/devices', (route) => route.fulfill({
    status: 200,
    contentType: 'application/json',
    body: JSON.stringify({ devices: [] }),
  }))
}

for (const viewport of [
  { width: 390, height: 844 },
  { width: 320, height: 568, deviceScaleFactor: 1.25 },
  { width: 1280, height: 800 },
]) {
  test(`store app row not_set_up ${viewport.width}`, async ({ page }) => {
    resetStage()
    stage.kind = 'account'
    stage.householdName = 'Test Household'
    stage.role = 'owner'
    await install(page)
    await page.setViewportSize({ width: viewport.width, height: viewport.height })
    await stubHousehold(page, krogerApp('not_set_up'))
    await page.goto('/household')
    await expect(page.getByRole('heading', { name: 'Store app' })).toBeVisible()
    await expect(page.getByRole('button', { name: 'Set up' })).toBeVisible()
  })
}

test('menu no longer lists Manage Kroger connection', async ({ page }) => {
  resetStage()
  stage.kind = 'account'
  await install(page)
  await page.route('**/api/shopping-list**', (route) => route.fulfill({ status: 200, body: '[]' }))
  await page.route('**/api/restock', (route) => route.fulfill({ status: 200, body: 'null' }))
  await page.route('**/api/providers', (route) => route.fulfill({ status: 200, body: '[]' }))
  await page.goto('/shopping')
  await page.getByRole('button', { name: 'Menu' }).click()
  await expect(page.getByRole('menuitem', { name: 'Manage Kroger connection' })).toHaveCount(0)
})
