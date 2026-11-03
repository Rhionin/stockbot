import { expect, test } from '@playwright/test'
import { krogerAppCopy } from '@stockbot/shared/krogerAppCopy'
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

async function stubHousehold(page: import('@playwright/test').Page, appBody: unknown) {
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
  await page.route('**/api/invites', (route) => route.fulfill({ status: 200, contentType: 'application/json', body: '[]' }))
  await page.route('**/api/households/hh-1/members', (route) => route.fulfill({
    status: 200,
    contentType: 'application/json',
    body: JSON.stringify({ members: [{ userId: 'user-1', email: 'owner@test.example', role: 'owner' }] }),
  }))
  await page.route('**/api/devices', (route) => route.fulfill({ status: 200, contentType: 'application/json', body: '[]' }))
  await page.route('**/api/providers', (route) => route.fulfill({ status: 200, contentType: 'application/json', body: '[]' }))
  await page.route('**/api/stores', (route) => route.fulfill({
    status: 200,
    contentType: 'application/json',
    body: JSON.stringify({
      stores: [{ id: 's1', name: 'Test Kroger', linkedProvider: 'kroger' }],
      defaultStoreId: 's1',
      lookupsEnabled: false,
      canConnect: true,
    }),
  }))
}

test('T53 member row omits unknown product access note', async ({ page }) => {
  resetStage()
  stage.kind = 'account'
  stage.role = 'member'
  stage.householdName = 'Test Household'
  await install(page)
  await page.setViewportSize({ width: 390, height: 844 })
  await stubHousehold(page, krogerApp('connected', { configured: true, canManage: false, productAccess: 'unknown' }))
  await page.goto('/household')
  await expect(page.getByRole('heading', { name: krogerAppCopy.rowTitle })).toBeVisible()
  await expect(page.locator('.store-app-row-title')).toHaveText(krogerAppCopy.rowConnected)
  await expect(page.getByText(/haven't checked yet/i)).toHaveCount(0)
})

test('step 7 connect shows connect-failed on authorize errors', async ({ page }) => {
  resetStage()
  stage.kind = 'account'
  stage.role = 'owner'
  stage.householdName = 'Test Household'
  await install(page)
  await page.setViewportSize({ width: 390, height: 844 })
  await stubHousehold(page, krogerApp('ready_to_connect', { configured: true }))
  await page.route('**/api/providers/kroger/authorize**', (route) => route.fulfill({
    status: 400,
    contentType: 'application/json',
    body: JSON.stringify({ code: 'validation', error: 'bad' }),
  }))
  await page.goto('/household')
  const connectButton = page.getByRole('button', { name: /Connect your/i })
  await expect(connectButton).toBeVisible()
  await connectButton.click()
  await expect(page.getByRole('alert')).toContainText("Couldn't connect your store account")
})
