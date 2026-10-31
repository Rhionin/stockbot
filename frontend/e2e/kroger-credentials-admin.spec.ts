import { expect, test, type Page } from '@playwright/test'
import { install, resetStage, stage } from './householdStageFixtures'

const krogerProviders = (source: 'none' | 'environment' | 'saved') => ([
  {
    id: 'kroger',
    displayName: 'Kroger',
    capabilities: {
      auth: 'oauth2_authorization_code',
      delivery: 'server_push',
      confirmation: 'per_request',
      mutation: 'add_only',
      identity: 'derived',
    },
    connectionState: 'disconnected',
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

const stubShoppingSurface = async (page: Page, providers: ReturnType<typeof krogerProviders>) => {
  await page.route('**/api/shopping-list**', (route) => route.fulfill({
    status: 200,
    contentType: 'application/json',
    body: '[]',
  }))
  await page.route('**/api/restock', (route) => route.fulfill({
    status: 200,
    contentType: 'application/json',
    body: 'null',
  }))
  await page.route('**/api/providers', (route) => route.fulfill({
    status: 200,
    contentType: 'application/json',
    body: JSON.stringify(providers),
  }))
}

test('server admin can open Kroger app keys from the menu', async ({ page }) => {
  await page.goto('/shopping')
  await page.getByRole('button', { name: 'Menu' }).click()
  await expect(page.getByRole('menuitem', { name: 'Manage Kroger connection' })).toBeVisible()
  await page.getByRole('menuitem', { name: 'Manage Kroger connection' }).click()
  await expect(page.getByRole('dialog', { name: 'Kroger connection' })).toBeVisible()
  await expect(page.getByRole('form', { name: 'Kroger credentials' })).toBeVisible()
})

test('Test Household member does not see Kroger app keys when source is none', async ({ page }) => {
  resetStage()
  stage.kind = 'account'
  stage.householdName = 'Test Household'
  stage.operator = false
  stage.role = 'member'
  await install(page)
  await stubShoppingSurface(page, krogerProviders('none'))
  await page.goto('/shopping')
  await page.getByRole('button', { name: 'Menu' }).click()
  await expect(page.getByRole('menuitem', { name: 'Manage Kroger connection' })).toHaveCount(0)
})
