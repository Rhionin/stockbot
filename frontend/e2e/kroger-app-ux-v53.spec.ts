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

test('T53 member row omits unknown product access note', async ({ page }) => {
  resetStage()
  stage.kind = 'account'
  stage.role = 'member'
  stage.householdName = 'Test Household'
  stage.krogerAppGetBody = krogerApp('connected', { configured: true, canManage: false, productAccess: 'unknown' })
  await install(page)
  await page.setViewportSize({ width: 390, height: 844 })
  await page.goto('/household')
  await expect(page.locator('.store-app-row-title')).toHaveText(krogerAppCopy.rowConnected, { timeout: 15000 })
  await expect(page.getByText(/haven't checked yet/i)).toHaveCount(0)
})

test('step 7 connect shows connect-failed on authorize errors', async ({ page }) => {
  resetStage()
  stage.kind = 'account'
  stage.role = 'owner'
  stage.householdName = 'Test Household'
  stage.krogerAppGetBody = krogerApp('ready_to_connect', { configured: true })
  await install(page)
  await page.setViewportSize({ width: 390, height: 844 })
  await page.route('**/api/providers/kroger/authorize**', (route) => route.fulfill({
    status: 400,
    contentType: 'application/json',
    body: JSON.stringify({ code: 'validation', error: 'bad' }),
  }))
  await page.goto('/household')
  const connectButton = page.locator('.store-app-section').getByRole('button', { name: krogerAppCopy.rowConnect, exact: true })
  await expect(connectButton).toBeVisible()
  await connectButton.click()
  await expect(page.getByRole('alert')).toContainText("Couldn't connect your Test Kroger account")
})
