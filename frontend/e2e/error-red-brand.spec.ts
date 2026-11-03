import { expect, test, type Page } from '@playwright/test'
import { install, resetStage, stage } from './householdStageFixtures'

const RED_FLOOR_RGB = 'rgb(224, 49, 49)'
const RED_DARK_CEILING_RGB = 'rgb(250, 82, 82)'
const RED_TOO_LIGHT_RGB = 'rgb(255, 135, 135)'

function channels(color: string): [number, number, number] {
  const match = color.match(/rgba?\((\d+),\s*(\d+),\s*(\d+)/)
  if (!match) throw new Error(`Unparsed color: ${color}`)
  return [Number(match[1]), Number(match[2]), Number(match[3])]
}

function relativeLuminance(color: string): number {
  const lin = (channel: number) => {
    const value = channel / 255
    return value <= 0.04045 ? value / 12.92 : ((value + 0.055) / 1.055) ** 2.4
  }
  const [red, green, blue] = channels(color)
  return 0.2126 * lin(red) + 0.7152 * lin(green) + 0.0722 * lin(blue)
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
    await route.fulfill({ status: 200, contentType: 'application/json', body: '[]' })
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
}

async function openThemed(page: Page, scheme: 'light' | 'dark') {
  await page.addInitScript((value) => {
    window.localStorage.setItem('mantine-color-scheme-value', value)
  }, scheme)
  await page.setViewportSize({ width: 390, height: 844 })
  await page.goto('/shopping')
  await expect.poll(() => page.evaluate(() => document.documentElement.getAttribute('data-mantine-color-scheme'))).toBe(scheme)
}

async function showProviderPanelError(page: Page) {
  await page.route('**/api/providers/kroger/ledger/reset', async (route) => {
    if (route.request().method() !== 'POST') return route.continue()
    await route.fulfill({
      status: 500,
      contentType: 'application/json',
      body: JSON.stringify({ error: 'Unable to forget what was already sent.', code: 'internal' }),
    })
  })
  await expect(page.getByRole('button', { name: 'Start a new cart' })).toBeVisible()
  await page.getByRole('button', { name: 'Start a new cart' }).click()
  const alert = page.getByRole('alert').filter({ hasText: 'Unable to forget what was already sent.' })
  await expect(alert).toBeVisible()
  return alert
}

async function expectErrorInk(alert: ReturnType<Page['getByRole']>, scheme: 'light' | 'dark') {
  const sample = await alert.evaluate((el) => {
    const foreground = getComputedStyle(el).color
    let node: Element | null = el
    while (node) {
      const bg = getComputedStyle(node).backgroundColor
      const alpha = bg.match(/rgba\(\d+,\s*\d+,\s*\d+,\s*([\d.]+)\)/)
      if (bg !== 'rgba(0, 0, 0, 0)' && bg !== 'transparent' && (!alpha || Number(alpha[1]) > 0.05)) {
        return { foreground, background: bg }
      }
      node = node.parentElement
    }
    return { foreground, background: getComputedStyle(document.body).backgroundColor }
  })
  expect(contrast(sample.foreground, sample.background)).toBeGreaterThanOrEqual(4.5)
  if (scheme === 'light') {
    expect(relativeLuminance(sample.foreground)).toBeLessThanOrEqual(relativeLuminance(RED_FLOOR_RGB) + 1e-6)
  } else {
    expect(sample.foreground).not.toBe(RED_TOO_LIGHT_RGB)
    expect(relativeLuminance(sample.foreground)).toBeLessThanOrEqual(relativeLuminance(RED_DARK_CEILING_RGB) + 1e-6)
  }
}

for (const scheme of ['light', 'dark'] as const) {
  test(`provider panel error ink stays readable in ${scheme} mode`, async ({ page }) => {
    resetStage()
    stage.kind = 'account'
    stage.householdName = 'Test Household'
    stage.operator = false
    stage.role = 'member'
    await install(page)
    await stubShopping(page)
    await openThemed(page, scheme)
    const alert = await showProviderPanelError(page)
    await expectErrorInk(alert, scheme)
  })
}
