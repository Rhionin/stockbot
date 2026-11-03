import { expect, test, type Locator, type Page } from '@playwright/test'
import { install, resetStage, stage } from './householdStageFixtures'
import { resetSharedState } from './helpers'

const ratioOf = (foreground: string, background: string) => {
  const parse = (value: string) => value.match(/[\d.]+/g)!.slice(0, 3).map(Number)
  const luminance = (channels: number[]) => {
    const linear = (unit: number) => (unit <= 0.04045 ? unit / 12.92 : ((unit + 0.055) / 1.055) ** 2.4)
    const [red, green, blue] = channels.map((channel) => linear(channel / 255))
    return 0.2126 * red + 0.7152 * green + 0.0722 * blue
  }
  const [lighter, darker] = [luminance(parse(foreground)), luminance(parse(background))].sort((a, b) => b - a)
  return (lighter + 0.05) / (darker + 0.05)
}

const allUnknownExport = {
  provider: 'kroger',
  exported: 0,
  results: [
    { entryId: 'a', itemId: '1', name: 'Oats', quantity: 1, status: 'unknown', reason: 'no_per_item_result' },
    { entryId: 'b', itemId: '2', name: 'Rice', quantity: 1, status: 'unknown' },
  ],
}

async function stubShopping(page: Page) {
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
    body: JSON.stringify([{
      id: 'kroger',
      displayName: 'Kroger',
      capabilities: {
        auth: 'oauth2_authorization_code',
        delivery: 'server_push',
        confirmation: 'per_request',
        mutation: 'add_only',
        identity: 'derived',
      },
      connectionState: 'connected',
      credentialsConfigured: true,
      credentials: {
        clientId: 'fixture-client',
        redirectUri: 'https://test-household.example/cb',
        modality: 'PICKUP',
        secretSet: true,
        source: 'saved',
      },
    }]),
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

async function installProvisionPanelMocks(page: Page) {
  await stubShopping(page)
  await page.route('**/api/shopping-list**', async (route) => {
    const incoming = route.request()
    const url = new URL(incoming.url())
    if (incoming.method() === 'GET' && url.pathname === '/api/shopping-list') {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify([{
          id: 'sli-test-1',
          itemId: 'item-test-1',
          quantity: 2,
          source: 'manual',
          purchasedAt: null,
        }]),
      })
      return
    }
    if (incoming.method() === 'POST' && url.pathname === '/api/shopping-list/export') {
      await route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify(allUnknownExport) })
      return
    }
    await route.continue()
  })
}

async function focusContrast(page: Page, target: ReturnType<Page['locator']>) {
  await target.focus()
  await expect(target).toBeFocused()
  return target.evaluate((el) => {
    let node: Element | null = el.parentElement
    let background = 'rgb(255, 255, 255)'
    for (; node; node = node.parentElement) {
      const color = getComputedStyle(node).backgroundColor
      if (!/rgba\(\s*\d+,\s*\d+,\s*\d+,\s*0\s*\)$/.test(color) && color !== 'transparent') {
        background = color
        break
      }
    }
    const style = getComputedStyle(el)
    const outlineVisible = style.outlineStyle !== 'none' && style.outlineWidth !== '0px'
    const indicator = outlineVisible ? style.outlineColor : style.borderColor
    return { indicator, background, outlineStyle: style.outlineStyle }
  })
}

async function stableFieldBorderContrast(page: Page, target: Locator) {
  await target.focus()
  await expect(target).toBeFocused()
  let metrics: { borderColor: string; fieldBackground: string } | null = null
  await expect.poll(async () => {
    const first = await target.evaluate((el) => getComputedStyle(el).borderColor)
    await page.waitForTimeout(120)
    const second = await target.evaluate((el) => getComputedStyle(el).borderColor)
    if (first !== second) return false
    const fieldBackground = await target.evaluate((el) => getComputedStyle(el).backgroundColor)
    metrics = { borderColor: second, fieldBackground }
    return true
  }, { timeout: 5000 }).toBe(true)
  expect(metrics).not.toBeNull()
  expect(ratioOf(metrics!.borderColor, metrics!.fieldBackground)).toBeGreaterThanOrEqual(3)
}

async function openHouseholdAndSettings(page: Page, scheme: 'light' | 'dark') {
  resetStage()
  stage.kind = 'account'
  stage.role = 'owner'
  stage.householdName = 'Pantry'
  await install(page)
  await page.route('**/api/settings/supply**', (route) => route.fulfill({
    status: 200,
    contentType: 'application/json',
    body: JSON.stringify({ months: 3, wipePhrase: 'WIPE INVENTORY' }),
  }))
  await page.route('**/api/settings/group-rule**', (route) => route.fulfill({
    status: 200,
    contentType: 'application/json',
    body: JSON.stringify({ rule: 'same_as_ran_out' }),
  }))
  await page.addInitScript((value) => {
    window.localStorage.setItem('mantine-color-scheme-value', value)
  }, scheme)
  await page.setViewportSize({ width: 1280, height: 900 })
}

async function checkRowFocusContrast(page: Page, row: Locator) {
  await row.focus()
  await expect(row).toBeFocused()
  const metrics = await row.evaluate((el) => {
    let node: Element | null = el
    let background = 'rgb(255, 255, 255)'
    for (; node; node = node.parentElement) {
      const color = getComputedStyle(node).backgroundColor
      if (!/rgba\(\s*\d+,\s*\d+,\s*\d+,\s*0\s*\)$/.test(color) && color !== 'transparent') {
        background = color
        break
      }
    }
    const style = getComputedStyle(el)
    return {
      indicator: style.outlineColor,
      outlineWidth: style.outlineWidth,
      outlineStyle: style.outlineStyle,
      background,
    }
  })
  expect(metrics.outlineStyle).not.toBe('none')
  expect(parseFloat(metrics.outlineWidth)).toBeGreaterThan(0)
  expect(ratioOf(metrics.indicator, metrics.background)).toBeGreaterThanOrEqual(3)
}

test.beforeAll(async ({ request }) => {
  await resetSharedState(request)
})

for (const scheme of ['light', 'dark'] as const) {
  test(`keyboard focus indicators meet 3:1 on button and link (${scheme})`, async ({ page }) => {
    await stubShopping(page)
    await page.addInitScript((value) => {
      window.localStorage.setItem('mantine-color-scheme-value', value)
    }, scheme)
    await page.setViewportSize({ width: 1280, height: 900 })
    await page.goto('/shopping')
    await expect.poll(() => page.evaluate(() => document.documentElement.getAttribute('data-mantine-color-scheme'))).toBe(scheme)

    const send = page.getByRole('button', { name: /Send to Kroger/ })
    await send.focus()
    await page.keyboard.press('Shift+Tab')
    await page.keyboard.press('Tab')
    const buttonFocus = await focusContrast(page, send)
    expect(buttonFocus.outlineStyle).not.toBe('none')
    expect(ratioOf(buttonFocus.indicator, buttonFocus.background)).toBeGreaterThanOrEqual(3)

    const inventoryLink = page.getByRole('link', { name: 'Inventory' })
    const linkFocus = await focusContrast(page, inventoryLink)
    expect(linkFocus.outlineStyle).not.toBe('none')
    expect(ratioOf(linkFocus.indicator, linkFocus.background)).toBeGreaterThanOrEqual(3)
  })

  test(`Mantine field focus border meets 3:1 on household TextInput and settings Select (${scheme})`, async ({ page }) => {
    await openHouseholdAndSettings(page, scheme)
    await page.goto('/household')
    await expect.poll(() => page.evaluate(() => document.documentElement.getAttribute('data-mantine-color-scheme'))).toBe(scheme)
    const householdName = page.getByRole('textbox', { name: 'Household name' })
    await expect(householdName).toBeVisible()
    await stableFieldBorderContrast(page, householdName)

    await page.goto('/settings')
    await expect(page.getByRole('heading', { name: 'Settings' })).toBeVisible()
    const groupRule = page.getByRole('combobox', { name: 'Default group rule' })
    await expect(groupRule).toBeEnabled()
    await expect(groupRule).toBeVisible()
    await stableFieldBorderContrast(page, groupRule)
  })
}

test('provision check row focus outline meets 3:1 in dark mode', async ({ page }) => {
  await installProvisionPanelMocks(page)
  await page.addInitScript((value) => {
    window.localStorage.setItem('mantine-color-scheme-value', value)
  }, 'dark')
  await page.setViewportSize({ width: 390, height: 844 })
  await page.goto('/shopping')
  await expect.poll(() => page.evaluate(() => document.documentElement.getAttribute('data-mantine-color-scheme'))).toBe('dark')
  await page.getByRole('button', { name: /Send to Kroger/ }).click()
  const panel = page.getByRole('status', { name: 'Cart send result' })
  await expect(panel).toBeVisible()
  const row = panel.locator('.provision-check-row', { hasText: 'Oats' })
  await expect(row).toBeVisible()
  await panel.getByRole('button', { name: "It's in my cart: Oats" }).click()
  await row.focus()
  await page.keyboard.press('Shift+Tab')
  await page.keyboard.press('Tab')
  await checkRowFocusContrast(page, row)
})
