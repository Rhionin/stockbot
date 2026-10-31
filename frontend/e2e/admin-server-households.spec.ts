import { expect, test, type Locator, type Page, type TestInfo } from '@playwright/test'
import { assertSectionsWithinViewport, assertVisibleControlsWithinViewport } from './helpers'
import { install, resetStage, stage } from './householdStageFixtures'

const MIN_TOUCH_TARGET_CSS_PX = 44
const TOUCH_TARGET_FLOAT_EPS = 0.01
// Master pins are measured in local Chromium; Linux CI subpixel and font metrics differ slightly.
const MASTER_LAYOUT_X_TOLERANCE_PX = 4
const MASTER_LAYOUT_WIDTH_TOLERANCE_PX = 8

function expectBoxLayout(
  label: string,
  box: { x: number; width: number; height: number } | null,
  expected: { x: number; width: number; height?: number },
  heightTolerance = 0.5,
  widthTolerance = MASTER_LAYOUT_WIDTH_TOLERANCE_PX,
) {
  expect(box, label).not.toBeNull()
  expect(Math.abs(box!.x - expected.x), `${label} x`).toBeLessThanOrEqual(MASTER_LAYOUT_X_TOLERANCE_PX)
  expect(Math.abs(box!.width - expected.width), `${label} width`).toBeLessThanOrEqual(widthTolerance)
  if (expected.height !== undefined) {
    expect(Math.abs(box!.height - expected.height), `${label} height`).toBeLessThanOrEqual(heightTolerance)
  }
}

const MASTER_NAV_GAP_TOLERANCE_PX = 2

function expectHorizontalGap(
  label: string,
  left: { x: number; width: number },
  right: { x: number; width: number },
  expectedGap: number,
  tolerance = MASTER_NAV_GAP_TOLERANCE_PX,
) {
  const gap = right.x - (left.x + left.width)
  expect(Math.abs(gap - expectedGap), label).toBeLessThanOrEqual(tolerance)
}

async function blurActiveElement(page: Page) {
  await page.evaluate(() => { (document.activeElement as HTMLElement | null)?.blur() })
}

async function assertDocumentAndMainNoHorizontalScroll(page: Page) {
  await expect(page.evaluate(() => document.documentElement.scrollWidth <= document.documentElement.clientWidth)).resolves.toBe(true)
  await expect(page.locator('main.mantine-AppShell-main').evaluate((el) => el.scrollWidth <= el.clientWidth)).resolves.toBe(true)
}

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
  const householdControl = page.getByRole('combobox', { name: stage.householdName })
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

/** Taps 2px inside each corner of an invite text field; password left corners focus, right corners toggle. */
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
      await blurActiveElement(page)
      await page.mouse.click(x, y)
      await expect(input).toBeFocused()
    }
  }
  const passwordInput = page.getByRole('textbox', { name: 'Password', exact: true })
  const toggle = page.locator('.invite-accept-password-toggle')
  await expect(passwordInput).toBeVisible()
  const passwordBox = await page.locator('.invite-accept-page .mantine-PasswordInput-input').boundingBox()
  expect(passwordBox).not.toBeNull()
  const leftCorners = [
    [passwordBox!.x + inset, passwordBox!.y + inset],
    [passwordBox!.x + inset, passwordBox!.y + passwordBox!.height - inset],
    [passwordBox!.x + inset, passwordBox!.y + passwordBox!.height / 2],
  ] as const
  const rightCorners = [
    [passwordBox!.x + passwordBox!.width - inset, passwordBox!.y + inset],
    [passwordBox!.x + passwordBox!.width - inset, passwordBox!.y + passwordBox!.height - inset],
    [passwordBox!.x + passwordBox!.width - inset, passwordBox!.y + passwordBox!.height / 2],
  ] as const
  await blurActiveElement(page)
  await expect(toggle).toHaveAttribute('aria-pressed', 'false')
  for (const [x, y] of leftCorners) {
    await blurActiveElement(page)
    await page.mouse.click(x, y)
    await expect(passwordInput).toBeFocused()
    await expect(toggle).toHaveAttribute('aria-pressed', 'false')
  }
  for (const [x, y] of rightCorners) {
    await blurActiveElement(page)
    await page.mouse.click(x, y)
    await expect(toggle).toHaveAttribute('aria-pressed', 'true')
    await page.mouse.click(x, y)
    await expect(toggle).toHaveAttribute('aria-pressed', 'false')
  }
}

async function assertPasswordToggleInsideField(page: Page) {
  const field = page.locator('.invite-accept-page .mantine-PasswordInput-input')
  const toggle = page.locator('.invite-accept-password-toggle')
  const fieldBox = await field.boundingBox()
  const button = await toggle.boundingBox()
  expect(fieldBox).not.toBeNull()
  expect(button).not.toBeNull()
  expect(button!.x + button!.width).toBeLessThanOrEqual(fieldBox!.x + fieldBox!.width + 0.5)
  expect(button!.x).toBeGreaterThanOrEqual(fieldBox!.x - 0.5)
}

/** Row taps at circle, label text, and body left/right edges must select that option. */
async function assertRadioRowTapPoints(page: Page, rootSelector: string, label: string) {
  const row = page.locator(`${rootSelector} .mantine-Radio-root`, { hasText: label })
  const bodyBox = await row.locator('.mantine-Radio-body').boundingBox()
  const circle = await row.locator('.mantine-Radio-inner').boundingBox()
  const labelBox = await row.locator('.mantine-Radio-label').boundingBox()
  expect(bodyBox).not.toBeNull()
  expect(circle).not.toBeNull()
  expect(labelBox).not.toBeNull()
  // The label text must start clear of the circle, not under it.
  const textLeft = await row.locator('.mantine-Radio-label').evaluate((el) => {
    const range = document.createRange()
    range.selectNodeContents(el)
    return range.getBoundingClientRect().left
  })
  expect(textLeft, `${label} text starts right of the circle`).toBeGreaterThanOrEqual(circle!.x + circle!.width)
  const inset = 2
  const tapPoints = [
    [circle!.x + circle!.width / 2, bodyBox!.y + bodyBox!.height / 2],
    [labelBox!.x + labelBox!.width / 2, labelBox!.y + labelBox!.height / 2],
    [bodyBox!.x + inset, bodyBox!.y + bodyBox!.height / 2],
    [bodyBox!.x + bodyBox!.width - inset, bodyBox!.y + bodyBox!.height / 2],
    // Above and below the circle: the label must cover the circle column, not just the text.
    [circle!.x + circle!.width / 2, bodyBox!.y + inset],
    [circle!.x + circle!.width / 2, bodyBox!.y + bodyBox!.height - inset],
    [bodyBox!.x + inset, bodyBox!.y + inset],
    [bodyBox!.x + bodyBox!.width - inset, bodyBox!.y + inset],
    [bodyBox!.x + inset, bodyBox!.y + bodyBox!.height - inset],
    [bodyBox!.x + bodyBox!.width - inset, bodyBox!.y + bodyBox!.height - inset],
  ] as const
  const other = page.locator(`${rootSelector} .mantine-Radio-root`).filter({ hasNotText: label }).first()
  for (const [x, y] of tapPoints) {
    // Reset to another option first so a dead tap cannot pass on the previous selection.
    await other.locator('input[type="radio"]').click()
    await page.mouse.click(x, y)
    await expect(row.locator('input[type="radio"]')).toBeChecked()
  }
}

async function assertRadioRowsDoNotOverlap(page: Page, rootSelector: string) {
  const bodies = page.locator(`${rootSelector} .mantine-Radio-body`)
  const count = await bodies.count()
  const tops: number[] = []
  const bottoms: number[] = []
  for (let index = 0; index < count; index += 1) {
    const box = await bodies.nth(index).boundingBox()
    expect(box).not.toBeNull()
    tops.push(box!.y)
    bottoms.push(box!.y + box!.height)
  }
  for (let index = 0; index < tops.length - 1; index += 1) {
    expect(bottoms[index], `row ${index} overlaps row ${index + 1}`).toBeLessThanOrEqual(tops[index + 1] + 1)
  }
}

async function assertRadioRowsAt125Percent(page: Page, rootSelector: string) {
  await page.addStyleTag({ content: 'html { font-size: 125% !important; }' })
  await assertRadioRowsDoNotOverlap(page, rootSelector)
  const bodies = page.locator(`${rootSelector} .mantine-Radio-body`)
  const count = await bodies.count()
  for (let index = 0; index < count; index += 1) {
    const body = bodies.nth(index)
    const box = await body.boundingBox()
    expect(box).not.toBeNull()
    expect(touchTargetDimensionMeetsMinimum(box!.height)).toBe(true)
    const label = body.locator('.mantine-Radio-label')
    await expect(label.evaluate((el) => el.scrollHeight <= el.clientHeight + 0.5)).resolves.toBe(true)
  }
}

async function assertRadioRowTouchTarget(page: Page, rootSelector: string, label: string) {
  const row = page.locator(`${rootSelector} .mantine-Radio-root`, { hasText: label })
  const body = row.locator('.mantine-Radio-body')
  const field = page.locator(`${rootSelector} .mantine-RadioGroup-root`).first()
  const bodyBox = await body.boundingBox()
  const fieldBox = await field.boundingBox()
  expect(bodyBox).not.toBeNull()
  expect(fieldBox).not.toBeNull()
  expect(Math.abs(bodyBox!.width - fieldBox!.width)).toBeLessThanOrEqual(1)
  expect(touchTargetDimensionMeetsMinimum(bodyBox!.height)).toBe(true)
  await assertRadioRowTapPoints(page, rootSelector, label)
  await assertRadioRowsDoNotOverlap(page, rootSelector)
}

async function assertHouseholdSwitcherEllipsis(page: Page, fullName: string) {
  const switcher = page.locator('.app-header .household-switch .mantine-Select-input')
  await expect(switcher).toBeVisible()
  await expect(page.getByRole('combobox', { name: fullName })).toBeVisible()
  await expect(switcher.evaluate((el) => {
    const style = getComputedStyle(el)
    return el.scrollWidth > el.clientWidth + 0.5 && style.textOverflow === 'ellipsis'
  })).resolves.toBe(true)
}

async function assertHouseholdsPageTouchTargets(page: Page) {
  const root = page.locator('.households-admin-page')
  for (const locator of [
    root.getByRole('button'),
    root.getByRole('textbox'),
    root.locator('.mantine-Radio-body'),
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
    root.locator('.mantine-Radio-body'),
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

async function openAdmin(page: Page, width: number, height: number, householdName = 'Test Household') {
  resetStage()
  stage.kind = 'account'
  stage.operator = true
  stage.householdName = householdName
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
    await assertRadioRowTouchTarget(page, '.households-admin-page', PM_HOUSEHOLD_RESTOCK_LABELS[0])
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
    await expect(nav).toHaveCSS('overflow-x', 'auto')
    await assertDocumentAndMainNoHorizontalScroll(page)
    for (const name of NAV_LINK_NAMES) {
      const link = page.getByRole('link', { name })
      await assertTouchTargetSize(link)
      await expect(link).toHaveCSS('white-space', 'nowrap')
      await expect(link.evaluate((el) => getComputedStyle(el).textOverflow !== 'ellipsis')).resolves.toBe(true)
      await link.evaluate((el) => { el.scrollIntoView({ block: 'nearest', inline: 'center' }) })
      const visible = await link.evaluate((el) => {
        const range = document.createRange()
        range.selectNodeContents(el)
        const text = range.getBoundingClientRect()
        const box = el.getBoundingClientRect()
        const navBox = el.closest('.app-nav')!.getBoundingClientRect()
        const eps = 0.5
        return text.left >= box.left - eps && text.right <= box.right + eps
          && text.left >= navBox.left - eps && text.right <= navBox.right + eps
      })
      expect(visible, `${name} label is fully visible after horizontal scroll`).toBe(true)
    }
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
    await assertRadioRowTouchTarget(page, '.invite-accept-page', PM_HOUSEHOLD_RESTOCK_LABELS[1])
    await assertRadioRowTapPoints(page, '.invite-accept-page', PM_HOUSEHOLD_RESTOCK_LABELS[2])
    // The row overlay must not steal pointer events from the radio itself.
    await page.getByRole('radio', { name: PM_HOUSEHOLD_RESTOCK_LABELS[0] }).check({ timeout: 2000 })
  })
}

for (const viewportWidth of [390, 320]) {
  test(`app header household switcher ellipsizes at ${viewportWidth}x844 with 110% text`, async ({ page }) => {
    await openAdmin(page, viewportWidth, 844, 'Test Household Pantry Storage')
    await page.goto('/')
    await page.addStyleTag({ content: 'html { font-size: 110% !important; }' })
    await assertHouseholdSwitcherEllipsis(page, 'Test Household Pantry Storage')
  })
}

for (const viewportWidth of [390, 320]) {
  test(`invite accept radio rows fit at ${viewportWidth}x844 with 125% text`, async ({ page }) => {
    await openInviteAccept(page, viewportWidth, 844)
    await assertRadioRowTapPoints(page, '.invite-accept-page', PM_HOUSEHOLD_RESTOCK_LABELS[0])
    await assertRadioRowsAt125Percent(page, '.invite-accept-page')
  })

  test(`admin households radio rows fit at ${viewportWidth}x844 with 125% text`, async ({ page }) => {
    await openAdmin(page, viewportWidth, 844)
    await page.goto('/households')
    await assertRadioRowTapPoints(page, '.households-admin-page', PM_HOUSEHOLD_RESTOCK_LABELS[1])
    await assertRadioRowsAt125Percent(page, '.households-admin-page')
  })
}

test('app header control layout matches master at 1280x800', async ({ page }) => {
  await openAdmin(page, 1280, 800)
  await page.goto('/')
  const menu = await page.getByRole('button', { name: 'Menu' }).boundingBox()
  const household = await page.getByRole('combobox', { name: 'Test Household' }).boundingBox()
  const scanQueue = await page.getByRole('link', { name: 'Scan Queue' }).boundingBox()
  const inventory = await page.getByRole('link', { name: 'Inventory' }).boundingBox()
  const history = await page.getByRole('link', { name: 'History' }).boundingBox()
  const shopping = await page.getByRole('link', { name: 'Shopping List' }).boundingBox()
  const header = await page.locator('.app-header').boundingBox()
  expect(header).not.toBeNull()
  expect(header!.height).toBeLessThanOrEqual(52)
  expectBoxLayout('Menu', menu, { x: 976.75, width: 42, height: 42 }, 0.5, 1)
  expect(household).not.toBeNull()
  expect(household!.x, 'Household switcher before Scan Queue').toBeLessThan(scanQueue!.x)
  expect(household!.x + household!.width, 'Household switcher clear of Scan Queue').toBeLessThanOrEqual(scanQueue!.x)
  expect(Math.abs(household!.width - 220), 'Household switcher width').toBeLessThanOrEqual(MASTER_LAYOUT_WIDTH_TOLERANCE_PX)
  expect(Math.abs(household!.height - 33.75), 'Household switcher height').toBeLessThanOrEqual(0.5)
  expectHorizontalGap('Household switcher to Scan Queue', household!, scanQueue!, 15)
  expectHorizontalGap('Scan Queue to Inventory', scanQueue!, inventory!, 2)
  expectHorizontalGap('Inventory to History', inventory!, history!, 2)
  expectHorizontalGap('History to Shopping List', history!, shopping!, 2)
  for (const [label, box, height] of [
    ['Scan Queue link', scanQueue, 31.75],
    ['Inventory link', inventory, 31.75],
    ['History link', history, 31.75],
    ['Shopping List link', shopping, 31.75],
  ] as const) {
    expect(Math.abs(box!.height - height), `${label} height`).toBeLessThanOrEqual(0.5)
  }
  const navRowY = scanQueue!.y
  for (const [label, box] of [
    ['Household switcher', household],
    ['Inventory', inventory],
    ['History', history],
    ['Shopping List', shopping],
  ] as const) {
    expect(Math.abs(box!.y - navRowY), `${label} vertical alignment`).toBeLessThanOrEqual(2)
  }
  await assertDocumentAndMainNoHorizontalScroll(page)
})

test('invite accept control layout matches master at 1280x800', async ({ page }) => {
  await openInviteAccept(page, 1280, 800)
  const name = await page.getByRole('textbox', { name: 'Name', exact: true }).boundingBox()
  const email = await page.getByRole('textbox', { name: 'Email', exact: true }).boundingBox()
  const passwordInner = await page.getByRole('textbox', { name: 'Password', exact: true }).boundingBox()
  const passwordWrap = await page.locator('.invite-accept-page .mantine-PasswordInput-input').boundingBox()
  const household = await page.getByRole('textbox', { name: 'Household name', exact: true }).boundingBox()
  const create = await page.getByRole('button', { name: 'Create household' }).boundingBox()
  const radioBody = await page.locator('.invite-accept-page .mantine-Radio-body').first().boundingBox()
  const toggle = await page.locator('.invite-accept-password-toggle').boundingBox()
  const fieldPin = { x: 459, width: 362, height: 33.75 }
  expectBoxLayout('Name', name, fieldPin)
  expectBoxLayout('Email', email, fieldPin)
  expectBoxLayout('Household name', household, fieldPin)
  expectBoxLayout('Create household', create, fieldPin)
  expectBoxLayout('Password wrapper', passwordWrap, fieldPin)
  expectBoxLayout('Password inner', passwordInner, { x: 460, width: 360, height: 31.75 })
  expectBoxLayout('Radio row', radioBody, { x: 459, width: 362, height: 18.75 })
  expectBoxLayout('Password toggle', toggle, { x: 791, width: 26.25, height: 26.25 })
  await assertRadioRowTapPoints(page, '.invite-accept-page', PM_HOUSEHOLD_RESTOCK_LABELS[0])
  await assertRadioRowsDoNotOverlap(page, '.invite-accept-page')
  await expect(page.evaluate(() => document.documentElement.scrollWidth <= document.documentElement.clientWidth)).resolves.toBe(true)
})

test('admin households radio layout matches master at 1280x800', async ({ page }) => {
  await openAdmin(page, 1280, 800)
  await page.goto('/households')
  const radioBody = await page.locator('.households-admin-page .mantine-Radio-body').first().boundingBox()
  expectBoxLayout('Admin radio row', radioBody, { x: 311, width: 598, height: 44 })
  expect(touchTargetDimensionMeetsMinimum(radioBody!.height)).toBe(true)
  await assertRadioRowTapPoints(page, '.households-admin-page', PM_HOUSEHOLD_RESTOCK_LABELS[1])
  await assertRadioRowsDoNotOverlap(page, '.households-admin-page')
})

async function installTryAgainRetryRoute(page: Page) {
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
  return {
    unlockRetry: () => { fail = false },
    releaseRetry,
  }
}

async function assertTryAgainButtonSizing(
  section: Locator,
  testInfo: TestInfo,
  captureScreenshots: boolean,
) {
  const tryAgain = section.getByRole('button', { name: 'Try again' })
  await expect(tryAgain).toHaveAccessibleName('Try again')
  await expect(tryAgain.locator('.admin-household-retry-label-stack span').nth(1)).toHaveAttribute('aria-hidden', 'true')
  const idleMinWidth = await tryAgain.evaluate((el) => getComputedStyle(el).minWidth)
  // getComputedStyle reports min-width in px (e.g. 172.5px), never rem, so match the unset values.
  expect(['auto', '0px'], 'Try again uses content width, not a fixed min-width').toContain(idleMinWidth)
  const idleBox = await tryAgain.boundingBox()
  expect(idleBox).not.toBeNull()
  const contentWidth = await tryAgain.evaluate((el) => {
    const px = (v: string) => parseFloat(v) || 0
    const cs = getComputedStyle(el)
    const labelWidths = [...el.querySelectorAll('.admin-household-retry-label-stack > span')].map((span) => (
      span.getBoundingClientRect().width
    ))
    return px(cs.borderLeftWidth) + px(cs.paddingLeft) + px(cs.paddingRight) + px(cs.borderRightWidth)
      + Math.max(...labelWidths)
  })
  expect(Math.abs(idleBox!.width - contentWidth), 'Try again width follows the widest stacked label').toBeLessThanOrEqual(0.5)
  await expect(tryAgain.evaluate((el) => {
    const idle = el.querySelector('.admin-household-retry-label-idle')
    const stack = el.querySelector('.admin-household-retry-label-stack')
    if (!idle || !stack) return false
    const range = document.createRange()
    range.selectNodeContents(idle)
    const text = range.getBoundingClientRect()
    const stackBox = stack.getBoundingClientRect()
    const leftSpace = text.left - stackBox.left
    const rightSpace = stackBox.right - text.right
    return Math.abs(leftSpace - rightSpace) <= 1
  }), 'Idle Try again text is centered in the label cell').resolves.toBe(true)
  if (captureScreenshots) {
    await tryAgain.screenshot({ path: testInfo.outputPath('try-again-idle.png') })
  }
  const clickPromise = tryAgain.click()
  const busyButton = section.getByRole('button', { name: 'Trying again…' })
  await expect(busyButton).toHaveAccessibleName('Trying again…')
  await expect(busyButton).toHaveAttribute('data-loading', 'true')
  await expect(busyButton).toHaveAttribute('aria-busy', 'true')
  await expect(busyButton).toBeDisabled()
  await expect(busyButton.locator('.admin-household-retry-label-stack span').first()).toHaveAttribute('aria-hidden', 'true')
  const activeBusyLabel = busyButton.locator('.admin-household-retry-label-busy')
  await expect(activeBusyLabel.evaluate((el) => {
    const style = getComputedStyle(el)
    return { opacity: style.opacity, visibility: style.visibility }
  })).resolves.toEqual({ opacity: '1', visibility: 'visible' })
  await expect(section.getByRole('alert')).toHaveText('Something went wrong. Try again.')
  const busyBox = await busyButton.boundingBox()
  expect(busyBox).not.toBeNull()
  expect(Math.abs(idleBox!.width - busyBox!.width)).toBeLessThanOrEqual(0.5)
  await expect(busyButton.evaluate((el) => el.scrollWidth <= el.clientWidth + 0.5)).resolves.toBe(true)
  expect(touchTargetDimensionMeetsMinimum(busyBox!.height)).toBe(true)
  if (captureScreenshots) {
    await busyButton.screenshot({ path: testInfo.outputPath('try-again-busy.png') })
  }
  return clickPromise
}

for (const { viewportWidth, viewportHeight, font125, captureScreenshots } of [
  { viewportWidth: 390, viewportHeight: 844, font125: false, captureScreenshots: true },
  { viewportWidth: 1280, viewportHeight: 800, font125: false, captureScreenshots: true },
  { viewportWidth: 320, viewportHeight: 844, font125: true, captureScreenshots: false },
] as const) {
  test(`admin households Try again sizing at ${viewportWidth}x${viewportHeight}${font125 ? ' with 125% text' : ''}`, async ({ page }, testInfo) => {
    await openAdmin(page, viewportWidth, viewportHeight)
    const harness = await installTryAgainRetryRoute(page)
    if (font125) {
      await page.addStyleTag({ content: 'html { font-size: 125% !important; }' })
    }
    await page.goto('/households')
    const section = page.getByTestId('operator-households-section')
    await expect(section.getByRole('alert')).toHaveText('Something went wrong. Try again.')
    harness.unlockRetry()
    const clickPromise = await assertTryAgainButtonSizing(section, testInfo, captureScreenshots)
    harness.releaseRetry()
    await clickPromise
  })
}

test('admin households select a restock radio when tapping the label padding', async ({ page }) => {
  await openAdmin(page, 390, 844)
  await page.goto('/households')
  const row = page.locator('.households-admin-page .mantine-Radio-root', { hasText: 'Ask before restocking new items' })
  const body = row.locator('.mantine-Radio-body')
  await expect(body).toBeVisible()
  const box = await body.boundingBox()
  expect(box).not.toBeNull()
  await page.mouse.click(box!.x + box!.width / 2, box!.y + box!.height - 10)
  await expect(page.getByRole('radio', { name: 'Ask before restocking new items' })).toBeChecked()
  await assertRadioRowTapPoints(page, '.households-admin-page', 'Ask before restocking new items')
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
