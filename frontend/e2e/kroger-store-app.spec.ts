import { expect, test, type Page } from '@playwright/test'
import { krogerAppCopy } from '@stockbot/shared/krogerAppCopy'
import { install, resetStage, stage } from './householdStageFixtures'

async function expectMinTargetSize(locator: ReturnType<Page['getByRole']>) {
  const box = await locator.boundingBox()
  expect(box).not.toBeNull()
  expect(box!.width).toBeGreaterThanOrEqual(43.99)
  expect(box!.height).toBeGreaterThanOrEqual(43.99)
}

async function waitForStoreAppDialogSettled(page: Page, dialog: ReturnType<Page['getByRole']>) {
  await expect(dialog).toBeVisible()
  await page.waitForFunction(() => {
    const content = document.querySelector('.store-app-setup-modal .mantine-Modal-content')
    if (!(content instanceof HTMLElement)) return false
    const header = document.querySelector('.store-app-setup-modal .mantine-Modal-header')
    return getComputedStyle(content).opacity === '1'
      && header instanceof HTMLElement
      && header.getAttribute('role') === 'group'
  })
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

test('shopping hides Kroger controls when GET /api/providers marks Kroger disabled', async ({ page }) => {
  resetStage()
  stage.kind = 'account'
  await install(page)
  await page.route('**/api/shopping-list**', async (route) => {
    const incoming = route.request()
    if (incoming.method() === 'GET' && new URL(incoming.url()).pathname === '/api/shopping-list') {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify([{
          id: 'manual-1',
          itemId: 'item-1',
          quantity: 1,
          source: 'manual',
          purchasedAt: null,
        }]),
      })
      return
    }
    await route.continue()
  })
  await page.route('**/api/inventory', (route) => route.fulfill({ status: 200, contentType: 'application/json', body: '[]' }))
  await page.route('**/api/restock', (route) => route.fulfill({ status: 200, contentType: 'application/json', body: 'null' }))
  await page.route('**/api/providers', (route) => route.fulfill({
    status: 200,
    contentType: 'application/json',
    body: JSON.stringify([{ id: 'kroger', displayName: 'Kroger', status: 'disabled' }]),
  }))
  await page.goto('/shopping')
  await expect(page.getByLabel('Grocery providers')).toHaveCount(0)
  await expect(page.getByRole('button', { name: 'Send to Kroger' })).toHaveCount(0)
  await expect(page.getByRole('button', { name: 'Connect' })).toHaveCount(0)
  await expect(page.getByRole('button', { name: 'Start a new cart' })).toHaveCount(0)
})

test('menu no longer lists Manage Kroger connection', async ({ page }) => {
  resetStage()
  stage.kind = 'account'
  await install(page)
  await page.route('**/api/shopping-list**', (route) => route.fulfill({ status: 200, body: '[]' }))
  await page.route('**/api/restock', (route) => route.fulfill({ status: 200, body: 'null' }))
  await page.route('**/api/providers', (route) => route.fulfill({ status: 200, body: '[]' }))
  await page.goto('/shopping')
  await page.getByRole('button', { name: 'Menu' }).click()
  await expect(page.getByRole('menuitem').first()).toBeVisible()
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
  await waitForStoreAppDialogSettled(page, dialog)
  await expectMinTargetSize(page.getByRole('button', { name: krogerAppCopy.closeLabel }))
  await expectMinTargetSize(page.getByRole('button', { name: krogerAppCopy.stepsDisclosure }))
  await expectMinTargetSize(page.getByRole('link', { name: krogerAppCopy.s1OpenA11y }))
  await expectMinTargetSize(page.getByRole('button', { name: krogerAppCopy.next }))
  for (let step = 1; step < 6; step += 1) {
    await page.getByRole('button', { name: krogerAppCopy.next }).click()
  }
  await page.getByRole('button', { name: krogerAppCopy.stepsDisclosure }).click()
  const stepNavButtons = page.locator('.store-app-step-nav button')
  await expect(stepNavButtons).toHaveCount(7)
  for (const navButton of await stepNavButtons.all()) {
    await expectMinTargetSize(navButton)
  }
  await expectMinTargetSize(page.getByRole('button', { name: krogerAppCopy.back }))
  await expectMinTargetSize(page.getByRole('button', { name: krogerAppCopy.s6Test }))
  await expectMinTargetSize(page.getByRole('button', { name: krogerAppCopy.s6Show }))
  await expectMinTargetSize(page.getByLabel(/App ID/i))
  await expectMinTargetSize(page.getByLabel(/App secret/i))

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
  await expectMinTargetSize(page.getByRole('button', { name: krogerAppCopy.rmCancel }))

  await page.getByRole('button', { name: krogerAppCopy.rmCancel }).click()
  await page.getByRole('button', { name: krogerAppCopy.menuMore }).click()
  await page.getByRole('menuitem', { name: krogerAppCopy.menuReplace }).click()
  const replaceDialog = page.getByRole('dialog', { name: krogerAppCopy.menuReplace })
  await waitForStoreAppDialogSettled(page, replaceDialog)
  await expectMinTargetSize(replaceDialog.getByRole('button', { name: krogerAppCopy.replaceCancel }))
  const modalityControl = page.locator('.store-app-modality-control')
  await expect(modalityControl).toBeVisible()
  const segmentControls = modalityControl.locator('.mantine-SegmentedControl-control')
  await expectMinTargetSize(segmentControls.first())
  await expectMinTargetSize(segmentControls.last())

  await page.getByRole('button', { name: krogerAppCopy.s6Show }).click()

  await page.getByLabel(/App ID/i).fill('x')
  await page.getByLabel(/App secret/i).fill('y')
  await replaceDialog.getByRole('button', { name: krogerAppCopy.closeLabel }).click()
  const closeConfirm = page.getByRole('dialog', { name: krogerAppCopy.closeConfirmTitle })
  await expect(closeConfirm).toBeVisible()
  await expectMinTargetSize(closeConfirm.getByRole('button', { name: krogerAppCopy.closeConfirmKeep }))
  await expectMinTargetSize(closeConfirm.getByRole('button', { name: krogerAppCopy.closeConfirmClose }))
})

test('store app credential fields meet 44px at 1280', async ({ page }) => {
  resetStage()
  stage.kind = 'account'
  stage.householdName = 'Test Household'
  stage.role = 'owner'
  await install(page)
  await page.setViewportSize({ width: 1280, height: 800 })
  await stubHousehold(page, krogerApp('not_set_up'))
  await page.goto('/household')
  await page.getByRole('button', { name: krogerAppCopy.rowSetUp }).click()
  const dialog = page.getByRole('dialog', { name: krogerAppCopy.title })
  await waitForStoreAppDialogSettled(page, dialog)
  for (let step = 1; step < 6; step += 1) {
    await page.getByRole('button', { name: krogerAppCopy.next }).click()
  }
  await expectMinTargetSize(page.getByLabel(/App ID/i))
  await expectMinTargetSize(page.getByLabel(/App secret/i))
})

async function assertSetupBodyNoHorizontalScroll(page: Page) {
  await expect.poll(async () => page.evaluate(() => {
    const body = document.querySelector('.store-app-setup-body')
    if (!(body instanceof HTMLElement)) return false
    return body.scrollWidth <= body.clientWidth + 0.5
  })).toBe(true)
}

for (const viewport of [
  { width: 390, height: 844 },
  { width: 320, height: 568, deviceScaleFactor: 1.25 },
] as const) {
  test(`store app step 6 setup body has no horizontal scroll at ${viewport.width}`, async ({ page }) => {
    resetStage()
    stage.kind = 'account'
    stage.householdName = 'Test Household'
    stage.role = 'owner'
    await install(page)
    await page.setViewportSize({
      width: viewport.width,
      height: viewport.height,
      ...(viewport.deviceScaleFactor ? { deviceScaleFactor: viewport.deviceScaleFactor } : {}),
    })
    await stubHousehold(page, krogerApp('connected', { configured: true, clientIdLast4: '1234' }))
    await page.route('**/api/households/hh-1/kroger-app', (route) => {
      if (route.request().method() === 'PUT') {
        return route.fulfill({
          status: 409,
          contentType: 'application/json',
          body: JSON.stringify({
            code: 'kroger_not_connected',
            error: 'not configured',
            checkReason: 'not_configured',
          }),
        })
      }
      return route.continue()
    })
    await page.goto('/household')
    await page.getByRole('button', { name: krogerAppCopy.menuMore }).click()
    await page.getByRole('menuitem', { name: krogerAppCopy.menuReplace }).click()
    const dialog = page.getByRole('dialog', { name: krogerAppCopy.menuReplace })
    await waitForStoreAppDialogSettled(page, dialog)
    await assertSetupBodyNoHorizontalScroll(page)
    await page.getByRole('button', { name: krogerAppCopy.s6Show }).click()
    await assertSetupBodyNoHorizontalScroll(page)
    await page.getByRole('radio', { name: krogerAppCopy.modalityOptionDelivery }).click()
    await expect(page.getByText(krogerAppCopy.errRemoved)).toBeVisible()
    await assertSetupBodyNoHorizontalScroll(page)
  })
}
