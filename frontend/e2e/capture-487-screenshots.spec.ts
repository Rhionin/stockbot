import { expect, test, type Page } from '@playwright/test'
import { mkdirSync } from 'node:fs'
import { join } from 'node:path'
import { install, resetStage, stage } from './householdStageFixtures'

const outDir = process.env.SCREENSHOT_DIR ?? join(process.cwd(), 'test-results', '487-screenshots')

const krogerProviders = () => ([
  {
    id: 'kroger',
    displayName: "Smith's",
    capabilities: {
      auth: 'oauth2_authorization_code',
      delivery: 'server_push',
      confirmation: 'per_request',
      mutation: 'add_only',
      identity: 'derived',
    },
    connectionState: 'connected' as const,
    credentialsConfigured: true,
    credentials: {
      clientId: 'fixture-client',
      redirectUri: 'https://test-household.example/cb',
      modality: 'PICKUP',
      secretSet: true,
      source: 'saved' as const,
    },
  },
])

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
    body: JSON.stringify(krogerProviders()),
  }))
}

async function singleHouseholdSession(page: Page, name: string) {
  await page.route('**/api/auth/session', async (route) => {
    if (route.request().method() !== 'GET') return route.continue()
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({
        user: { id: 'user-1', email: 'ada@example.com', name: 'Ada' },
        kind: 'account',
        memberships: [{ householdId: 'hh-1', name, role: 'owner', deletedAt: null }],
        households: [{ id: 'hh-1', name, role: 'owner', deletedAt: null }],
        activeHouseholdId: 'hh-1',
        operator: false,
      }),
    })
  })
}

const shots = [
  { file: '390-light.png', width: 390, height: 844, scheme: 'light' as const, font125: false },
  { file: '390-dark.png', width: 390, height: 844, scheme: 'dark' as const, font125: false },
  { file: '320-125-light.png', width: 320, height: 844, scheme: 'light' as const, font125: true },
  { file: '320-125-dark.png', width: 320, height: 844, scheme: 'dark' as const, font125: true },
  { file: '1280-light.png', width: 1280, height: 800, scheme: 'light' as const, font125: false },
  { file: '1280-dark.png', width: 1280, height: 800, scheme: 'dark' as const, font125: false },
]

test.beforeAll(() => {
  mkdirSync(outDir, { recursive: true })
})

for (const shot of shots) {
  test(`capture ${shot.file}`, async ({ page }) => {
    resetStage()
    stage.kind = 'account'
    stage.householdName = 'Test Household Pantry Storage'
    await install(page)
    await singleHouseholdSession(page, stage.householdName)
    await stubShopping(page)
    await page.addInitScript((scheme) => {
      window.localStorage.setItem('mantine-color-scheme-value', scheme)
    }, shot.scheme)
    if (shot.font125) {
      await page.addInitScript(() => {
        const apply = () => {
          document.documentElement.style.setProperty('font-size', '125%', 'important')
        }
        if (document.readyState === 'loading') {
          document.addEventListener('DOMContentLoaded', apply, { once: true })
        }
        apply()
      })
    }
    await page.setViewportSize({ width: shot.width, height: shot.height })
    await page.goto('/shopping')
    if (shot.font125) {
      await expect.poll(async () => {
        const px = await page.evaluate(() => parseFloat(getComputedStyle(document.documentElement).fontSize))
        return Math.abs(px - 20) <= 0.5
      }).toBe(true)
    }
    await expect(page.getByRole('button', { name: /Send to Smith's/ })).toBeVisible()
    await page.screenshot({ path: join(outDir, shot.file), fullPage: false })
  })
}

const headerShots = [
  { file: 'header-320-125-light.png', width: 320, height: 844, scheme: 'light' as const, font125: true },
  { file: 'header-320-125-dark.png', width: 320, height: 844, scheme: 'dark' as const, font125: true },
  { file: 'header-390-light.png', width: 390, height: 844, scheme: 'light' as const, font125: false },
  { file: 'header-390-dark.png', width: 390, height: 844, scheme: 'dark' as const, font125: false },
] as const

for (const shot of headerShots) {
  test(`capture header ${shot.file}`, async ({ page }) => {
    resetStage()
    stage.kind = 'account'
    stage.householdName = 'Test Household Pantry Storage'
    await install(page)
    await singleHouseholdSession(page, stage.householdName)
    await stubShopping(page)
    await page.addInitScript((scheme) => {
      window.localStorage.setItem('mantine-color-scheme-value', scheme)
    }, shot.scheme)
    if (shot.font125) {
      await page.addInitScript(() => {
        const apply = () => {
          document.documentElement.style.setProperty('font-size', '125%', 'important')
        }
        if (document.readyState === 'loading') {
          document.addEventListener('DOMContentLoaded', apply, { once: true })
        }
        apply()
      })
    }
    await page.setViewportSize({ width: shot.width, height: shot.height })
    await page.goto('/shopping')
    if (shot.font125) {
      await expect.poll(async () => {
        const px = await page.evaluate(() => parseFloat(getComputedStyle(document.documentElement).fontSize))
        return Math.abs(px - 20) <= 0.5
      }).toBe(true)
    }
    await expect(page.locator('.app-header .household-switch-name-text')).toBeVisible()
    await page.locator('.app-header').screenshot({ path: join(outDir, shot.file) })
  })
}
