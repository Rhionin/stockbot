import { expect, test, type Page } from '@playwright/test'
import { krogerAppCopy } from '@stockbot/shared/krogerAppCopy'
import { install, resetStage, stage } from './householdStageFixtures'

async function expectMinTargetSize(locator: ReturnType<Page['getByRole']>) {
  const box = await locator.boundingBox()
  expect(box).not.toBeNull()
  expect(box!.width).toBeGreaterThanOrEqual(44)
  expect(box!.height).toBeGreaterThanOrEqual(44)
}

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

test('store app touch targets meet 44px minimum', async ({ page }) => {
  resetStage()
  stage.kind = 'account'
  stage.householdName = 'Test Household'
  stage.role = 'owner'
  await install(page)
  await page.setViewportSize({ width: 390, height: 844 })
  await stubHousehold(page, krogerApp('not_set_up'))
  await page.goto('/household')
  await expectMinTargetSize(page.getByRole('button', { name: krogerAppCopy.rowSetUp }))
  await page.getByRole('button', { name: krogerAppCopy.rowSetUp }).click()
  const dialog = page.getByRole('dialog', { name: krogerAppCopy.title })
  await expect(dialog).toBeVisible()
  await expectMinTargetSize(page.getByRole('button', { name: krogerAppCopy.closeLabel }))
  await expectMinTargetSize(page.getByRole('button', { name: krogerAppCopy.stepsDisclosure }))
  await expectMinTargetSize(page.getByRole('link', { name: krogerAppCopy.s1OpenA11y }))

  await page.getByRole('button', { name: krogerAppCopy.closeLabel }).click()
  await expect(dialog).toBeHidden()

  await stubHousehold(page, krogerApp('connected', { configured: true, clientIdLast4: '1234' }))
  await page.reload()
  await expectMinTargetSize(page.getByRole('button', { name: krogerAppCopy.menuMore }))
  await page.getByRole('button', { name: krogerAppCopy.menuMore }).click()
  await expectMinTargetSize(page.getByRole('menuitem', { name: krogerAppCopy.menuReplace }))
  await expectMinTargetSize(page.getByRole('menuitem', { name: krogerAppCopy.menuTest }))
  await expectMinTargetSize(page.getByRole('menuitem', { name: krogerAppCopy.menuRemove }))
  await page.getByRole('menuitem', { name: krogerAppCopy.menuRemove }).click()
  const removeDialog = page.getByRole('dialog', { name: krogerAppCopy.rmTitle })
  await expect(removeDialog).toBeVisible()
  await expectMinTargetSize(page.getByRole('button', { name: krogerAppCopy.closeLabel }))

  await page.getByRole('button', { name: krogerAppCopy.rmCancel }).click()
  await page.getByRole('button', { name: krogerAppCopy.menuMore }).click()
  await page.getByRole('menuitem', { name: krogerAppCopy.menuReplace }).click()
  await page.getByRole('dialog', { name: krogerAppCopy.menuReplace }).waitFor()
  const modalityControl = page.locator('.store-app-modality-control')
  await expect(modalityControl).toBeVisible()
  const pickupSegment = modalityControl.getByText(krogerAppCopy.modalityOptionPickup, { exact: true })
  await expectMinTargetSize(pickupSegment)
  const deliverySegment = modalityControl.getByText(krogerAppCopy.modalityOptionDelivery, { exact: true })
  await expectMinTargetSize(deliverySegment)

  await page.getByRole('button', { name: krogerAppCopy.s6Show }).click()

  await page.getByLabel(/App ID/i).fill('x')
  await page.getByLabel(/App secret/i).fill('y')
  const replaceDialog = page.getByRole('dialog', { name: krogerAppCopy.menuReplace })
  await replaceDialog.getByRole('button', { name: krogerAppCopy.closeLabel }).click()
  const closeConfirm = page.getByRole('dialog', { name: krogerAppCopy.closeConfirmTitle })
  await expect(closeConfirm).toBeVisible()
  await expectMinTargetSize(closeConfirm.getByRole('button', { name: krogerAppCopy.closeLabel }))
})
