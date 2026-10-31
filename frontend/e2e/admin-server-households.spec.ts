import { expect, test, type Locator, type Page } from '@playwright/test'
import { assertSectionsWithinViewport, assertVisibleControlsWithinViewport } from './helpers'
import { install, resetStage, stage } from './householdStageFixtures'

const MIN_TOUCH_TARGET_CSS_PX = 44
const TOUCH_TARGET_FLOAT_EPS = 0.01

const PM_HOUSEHOLD_RESTOCK_LABELS = [
  'New items restock automatically',
  'Ask before restocking new items',
  'Ask before restocking, and hold until you answer',
] as const

function touchTargetDimensionMeetsMinimum(value: number) {
  return value >= MIN_TOUCH_TARGET_CSS_PX - TOUCH_TARGET_FLOAT_EPS
}

async function assertTouchTargetSize(locator: Locator) {
  const box = await locator.boundingBox()
  expect(box).not.toBeNull()
  expect(touchTargetDimensionMeetsMinimum(box!.width)).toBe(true)
  expect(touchTargetDimensionMeetsMinimum(box!.height)).toBe(true)
}

async function assertAppHeaderTouchTargets(page: Page) {
  await assertTouchTargetSize(page.getByRole('button', { name: 'Menu' }))
  for (const name of ['Scan Queue', 'Inventory', 'History', 'Shopping List'] as const) {
    await assertTouchTargetSize(page.getByRole('link', { name }))
  }
  const householdControl = page.getByRole('combobox', { name: 'Household' })
    .or(page.getByRole('button', { name: 'Choose a household' }))
    .or(page.getByRole('button', { name: stage.householdName }))
  await assertTouchTargetSize(householdControl.first())
}

const NAV_LINK_NAMES = ['Scan Queue', 'Inventory', 'History', 'Shopping List'] as const

/** Box checks miss a label that overflows its link and is cut off by the nav. */
async function assertNavLabelsFit(page: Page) {
  const nav = page.locator('.app-nav')
  await expect(nav.evaluate((el) => el.scrollWidth <= el.clientWidth + 0.5)).resolves.toBe(true)
  for (const name of NAV_LINK_NAMES) {
    const fits = await page.getByRole('link', { name }).evaluate((link) => {
      const range = document.createRange()
      range.selectNodeContents(link)
      const text = range.getBoundingClientRect()
      const box = link.getBoundingClientRect()
      const navBox = link.closest('.app-nav')!.getBoundingClientRect()
      const eps = 0.5
      return text.left >= box.left - eps && text.right <= box.right + eps
        && box.left >= navBox.left - eps && box.right <= navBox.right + eps
    })
    expect(fits, `${name} label fits inside its link and the nav`).toBe(true)
  }
}

/** Tapping 2px inside each corner of a 44px target must still activate it. */
async function assertCornersHit(locator: Locator) {
  const box = await locator.boundingBox()
  expect(box).not.toBeNull()
  const inset = 2
  for (const [x, y] of [
    [box!.x + inset, box!.y + inset],
    [box!.x + box!.width - inset, box!.y + inset],
    [box!.x + inset, box!.y + box!.height - inset],
    [box!.x + box!.width - inset, box!.y + box!.height - inset],
  ]) {
    const hit = await locator.evaluate((el, [px, py]) => {
      const top = document.elementFromPoint(px, py)
      return top !== null && (top === el || el.contains(top))
    }, [x, y])
    expect(hit, `corner (${x.toFixed(1)}, ${y.toFixed(1)}) hits the target`).toBe(true)
  }
}

/** Taps 2px inside each corner of an invite text field and expects focus on the input. */
async function assertInviteInputCornersFocus(page: Page) {
  const inset = 2
  for (const name of ['Name', 'Email', 'Household name'] as const) {
    const input = page.getByRole('textbox', { name, exact: true })
    await expect(input).toBeVisible()
    const box = await input.boundingBox()
    expect(box).not.toBeNull()
    for (const [x, y] of [
      [box!.x + inset, box!.y + inset],
      [box!.x + box!.width - inset, box!.y + inset],
      [box!.x + inset, box!.y + box!.height - inset],
      [box!.x + box!.width - inset, box!.y + box!.height - inset],
    ]) {
      await page.mouse.click(x, y)
      await expect(input).toBeFocused()
    }
  }
  const passwordInput = page.getByRole('textbox', { name: 'Password', exact: true })
  await expect(passwordInput).toBeVisible()
  const passwordBox = await passwordInput.boundingBox()
  expect(passwordBox).not.toBeNull()
  for (const [x, y] of [
    [passwordBox!.x + inset, passwordBox!.y + inset],
    [passwordBox!.x + passwordBox!.width - inset, passwordBox!.y + inset],
    [passwordBox!.x + inset, passwordBox!.y + passwordBox!.height - inset],
    [passwordBox!.x + passwordBox!.width - inset, passwordBox!.y + passwordBox!.height - inset],
  ]) {
    await page.mouse.click(x, y)
    await expect(passwordInput).toBeFocused()
  }
}

async function assertPasswordToggleInsideField(page: Page) {
  const wrapper = page.locator('.invite-accept-page .mantine-PasswordInput-root')
  const toggle = page.locator('.invite-accept-password-toggle')
  const field = await wrapper.boundingBox()
  const button = await toggle.boundingBox()
  expect(field).not.toBeNull()
  expect(button).not.toBeNull()
  expect(button!.x + button!.width).toBeLessThanOrEqual(field!.x + field!.width + 0.5)
  expect(button!.x).toBeGreaterThanOrEqual(field!.x - 0.5)
}

/** Taps the row just under the radio circle, outside the label text. */
async function tapRadioRowUnderCircle(page: Page, rootSelector: string, label: string) {
  const row = page.locator(`${rootSelector} .mantine-Radio-root`, { hasText: label })
  const body = await row.locator('.mantine-Radio-body').boundingBox()
  const circle = await row.locator('.mantine-Radio-inner').boundingBox()
  expect(body).not.toBeNull()
  expect(circle).not.toBeNull()
  await page.mouse.click(circle!.x + circle!.width / 2, body!.y + body!.height - 3)
  await expect(page.getByRole('radio', { name: label })).toBeChecked()
}

async function assertHouseholdsPageTouchTargets(page: Page) {
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
  // The AppShell main is the real horizontal scroll container (.app-shell clips overflow on html).
  await expect(page.locator('main.mantine-AppShell-main').evaluate((el) => el.scrollWidth <= el.clientWidth)).resolves.toBe(true)
}

async function assertAdminLayoutWithinViewport(page: Page) {
  await assertNoHorizontalScroll(page)
  await assertSectionsWithinViewport(page, [
    '.app-header',
    'main.mantine-AppShell-main',
    '[data-testid="operator-households-section"]',
  ])
  await assertVisibleControlsWithinViewport(page)
}

async function routeInviteStartPreview(page: Page) {
  await page.route('**/api/invites/preview', (route) => route.fulfill({
    status: 200,
    contentType: 'application/json',
    body: JSON.stringify({
      kind: 'start_household',
      role: 'owner',
      expiresAt: '2026-11-06T17:00:00.000Z',
    }),
  }))
  await page.route('**/api/auth/session', (route) => route.fulfill({
    status: 200,
    contentType: 'application/json',
    body: JSON.stringify({
      user: null,
      kind: 'signed_out',
      memberships: [],
      households: [],
      activeHouseholdId: null,
      operator: false,
    }),
  }))
}

async function assertInviteAcceptTouchTargets(page: Page) {
  const root = page.locator('.invite-accept-page')
  for (const locator of [
    root.getByRole('button'),
    root.locator('.mantine-Radio-label'),
  ]) {
    const count = await locator.count()
    for (let index = 0; index < count; index += 1) {
      const target = locator.nth(index)
      if (!(await target.isVisible())) continue
      await assertTouchTargetSize(target)
    }
  }
  for (const name of ['Name', 'Email', 'Household name'] as const) {
    await assertTouchTargetSize(root.getByRole('textbox', { name, exact: true }))
  }
  await assertTouchTargetSize(root.locator('.mantine-PasswordInput-input'))
}

async function openInviteAccept(page: Page, width: number, height: number) {
  resetStage()
  await page.setViewportSize({ width, height })
  await routeInviteStartPreview(page)
  await page.goto('/invite/test-start-code')
  await expect(page.getByRole('heading', { name: 'Start a household' })).toBeVisible()
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
    await assertAdminLayoutWithinViewport(page)
    await expectNoMemberPii(page)
  })
}

test('admin households wrap stat lines at 320x844', async ({ page }) => {
  await openAdmin(page, 320, 844)
  await routeHouseholds(page, [{
    ...idealStatsHousehold,
    name: 'Test Household',
    memberCount: 1234,
    productCount: 5678,
    itemsOnHand: 91011,
    pendingScans: 888,
    scansThisWeek: 7777,
  }])
  await page.goto('/households')
  const stats = page.getByTestId('operator-household-stats')
  const inventoryLine = stats.locator('.admin-household-stat-line').first()
  await expect(inventoryLine).toContainText('1234 members · 5678 products · 91011 items on hand')
  const partTops = await inventoryLine.locator('.admin-household-stat-part').evaluateAll((els) => els.map((el) => el.getBoundingClientRect().top))
  expect(partTops.length).toBeGreaterThanOrEqual(2)
  expect(partTops[partTops.length - 1]).toBeGreaterThan(partTops[0] + 1)
  await assertNoHorizontalScroll(page)
  await assertAdminLayoutWithinViewport(page)
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
    for (const label of PM_HOUSEHOLD_RESTOCK_LABELS) {
      await expect(page.getByLabel(label, { exact: true })).toBeVisible()
    }
    await assertAppHeaderTouchTargets(page)
    await assertHouseholdsPageTouchTargets(page)
    await assertAdminLayoutWithinViewport(page)
  })
}

for (const viewportWidth of [390, 320]) {
  test(`app header meets 44x44 touch targets at ${viewportWidth}x844`, async ({ page }) => {
    await openAdmin(page, viewportWidth, 844)
    await page.goto('/')
    await assertAppHeaderTouchTargets(page)
    await assertSectionsWithinViewport(page, ['.app-header'])
    await assertVisibleControlsWithinViewport(page)
    await assertNavLabelsFit(page)
    for (const name of NAV_LINK_NAMES) {
      await assertCornersHit(page.getByRole('link', { name }))
    }
    await assertCornersHit(page.getByRole('button', { name: 'Menu' }))
  })

  test(`app header nav labels stay reachable with 125% text at ${viewportWidth}x844`, async ({ page }) => {
    await openAdmin(page, viewportWidth, 844)
    await page.goto('/')
    await page.addStyleTag({ content: 'html { font-size: 125% !important; }' })
    const nav = page.locator('.app-nav')
    for (const name of NAV_LINK_NAMES) {
      const link = page.getByRole('link', { name })
      await link.scrollIntoViewIfNeeded()
      const visible = await link.evaluate((el) => {
        const range = document.createRange()
        range.selectNodeContents(el)
        const text = range.getBoundingClientRect()
        const box = el.getBoundingClientRect()
        const navBox = el.closest('.app-nav')!.getBoundingClientRect()
        return text.left >= box.left - 0.5 && text.right <= box.right + 0.5
          && text.left >= navBox.left - 0.5 && text.right <= navBox.right + 0.5
      })
      expect(visible, `${name} can be scrolled fully into view`).toBe(true)
    }
    // Overflow must stay reachable by swiping, not silently clipped.
    await expect(nav.evaluate((el) => el.scrollWidth <= el.clientWidth + 0.5
      || getComputedStyle(el).overflowX !== 'hidden')).resolves.toBe(true)
  })
}

for (const viewportWidth of [390, 320]) {
  test(`invite accept meets 44px touch targets at ${viewportWidth}x844`, async ({ page }) => {
    await openInviteAccept(page, viewportWidth, 844)
    for (const label of PM_HOUSEHOLD_RESTOCK_LABELS) {
      await expect(page.getByLabel(label, { exact: true })).toBeVisible()
    }
    await assertInviteAcceptTouchTargets(page)
    await assertSectionsWithinViewport(page, ['.invite-accept-page', '.login-card'])
    await assertVisibleControlsWithinViewport(page)
    await assertCornersHit(page.locator('.invite-accept-password-toggle'))
    await assertPasswordToggleInsideField(page)
    await assertInviteInputCornersFocus(page)
    await tapRadioRowUnderCircle(page, '.invite-accept-page', PM_HOUSEHOLD_RESTOCK_LABELS[1])
    const labelBox = await page.locator('.invite-accept-page .mantine-Radio-label', { hasText: PM_HOUSEHOLD_RESTOCK_LABELS[2] }).boundingBox()
    expect(labelBox).not.toBeNull()
    await page.mouse.click(labelBox!.x + labelBox!.width - 4, labelBox!.y + labelBox!.height - 3)
    await expect(page.getByRole('radio', { name: PM_HOUSEHOLD_RESTOCK_LABELS[2] })).toBeChecked()
    // The row overlay must not steal pointer events from the radio itself.
    await page.getByRole('radio', { name: PM_HOUSEHOLD_RESTOCK_LABELS[0] }).check({ timeout: 2000 })
  })
}

test('app header control heights match master at 1280x800', async ({ page }) => {
  await openAdmin(page, 1280, 800)
  await page.goto('/')
  const withinPx = (value: number, expected: number) => Math.abs(value - expected) <= 0.5
  const menu = await page.getByRole('button', { name: 'Menu' }).boundingBox()
  const household = await page.getByRole('combobox', { name: 'Household' }).boundingBox()
  const scanQueue = await page.getByRole('link', { name: 'Scan Queue' }).boundingBox()
  const shopping = await page.getByRole('link', { name: 'Shopping List' }).boundingBox()
  expect(menu).not.toBeNull()
  expect(household).not.toBeNull()
  expect(scanQueue).not.toBeNull()
  expect(shopping).not.toBeNull()
  // Pinned from master 95bc5a2a at 1280x800 (Playwright chromium).
  for (const [label, height, expected] of [
    ['Menu', menu!.height, 42],
    ['Household switcher', household!.height, 33.75],
    ['Scan Queue link', scanQueue!.height, 31.75],
    ['Shopping List link', shopping!.height, 31.75],
  ] as const) {
    expect(withinPx(height, expected), `${label} height ${height} vs master ${expected}`).toBe(true)
  }
})

test('invite accept control heights match master at 1280x800', async ({ page }) => {
  await openInviteAccept(page, 1280, 800)
  const withinPx = (value: number, expected: number, tolerance = 1) => Math.abs(value - expected) <= tolerance
  const name = await page.getByRole('textbox', { name: 'Name', exact: true }).boundingBox()
  const password = await page.getByRole('textbox', { name: 'Password', exact: true }).boundingBox()
  const household = await page.getByRole('textbox', { name: 'Household name', exact: true }).boundingBox()
  const create = await page.getByRole('button', { name: 'Create household' }).boundingBox()
  const radioLabel = await page.locator('.invite-accept-page .mantine-Radio-label').first().boundingBox()
  const toggle = await page.locator('.invite-accept-password-toggle').boundingBox()
  expect(name).not.toBeNull()
  expect(password).not.toBeNull()
  expect(household).not.toBeNull()
  expect(create).not.toBeNull()
  expect(radioLabel).not.toBeNull()
  expect(toggle).not.toBeNull()
  // Pinned from master 95bc5a2a at 1280x800 (Playwright chromium, default Mantine sm).
  for (const [label, height, expected] of [
    ['Name', name!.height, 33.75],
    ['Password', password!.height, 31.75],
    ['Household name', household!.height, 33.75],
    ['Create household', create!.height, 33.75],
    ['Radio label', radioLabel!.height, 18.75],
    ['Password toggle', toggle!.height, 26.25],
  ] as const) {
    expect(withinPx(height, expected, 0.5), `${label} height ${height} vs master ${expected}`).toBe(true)
  }
})

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
  await expect(section.getByRole('button', { name: 'Trying again…' })).toHaveAttribute('data-loading', 'true')
  const busyButton = section.getByRole('button', { name: 'Trying again…' })
  await expect(busyButton).toHaveAttribute('aria-busy', 'true')
  const box = await busyButton.boundingBox()
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
  await tapRadioRowUnderCircle(page, '.households-admin-page', 'Ask before restocking, and hold until you answer')
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
