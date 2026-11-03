import { expect, test, type Page } from '@playwright/test'
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

test.beforeAll(async ({ request }) => {
  await resetSharedState(request)
})

for (const scheme of ['light', 'dark'] as const) {
  test(`keyboard focus indicators meet 3:1 on button, link, and input (${scheme})`, async ({ page }) => {
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

    await page.goto('/inventory')
    const search = page.getByRole('textbox', { name: 'Search products or groups' })
    await expect(search).toBeVisible()
    const inputFocus = await focusContrast(page, search)
    expect(ratioOf(inputFocus.indicator, inputFocus.background)).toBeGreaterThanOrEqual(3)
  })
}
