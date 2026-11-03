import AxeBuilder from '@axe-core/playwright'
import { expect, test, type Page } from '@playwright/test'
import { install, resetStage, stage } from './householdStageFixtures'
import { resetSharedState } from './helpers'
import { krogerAppCopy } from '@stockbot/shared/krogerAppCopy'

const MIN_TOUCH = 44
const ROOT_FONT_PX_AT_100 = 15

function expectedRootFontPx(fontPercent: number) {
  if (fontPercent === 100) return ROOT_FONT_PX_AT_100
  if (fontPercent === 125) return 20
  return (ROOT_FONT_PX_AT_100 * fontPercent) / 100
}

async function installRootFontPercent(page: Page, fontPercent: number) {
  if (fontPercent === 100) return
  await page.addInitScript((percent) => {
    const apply = () => {
      document.documentElement.style.setProperty('font-size', `${percent}%`, 'important')
    }
    if (document.readyState === 'loading') {
      document.addEventListener('DOMContentLoaded', apply, { once: true })
    }
    apply()
  }, fontPercent)
}

async function assertRootFontSizePx(page: Page, expectedPx: number) {
  await expect.poll(async () => {
    const px = await page.evaluate(() => parseFloat(getComputedStyle(document.documentElement).fontSize))
    return Math.abs(px - expectedPx) <= 0.5
  }).toBe(true)
}

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

async function stubShopping(page: Page, displayName: string, entries: unknown[] = [{
  itemId: 'item-1',
  quantity: 1,
  item: { id: 'item-1', product: { name: 'Plain rice', category: 'Dry goods' } },
  basis: { instanceCount: 1 },
}]) {
  await page.route('**/api/shopping-list**', async (route) => {
    const incoming = route.request()
    if (incoming.method() !== 'GET' || new URL(incoming.url()).pathname !== '/api/shopping-list') {
      await route.continue()
      return
    }
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify(entries),
    })
  })
  await page.route('**/api/restock', (route) => route.fulfill({ status: 200, contentType: 'application/json', body: 'null' }))
  await page.route('**/api/inventory', (route) => route.fulfill({ status: 200, contentType: 'application/json', body: '[]' }))
  await page.route('**/api/providers', (route) => route.fulfill({
    status: 200,
    contentType: 'application/json',
    body: JSON.stringify(krogerProviders('saved', 'connected', displayName)),
  }))
  await page.route('**/api/group-suggestions**', async (route) => {
    const incoming = route.request()
    const url = new URL(incoming.url())
    if (incoming.method() === 'GET' && url.pathname === '/api/group-suggestions') {
      await route.fulfill({ status: 200, contentType: 'application/json', body: '[]' })
      return
    }
    await route.continue()
  })
}

async function openShoppingToolbar(
  page: Page,
  viewportWidth: number,
  fontPercent: number,
  scheme: 'light' | 'dark',
) {
  await page.addInitScript((value) => {
    window.localStorage.setItem('mantine-color-scheme-value', value)
  }, scheme)
  await installRootFontPercent(page, fontPercent)
  await page.setViewportSize({ width: viewportWidth, height: 844 })
  await page.goto('/shopping')
  await expect.poll(() => page.evaluate(() => document.documentElement.getAttribute('data-mantine-color-scheme'))).toBe(scheme)
  if (fontPercent !== 100) {
    await assertRootFontSizePx(page, expectedRootFontPx(fontPercent))
  } else {
    await assertRootFontSizePx(page, ROOT_FONT_PX_AT_100)
  }
}

async function stubShoppingWithBlockedSend(
  page: Page,
  displayName: string,
  entries: unknown[] = [{
    itemId: 'item-1',
    quantity: 1,
    item: { id: 'item-1', product: { name: 'Plain rice', category: 'Dry goods' } },
    basis: { instanceCount: 1 },
  }],
) {
  let resolveCalls = 0
  await stubShopping(page, displayName, entries)
  await page.route('**/api/shopping-list/items/**/unknown-resolution**', async (route) => {
    resolveCalls += 1
    await route.fulfill({
      status: resolveCalls === 1 ? 500 : 200,
      contentType: 'application/json',
      body: JSON.stringify(resolveCalls === 1 ? { error: 'boom', code: 'internal' } : { entryId: 'item-1' }),
    })
  })
  await page.route('**/api/shopping-list**', async (route) => {
    const incoming = route.request()
    const url = new URL(incoming.url())
    if (incoming.method() === 'POST' && url.pathname === '/api/shopping-list/export') {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          provider: 'kroger',
          exported: 0,
          results: [{
            entryId: 'a',
            itemId: 'item-1',
            name: 'Plain rice',
            quantity: 1,
            status: 'unknown',
            reason: 'no_per_item_result',
          }],
        }),
      })
      return
    }
    if (incoming.method() === 'GET' && url.pathname === '/api/shopping-list') {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify(entries),
      })
      return
    }
    await route.continue()
  })
}

async function triggerRealBlockedSendHint(page: Page) {
  await page.getByRole('button', { name: /Send to/ }).click()
  const panel = page.getByRole('status', { name: 'Cart send result' })
  await expect(panel).toBeVisible()
  await panel.getByRole('button', { name: /in my cart/i }).click()
  await page.getByRole('button', { name: /Send to/ }).click()
  await expect(page.locator('.provision-blocked-send-hint')).toHaveText("One item didn't save. Fix it below, then send again.")
}

async function assertSingleHouseholdEllipsis(page: Page, fullName: string) {
  const button = page.getByRole('button', { name: `Household: ${fullName}` })
  await expect(button).toBeVisible()
  await expect(button).toHaveAttribute('title', fullName)
  const nameText = button.locator('.household-switch-name-text')
  await expect(nameText).toHaveText(fullName)
  await expect.poll(async () => nameText.evaluate((el) => {
    const style = getComputedStyle(el)
    return el.scrollWidth > el.clientWidth + 0.5
      && style.textOverflow === 'ellipsis'
      && style.whiteSpace === 'nowrap'
  })).toBe(true)
  await expect(page.evaluate(() => document.documentElement.scrollWidth <= document.documentElement.clientWidth + 0.5)).resolves.toBe(true)
}

async function assertShortHouseholdHugsText(page: Page, householdName: string) {
  const button = page.getByRole('button', { name: `Household: ${householdName}` })
  await expect(button).toBeVisible()
  await expect(button).toHaveAttribute('title', householdName)
  const nameText = button.locator('.household-switch-name-text')
  await expect(nameText).toHaveText(householdName)
  const metrics = await page.evaluate(() => {
    const switchButton = document.querySelector('.app-header .household-switch-name') as HTMLElement | null
    const text = document.querySelector('.app-header .household-switch-name-text') as HTMLElement | null
    const wordmark = document.querySelector('.app-header-wordmark') as HTMLElement | null
    if (!switchButton || !text || !wordmark) return null
    const buttonStyle = getComputedStyle(switchButton)
    const textStyle = getComputedStyle(text)
    const buttonBox = switchButton.getBoundingClientRect()
    const wordmarkBox = wordmark.getBoundingClientRect()
    const paddingX = parseFloat(buttonStyle.paddingLeft) + parseFloat(buttonStyle.paddingRight)
    const canvas = document.createElement('canvas')
    const ctx = canvas.getContext('2d')
    const intrinsicTextWidth = ctx
      ? (ctx.font = textStyle.font, ctx.measureText(text.textContent ?? '').width)
      : text.scrollWidth
    return {
      buttonWidth: buttonBox.width,
      intrinsicTextWidth,
      paddingX,
      gapToWordmark: buttonBox.left - wordmarkBox.right,
      hasEllipsis: text.scrollWidth > text.clientWidth + 0.5,
    }
  })
  expect(metrics).not.toBeNull()
  expect(metrics!.hasEllipsis).toBe(false)
  const maxContentWidth = metrics!.intrinsicTextWidth + metrics!.paddingX + 2
  expect(metrics!.buttonWidth).toBeLessThanOrEqual(maxContentWidth)
  expect(metrics!.gapToWordmark).toBeGreaterThanOrEqual(-0.5)
  expect(metrics!.gapToWordmark).toBeLessThanOrEqual(12)
}

async function openSingleHouseholdHeader(
  page: Page,
  householdName: string,
  viewportWidth: number,
  fontPercent: number,
  scheme: 'light' | 'dark',
) {
  resetStage()
  stage.kind = 'account'
  stage.operator = false
  stage.role = 'owner'
  stage.householdName = householdName
  await install(page)
  await page.route('**/api/auth/session', async (route) => {
    if (route.request().method() !== 'GET') return route.continue()
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({
        user: { id: 'user-1', email: 'ada@example.com', name: 'Ada' },
        kind: 'account',
        memberships: [{ householdId: 'hh-1', name: householdName, role: 'owner', deletedAt: null }],
        households: [{ id: 'hh-1', name: householdName, role: 'owner', deletedAt: null }],
        activeHouseholdId: 'hh-1',
        operator: false,
      }),
    })
  }, { times: Number.POSITIVE_INFINITY })
  await stubShopping(page, 'Kroger')
  await page.addInitScript((value) => {
    window.localStorage.setItem('mantine-color-scheme-value', value)
  }, scheme)
  await installRootFontPercent(page, fontPercent)
  await page.setViewportSize({ width: viewportWidth, height: 844 })
  await page.goto('/shopping')
  await expect.poll(() => page.evaluate(() => document.documentElement.getAttribute('data-mantine-color-scheme'))).toBe(scheme)
  if (fontPercent !== 100) {
    await assertRootFontSizePx(page, expectedRootFontPx(fontPercent))
  } else {
    await assertRootFontSizePx(page, ROOT_FONT_PX_AT_100)
  }
}

test.beforeAll(async ({ request }) => {
  await resetSharedState(request)
})

test('320px with 125% text keeps the single-household name on one line', async ({ page }) => {
  const householdName = 'Test Household Pantry Storage'
  await openSingleHouseholdHeader(page, householdName, 320, 125, 'light')
  await assertSingleHouseholdEllipsis(page, householdName)
})

const householdEllipsisCases = [
  { viewportWidth: 390, fontPercent: 100, scheme: 'light' as const, householdName: 'Pantry Storage Room Long Name' },
  { viewportWidth: 390, fontPercent: 100, scheme: 'dark' as const, householdName: 'Pantry Storage Room Long Name' },
] as const

for (const { viewportWidth, fontPercent, scheme, householdName } of householdEllipsisCases) {
  test(`${viewportWidth}px with ${fontPercent}% text ellipsizes the single-household name (${scheme})`, async ({ page }) => {
    await openSingleHouseholdHeader(page, householdName, viewportWidth, fontPercent, scheme)
    await assertSingleHouseholdEllipsis(page, householdName)
  })
}

const shortHouseholdNameCases = [
  { viewportWidth: 320, fontPercent: 125, scheme: 'light' as const },
  { viewportWidth: 390, fontPercent: 100, scheme: 'light' as const },
  { viewportWidth: 767, fontPercent: 100, scheme: 'light' as const },
] as const

for (const { viewportWidth, fontPercent, scheme } of shortHouseholdNameCases) {
  test(`${viewportWidth}px with ${fontPercent}% text keeps a short household name hugging the wordmark (${scheme})`, async ({ page }) => {
    await openSingleHouseholdHeader(page, 'Pantry', viewportWidth, fontPercent, scheme)
    await assertShortHouseholdHugsText(page, 'Pantry')
  })
}

async function assertShoppingToolbarStacked(page: Page, viewportWidth: number) {
  const toolbar = page.locator('.shopping-toolbar')
  await expect(toolbar).toBeVisible()
  const send = page.getByRole('button', { name: /Send to/ })
  const fill = page.getByRole('button', { name: /Update the list|Build the list/ })
  await expect(send).toBeVisible()
  await expect(fill).toBeVisible()

  const toolbarBox = await toolbar.boundingBox()
  expect(toolbarBox).not.toBeNull()
  const areas = await toolbar.evaluate((el) => getComputedStyle(el).gridTemplateAreas)
  expect(areas).not.toMatch(/fill send/)

  const fillBox = await fill.boundingBox()
  const sendBox = await send.boundingBox()
  expect(fillBox).not.toBeNull()
  expect(sendBox).not.toBeNull()
  expect(fillBox!.width).toBeGreaterThanOrEqual(toolbarBox!.width - 4)
  expect(sendBox!.width).toBeGreaterThanOrEqual(toolbarBox!.width - 4)
  expect(sendBox!.y).toBeGreaterThan(fillBox!.y + fillBox!.height - 2)

  for (const button of [fill, send]) {
    const labels = button.locator('.mantine-Button-label')
    await expect(labels).toHaveCount(1)
    await expect(labels.first().evaluate((el) => el.scrollWidth <= el.clientWidth + 1)).resolves.toBe(true)
    const box = await button.boundingBox()
    expect(box).not.toBeNull()
    expect(box!.x).toBeGreaterThanOrEqual(0)
    expect(box!.x + box!.width).toBeLessThanOrEqual(viewportWidth + 0.5)
  }
}

const toolbarLayoutCases = [
  { viewportWidth: 320, fontPercent: 125, scheme: 'light' },
  { viewportWidth: 320, fontPercent: 125, scheme: 'dark' },
  { viewportWidth: 359, fontPercent: 125, scheme: 'light' },
  { viewportWidth: 359, fontPercent: 125, scheme: 'dark' },
  { viewportWidth: 359, fontPercent: 100, scheme: 'light' },
  { viewportWidth: 359, fontPercent: 100, scheme: 'dark' },
] as const

for (const { viewportWidth, fontPercent, scheme } of toolbarLayoutCases) {
  for (const state of ['filled list', 'empty list notice', 'blocked send hint'] as const) {
    test(`shopping toolbar stacks (${state}) at ${viewportWidth}px ${fontPercent}% ${scheme}`, async ({ page }) => {
      if (state === 'blocked send hint') {
        await stubShoppingWithBlockedSend(page, 'Kroger')
      } else {
        await stubShopping(page, 'Kroger', state === 'empty list notice' ? [] : undefined)
      }
      await openShoppingToolbar(page, viewportWidth, fontPercent, scheme)
      if (state === 'empty list notice') {
        await expect(page.getByText(/Build the list before sending/)).toBeVisible()
      }
      if (state === 'blocked send hint') {
        await triggerRealBlockedSendHint(page)
      }
      await assertShoppingToolbarStacked(page, viewportWidth)
    })
  }
}

for (const scheme of ['light', 'dark'] as const) {
  test(`shopping toolbar stacks blocked hint and notices at 320px 125% (${scheme})`, async ({ page }) => {
    await stubShoppingWithBlockedSend(page, 'Kroger')
    await openShoppingToolbar(page, 320, 125, scheme)
    await triggerRealBlockedSendHint(page)
    await expect(page.locator('.provision-notices')).toBeVisible()
    await expect(page.locator('.provision-blocked-send-hint')).toBeVisible()
    await assertShoppingToolbarStacked(page, 320)
  })
}

for (const { scheme, fontPercent } of [
  { scheme: 'light' as const, fontPercent: 100 },
  { scheme: 'dark' as const, fontPercent: 100 },
  { scheme: 'light' as const, fontPercent: 125 },
  { scheme: 'dark' as const, fontPercent: 125 },
]) {
  test(`header menu fits the viewport at 320px ${fontPercent}% (${scheme})`, async ({ page }) => {
    if (fontPercent === 125) {
      test.setTimeout(60_000)
    }
    await stubShopping(page, 'Kroger')
    if (fontPercent === 100) {
      await page.addInitScript((value) => {
        window.localStorage.setItem('mantine-color-scheme-value', value)
      }, scheme)
      await page.setViewportSize({ width: 320, height: 844 })
      await page.goto('/shopping')
      await expect.poll(() => page.evaluate(() => document.documentElement.getAttribute('data-mantine-color-scheme'))).toBe(scheme)
      await assertRootFontSizePx(page, ROOT_FONT_PX_AT_100)
    } else {
      await openShoppingToolbar(page, 320, fontPercent, scheme)
    }
    await page.keyboard.press('Escape')
    await page.locator('.app-menu-button').click()
    const menu = page.locator('.app-menu-dropdown')
    await expect(menu).toBeVisible()
    const box = await menu.boundingBox()
    expect(box).not.toBeNull()
    if (fontPercent === 125) {
      const innerWidth = await page.evaluate(() => window.innerWidth)
      expect(box!.x).toBeGreaterThanOrEqual(-0.5)
      expect(box!.x + box!.width).toBeLessThanOrEqual(innerWidth + 0.5)
    } else {
      expect(box!.width).toBeLessThanOrEqual(320.5)
      expect(box!.x + box!.width).toBeLessThanOrEqual(320.5)
    }
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

async function stubStoreAppHousehold(page: Page, appBody: Record<string, unknown>) {
  await page.route('**/api/households/hh-1/kroger-app', (route) => {
    if (route.request().method() === 'GET') {
      return route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify(appBody) })
    }
    return route.continue()
  })
  await page.route('**/api/households/hh-1', (route) => route.fulfill({
    status: 200,
    contentType: 'application/json',
    body: JSON.stringify({ id: 'hh-1', name: stage.householdName, role: 'owner', deletedAt: null, purgeAt: null }),
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

test('household admin store app setup buttons meet 44px touch targets', async ({ page }) => {
  resetStage()
  stage.kind = 'account'
  stage.role = 'owner'
  stage.householdName = 'Test Household'
  await install(page)
  await stubStoreAppHousehold(page, {
    configured: false,
    clientIdLast4: null,
    redirectUri: 'https://test-household.example/cb',
    modality: 'PICKUP',
    productAccess: 'unknown',
    status: 'not_set_up',
    canManage: true,
  })
  await page.setViewportSize({ width: 390, height: 844 })
  await page.goto('/household')
  await assertTouchTargetSize(page.getByRole('button', { name: krogerAppCopy.rowSetUp }))
  await page.getByRole('button', { name: krogerAppCopy.rowSetUp }).click()
  const dialog = page.getByRole('dialog', { name: krogerAppCopy.title })
  await expect(dialog).toBeVisible()
  await assertTouchTargetSize(page.getByRole('button', { name: krogerAppCopy.closeLabel }))
  const axe = await new AxeBuilder({ page })
    .include('[role="dialog"]')
    .withRules(['color-contrast'])
    .analyze()
  expect(axe.violations).toEqual([])
})

test('store app remove error keeps the confirm label visible while loading at 390px', async ({ page }) => {
  resetStage()
  stage.kind = 'account'
  stage.householdName = 'Test Household'
  stage.role = 'owner'
  await install(page)
  let calls = 0
  await stubStoreAppHousehold(page, {
    configured: true,
    clientIdLast4: '1234',
    redirectUri: 'https://test-household.example/cb',
    modality: 'PICKUP',
    productAccess: 'unknown',
    status: 'connected',
    canManage: true,
  })
  await page.route('**/api/households/hh-1/kroger-app', (route) => {
    if (route.request().method() === 'DELETE') {
      calls += 1
      return route.fulfill({
        status: calls === 1 ? 500 : 200,
        contentType: 'application/json',
        body: calls === 1
          ? JSON.stringify({ error: 'remove failed', code: 'internal' })
          : JSON.stringify({
            configured: false,
            clientIdLast4: null,
            redirectUri: 'https://test-household.example/cb',
            modality: 'PICKUP',
            productAccess: 'unknown',
            status: 'not_set_up',
            canManage: true,
          }),
      })
    }
    if (route.request().method() === 'GET') {
      return route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          configured: true,
          clientIdLast4: '1234',
          redirectUri: 'https://test-household.example/cb',
          modality: 'PICKUP',
          productAccess: 'unknown',
          status: 'connected',
          canManage: true,
        }),
      })
    }
    return route.continue()
  })
  await page.setViewportSize({ width: 390, height: 844 })
  await page.goto('/household')
  await page.getByRole('button', { name: krogerAppCopy.menuMore }).click()
  await page.getByRole('menuitem', { name: krogerAppCopy.menuRemove }).click()
  const dialog = page.getByRole('dialog', { name: krogerAppCopy.rmTitle })
  const confirm = dialog.getByRole('button', { name: krogerAppCopy.rmConfirm })
  await confirm.click()
  await expect(dialog.getByRole('alert')).toHaveText(krogerAppCopy.rmFailed)
  const secondAttempt = confirm.click()
  await expect(confirm).toContainText(krogerAppCopy.rmBusy)
  await secondAttempt
  await expect.poll(() => calls).toBeGreaterThanOrEqual(2)
})
