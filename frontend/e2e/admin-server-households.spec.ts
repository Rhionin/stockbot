import { expect, test, type Locator, type Page } from '@playwright/test'
import { install, resetStage, stage } from './householdStageFixtures'

const MIN_TOUCH_TARGET_CSS_PX = 44
const TOUCH_TARGET_FLOAT_EPS = 0.01

function touchTargetDimensionMeetsMinimum(value: number) {
  return value >= MIN_TOUCH_TARGET_CSS_PX - TOUCH_TARGET_FLOAT_EPS
}

async function assertTouchTargetSize(locator: Locator) {
  const box = await locator.boundingBox()
  expect(box).not.toBeNull()
  expect(touchTargetDimensionMeetsMinimum(box!.width)).toBe(true)
  expect(touchTargetDimensionMeetsMinimum(box!.height)).toBe(true)
}

async function assertHouseholdsPageTouchTargets(page: Page) {
  // Global header touch targets (switcher, nav links, menu) are tracked separately; #475 covers /households content only.
  const root = page.locator('.households-admin-page')
  for (const locator of [
    root.getByRole('button'),
    root.getByRole('textbox'),
    root.locator('.mantine-Radio-label'),
  ]) {
    const count = await locator.count()
    for (let index = 0; index < count; index += 1) {
      const target = locator.nth(index)
      if (!(await target.isVisible())) continue
      await assertTouchTargetSize(target)
    }
  }
}

// Untyped legacy-server payload: an old or buggy server may still send members.
// The admin list must never render any of it.
const legacyMembers = [
  { userId: 'u-hidden', email: 'secret@example.com', name: 'Hidden Test Member', role: 'owner', createdAt: '2026-04-01T00:00:00Z' },
]

async function routeHouseholds(page: Page, households: Record<string, unknown>[]) {
  await page.route('**/api/operator/households', (route) => route.fulfill({
    status: 200,
    contentType: 'application/json',
    body: JSON.stringify({ households: households.map((row) => ({ ...row, members: legacyMembers })) }),
  }))
}

const baseRow = { isPrimary: false, restockMode: 'auto', createdAt: '2026-04-01T00:00:00Z', pendingScans: 0 }

async function assertNoHorizontalScroll(page: Page) {
  await expect(page.evaluate(() => {
    const root = document.documentElement
    return root.scrollWidth <= root.clientWidth
  })).resolves.toBe(true)
  await expect(page.locator('.households-admin-page').evaluate((el) => el.scrollWidth <= el.clientWidth)).resolves.toBe(true)
}

const idealStatsHousehold = {
  ...baseRow,
  id: 'hh-1',
  name: '',
  isPrimary: true,
  memberCount: 3,
  productCount: 4,
  itemsOnHand: 2,
  scannersOnline: 2,
  scannersOffline: 1,
  scansThisWeek: 5,
  lastActivityAt: '2026-04-02T00:00:00Z',
}

async function openAdmin(page: Page, width: number, height: number) {
  resetStage()
  stage.kind = 'account'
  stage.operator = true
  stage.householdName = 'Test Household'
  await page.setViewportSize({ width, height })
  await install(page)
}

async function expectNoMemberPii(page: Page) {
  const html = await page.content()
  for (const secret of ['secret@example.com', 'Hidden Test Member']) expect(html).not.toContain(secret)
  await expect(page.getByTestId('operator-households-section').getByText(/^\s*(Owner|Member)\s*$/)).toHaveCount(0)
}

for (const { width, height } of [{ width: 390, height: 844 }, { width: 1280, height: 800 }] as const) {
  test(`admin server households show counts only at ${width}x${height}`, async ({ page }) => {
    await openAdmin(page, width, height)
    await routeHouseholds(page, [{
      ...baseRow, id: 'hh-1', name: 'Test Household', isPrimary: true, memberCount: 3, productCount: 4, itemsOnHand: 2,
      scannersOnline: 2, scannersOffline: 1, scansThisWeek: 5, lastActivityAt: '2026-04-02T00:00:00Z',
    }])
    await page.goto('/households')
    await expect(page.getByRole('heading', { name: 'All households (Admin)' })).toBeVisible()
    const section = page.getByTestId('operator-households-section')
    await expect(section.getByText('Test Household', { exact: true }).first()).toBeVisible()
    const stats = section.getByTestId('operator-household-stats')
    await expect(stats).toContainText('3 members · 4 products · 2 items on hand')
    await expect(stats).toContainText('Scanners: 2 online · 1 offline')
    await expect(stats).toContainText('5 scans this week · 0 pending scans')
    await expect(stats).toContainText('Last activity')
    for (const part of await stats.locator('.admin-household-stat-part').all()) {
      await expect(part).toHaveCSS('white-space', 'nowrap')
    }
    await expectNoMemberPii(page)
    await expect(page.getByText(/\boperator\b/i)).toHaveCount(0)
  })
}

for (const viewportWidth of [390, 320]) {
  test(`admin households fit without horizontal scroll at ${viewportWidth}x844`, async ({ page }) => {
    await openAdmin(page, viewportWidth, 844)
    const longName = `Test Household ${'X'.repeat(60)}`
    await routeHouseholds(page, [{ ...idealStatsHousehold, name: longName }])
    await page.goto('/households')
    const section = page.getByTestId('operator-households-section')
    const name = section.getByText(longName, { exact: true })
    await expect(name).toBeVisible()
    const stats = section.getByTestId('operator-household-stats')
    await expect(stats).toContainText('3 members · 4 products · 2 items on hand')
    await expect(stats).toContainText('Scanners: 2 online · 1 offline')
    await expect(stats).toContainText('5 scans this week · 0 pending scans')
    await expect(stats).toContainText('Last activity')
    const card = await section.boundingBox()
    const box = await name.boundingBox()
    expect(box!.x + box!.width).toBeLessThanOrEqual(card!.x + card!.width)
    await assertNoHorizontalScroll(page)
    await expectNoMemberPii(page)
  })
}

test('admin households wrap stat lines at 320x844', async ({ page }) => {
  await openAdmin(page, 320, 844)
  await routeHouseholds(page, [{
    ...idealStatsHousehold,
    name: 'Test Household',
    memberCount: 12,
    productCount: 34,
    itemsOnHand: 567,
    pendingScans: 8,
    scansThisWeek: 91,
  }])
  await page.goto('/households')
  const stats = page.getByTestId('operator-household-stats')
  const inventoryLine = stats.locator('.admin-household-stat-line').first()
  await expect(inventoryLine).toContainText('12 members · 34 products · 567 items on hand')
  const lineBox = await inventoryLine.boundingBox()
  const partBox = await stats.locator('.admin-household-stat-part').first().boundingBox()
  const viewport = page.viewportSize()
  expect(lineBox).not.toBeNull()
  expect(partBox).not.toBeNull()
  expect(viewport).not.toBeNull()
  expect(lineBox!.x + lineBox!.width).toBeLessThanOrEqual(viewport!.width + 0.5)
  expect(lineBox!.height).toBeGreaterThan(partBox!.height + 4)
  await assertNoHorizontalScroll(page)
})

test('admin households show the load error inline, without the empty copy', async ({ page }) => {
  await openAdmin(page, 390, 844)
  await page.route('**/api/operator/households', (route) => route.fulfill({
    status: 500, contentType: 'application/json', body: JSON.stringify({ error: 'boom', code: 'internal' }),
  }))
  await page.goto('/households')
  const section = page.getByTestId('operator-households-section')
  await expect(section.getByRole('alert')).toHaveText('Something went wrong. Try again.')
  await expect(section.getByText('No households on this server yet.')).toHaveCount(0)
  await expect(page.getByText(/\boperator\b/i)).toHaveCount(0)
})

test('admin households recover from a server error when Try again succeeds', async ({ page }) => {
  await openAdmin(page, 390, 844)
  let calls = 0
  let fail = true
  await page.route('**/api/operator/households', async (route) => {
    calls += 1
    if (fail) {
      await route.fulfill({
        status: 500,
        contentType: 'application/json',
        body: JSON.stringify({ error: 'boom', code: 'internal' }),
      })
      return
    }
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({
        households: [{
          ...baseRow,
          id: 'hh-1',
          name: 'Test Household',
          isPrimary: true,
          memberCount: 1,
          productCount: 1,
          itemsOnHand: 1,
          scannersOnline: 0,
          scannersOffline: 0,
          scansThisWeek: 0,
        }],
      }),
    })
  })
  await page.goto('/households')
  const section = page.getByTestId('operator-households-section')
  await expect(section.getByRole('alert')).toHaveText('Something went wrong. Try again.')
  const callsBeforeRetry = calls
  fail = false
  await section.getByRole('button', { name: 'Try again' }).click()
  await expect(section.getByText('Test Household', { exact: true }).first()).toBeVisible()
  await expect(section.getByTestId('operator-household-stats')).toContainText('Scanners: 0 online · 0 offline')
  expect(calls).toBe(callsBeforeRetry + 1)
  expect(callsBeforeRetry).toBeGreaterThanOrEqual(1)
})

for (const viewportWidth of [390, 320]) {
  test(`admin households meet 44x44 touch targets at ${viewportWidth}x844`, async ({ page }) => {
    await openAdmin(page, viewportWidth, 844)
    await routeHouseholds(page, [{
      ...baseRow, id: 'hh-1', name: 'Test Household', isPrimary: true, memberCount: 1, productCount: 1, itemsOnHand: 1,
      scannersOnline: 0, scannersOffline: 0, scansThisWeek: 0,
    }])
    await page.goto('/households')
    await assertHouseholdsPageTouchTargets(page)
  })
}

test('admin households Try again is at least 44px tall while retrying', async ({ page }) => {
  await openAdmin(page, 390, 844)
  let fail = true
  let releaseRetry: () => void = () => undefined
  const retryGate = new Promise<void>((resolve) => { releaseRetry = resolve })
  await page.route('**/api/operator/households', async (route) => {
    if (fail) {
      await route.fulfill({
        status: 500,
        contentType: 'application/json',
        body: JSON.stringify({ error: 'boom', code: 'internal' }),
      })
      return
    }
    await retryGate
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({ households: [] }),
    })
  })
  await page.goto('/households')
  const section = page.getByTestId('operator-households-section')
  await expect(section.getByRole('alert')).toHaveText('Something went wrong. Try again.')
  fail = false
  const tryAgain = section.getByRole('button', { name: 'Try again' })
  const clickPromise = tryAgain.click()
  await expect(tryAgain).toHaveAttribute('data-loading', 'true')
  const box = await tryAgain.boundingBox()
  expect(box).not.toBeNull()
  expect(touchTargetDimensionMeetsMinimum(box!.height)).toBe(true)
  releaseRetry()
  await clickPromise
})

test('admin households select a restock radio when tapping the label padding', async ({ page }) => {
  await openAdmin(page, 390, 844)
  await page.goto('/households')
  const label = page.locator('.households-admin-page .mantine-Radio-label', { hasText: 'Ask before restocking new items' })
  await expect(label).toBeVisible()
  const box = await label.boundingBox()
  expect(box).not.toBeNull()
  await page.mouse.click(box!.x + box!.width / 2, box!.y + box!.height - 10)
  await expect(page.getByRole('radio', { name: 'Ask before restocking new items' })).toBeChecked()
})

test('manage household keeps compact member controls at 390x844', async ({ page }) => {
  resetStage()
  stage.kind = 'account'
  stage.role = 'owner'
  stage.operator = false
  stage.householdName = 'Test Household'
  await page.setViewportSize({ width: 390, height: 844 })
  await install(page)
  await page.goto('/household')
  await expect(page.locator('.households-admin-page')).toHaveCount(0)
  const remove = page.getByRole('button', { name: 'Remove' }).first()
  await expect(remove).toBeVisible()
  const box = await remove.boundingBox()
  expect(box).not.toBeNull()
  expect(Math.ceil(box!.height)).toBeLessThan(44)
})
