import { expect, test, type Page } from '@playwright/test'
import { resetSharedState } from './helpers'

const TEAL_700 = '#0f766e'
const TEAL_700_RGB = 'rgb(15, 118, 110)'

function channels(color: string): [number, number, number] {
  const hex = color.match(/^#([0-9a-f]{6})$/i)
  if (hex) {
    return [
      Number.parseInt(hex[1].slice(0, 2), 16),
      Number.parseInt(hex[1].slice(2, 4), 16),
      Number.parseInt(hex[1].slice(4, 6), 16),
    ]
  }
  const match = color.match(/rgba?\((\d+),\s*(\d+),\s*(\d+)/)
  if (!match) throw new Error(`Unparsed color: ${color}`)
  return [Number(match[1]), Number(match[2]), Number(match[3])]
}

function contrast(foreground: string, background: string) {
  const lin = (channel: number) => {
    const value = channel / 255
    return value <= 0.04045 ? value / 12.92 : ((value + 0.055) / 1.055) ** 2.4
  }
  const lum = (rgb: [number, number, number]) => 0.2126 * lin(rgb[0]) + 0.7152 * lin(rgb[1]) + 0.0722 * lin(rgb[2])
  const hi = Math.max(lum(channels(foreground)), lum(channels(background)))
  const lo = Math.min(lum(channels(foreground)), lum(channels(background)))
  return (hi + 0.05) / (lo + 0.05)
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
  await page.route('**/api/restock', (route) => route.fulfill({
    status: 200,
    contentType: 'application/json',
    body: JSON.stringify({
      household: { kind: 'months', value: 3 },
      householdLabel: '3 mo',
      appliesTo: 0,
      itemCount: 0,
      items: [],
      categories: [],
      notRestocked: 2,
      suggestionCount: 0,
    }),
  }))
  await page.route('**/api/inventory', (route) => route.fulfill({ status: 200, contentType: 'application/json', body: '[]' }))
  await page.route('**/api/providers', (route) => route.fulfill({
    status: 200,
    contentType: 'application/json',
    body: JSON.stringify([{
      id: 'kroger',
      displayName: "Smith's",
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
}

async function openThemed(page: Page, path: string, scheme: 'light' | 'dark') {
  await page.addInitScript((value) => {
    window.localStorage.setItem('mantine-color-scheme-value', value)
  }, scheme)
  await page.goto(path)
  await expect.poll(() => page.evaluate(() => document.documentElement.getAttribute('data-mantine-color-scheme'))).toBe(scheme)
}

async function expectFilledPrimaryButton(page: Page, name: string | RegExp) {
  const button = page.getByRole('button', { name }).first()
  await expect(button).toBeVisible()
  const sample = await button.evaluate((el) => {
    const style = getComputedStyle(el)
    return { background: style.backgroundColor, foreground: style.color }
  })
  expect(sample.background, `filled primary background for ${String(name)}`).toBe(TEAL_700_RGB)
  expect(contrast(sample.foreground, sample.background)).toBeGreaterThanOrEqual(4.5)
}

async function expectTealFilledToken(page: Page) {
  const filled = await page.evaluate(() => {
    const root = getComputedStyle(document.documentElement)
    return root.getPropertyValue('--mantine-color-teal-filled').trim()
  })
  expect(filled.toLowerCase()).toBe(TEAL_700)
}

async function expectPrimaryLinkInk(page: Page, scheme: 'light' | 'dark') {
  const link = page.getByRole('link', { name: 'Review' })
  await expect(link).toBeVisible()
  const color = await link.evaluate((el) => getComputedStyle(el).color)
  if (scheme === 'light') {
    expect(color).toBe(TEAL_700_RGB)
  }
  const ratio = await link.evaluate((el) => {
    const fg = getComputedStyle(el).color
    let node: Element | null = el
    while (node) {
      const bg = getComputedStyle(node).backgroundColor
      const alpha = bg.match(/rgba\(\d+,\s*\d+,\s*\d+,\s*([\d.]+)\)/)
      if (bg !== 'rgba(0, 0, 0, 0)' && bg !== 'transparent' && (!alpha || Number(alpha[1]) > 0.05)) {
        return { fg, bg }
      }
      node = node.parentElement
    }
    return { fg, bg: getComputedStyle(document.body).backgroundColor }
  })
  expect(contrast(ratio.fg, ratio.bg)).toBeGreaterThanOrEqual(4.5)
}

async function openScanQueueAddScan(page: Page) {
  await page.getByRole('button', { name: 'Scan with camera' }).click()
  await expect(page.getByRole('alert').filter({ hasText: 'Camera scanning unavailable' })).toBeVisible()
}

test.beforeAll(async ({ request }) => {
  await resetSharedState(request)
})

for (const scheme of ['light', 'dark'] as const) {
  test(`filled primary buttons and teal link ink stay teal-700 in ${scheme} mode`, async ({ page }) => {
    await openThemed(page, '/', scheme)
    await expectTealFilledToken(page)
    await openScanQueueAddScan(page)
    await expectFilledPrimaryButton(page, 'Add scan')

    await stubShopping(page)
    await openThemed(page, '/shopping', scheme)
    await expectTealFilledToken(page)
    await expectFilledPrimaryButton(page, /Send to/)
    await expectPrimaryLinkInk(page, scheme)
  })
}
