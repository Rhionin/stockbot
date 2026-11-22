import { expect, test, type Page } from '@playwright/test'
import { krogerAppCopy } from '@stockbot/shared/krogerAppCopy'
import { routeShoppingStores } from './shoppingStoresFixture'

const householdName = 'Test Household'

const krogerProviderDisconnected = {
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
}

async function installShoppingCanConnectMocks(
  page: Page,
  options: { canConnect: boolean; role: 'owner' | 'member' },
) {
  await page.route('**/api/auth/session', async (route) => {
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({
        kind: 'account',
        user: { id: 'user-1', email: 'user@testhousehold.test', name: 'User' },
        memberships: [{ householdId: 'hh-test', name: householdName, role: options.role, deletedAt: null }],
        households: [{ id: 'hh-test', name: householdName, role: options.role, deletedAt: null }],
        activeHouseholdId: 'hh-test',
        operator: false,
      }),
    })
  })

  await page.route('**/api/providers', async (route) => {
    if (route.request().method() !== 'GET') {
      await route.continue()
      return
    }
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify([krogerProviderDisconnected]),
    })
  })

  await routeShoppingStores(page, { canConnect: options.canConnect, storeName: 'Kroger' })

  await page.route('**/api/shopping-list**', async (route) => {
    const url = route.request().url()
    if (route.request().method() === 'GET' && !url.includes('/export')) {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify([]),
      })
      return
    }
    await route.continue()
  })

  await page.route('**/api/inventory', async (route) => {
    await route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify([]) })
  })

  await page.route('**/api/restock', async (route) => {
    await route.fulfill({ status: 404, body: 'not found' })
  })
}

const adminConnectLine = krogerAppCopy.askAdminConnect.replaceAll('{store}', 'Kroger')

test('member sees ask-admin connect line instead of Connect on shopping', async ({ page }) => {
  await installShoppingCanConnectMocks(page, { canConnect: false, role: 'member' })
  await page.goto('/shopping')
  await expect(page.getByText(adminConnectLine)).toBeVisible()
  await expect(page.getByRole('button', { name: 'Connect' })).toHaveCount(0)
})

test('owner still sees Connect when canConnect is true', async ({ page }) => {
  await installShoppingCanConnectMocks(page, { canConnect: true, role: 'owner' })
  await page.goto('/shopping')
  await expect(page.getByRole('button', { name: 'Connect' })).toBeVisible()
  await expect(page.getByText(adminConnectLine)).toHaveCount(0)
})

test('owner still sees Connect when /api/stores fails', async ({ page }) => {
  await installShoppingCanConnectMocks(page, { canConnect: true, role: 'owner' })
  await page.route('**/api/stores', async (route) => {
    await route.fulfill({
      status: 500,
      contentType: 'application/json',
      body: JSON.stringify({ error: 'unavailable' }),
    })
  })
  await page.goto('/shopping')
  await expect(page.getByRole('button', { name: 'Connect' })).toBeVisible()
  await expect(page.getByText(adminConnectLine)).toHaveCount(0)
  await expect(page.getByText(krogerAppCopy.askAdminConnectFallback)).toHaveCount(0)
})

test('?kroger=not_allowed shows ask-admin notice and drops the param', async ({ page }) => {
  await installShoppingCanConnectMocks(page, { canConnect: false, role: 'member' })
  await page.goto('/shopping?kroger=not_allowed')
  const notice = page.getByText(krogerAppCopy.askAdminConnectFallback).or(page.getByText(adminConnectLine))
  await expect(notice).toBeVisible()
  await expect.poll(() => new URL(page.url()).searchParams.has('kroger')).toBe(false)
})

const screenshotViewports = [
  { width: 390, height: 844, label: '390' },
  { width: 1280, height: 800, label: '1280' },
] as const

for (const viewport of screenshotViewports) {
  test(`member canConnect=false screenshot @${viewport.label}`, async ({ page }) => {
    await page.setViewportSize(viewport)
    await installShoppingCanConnectMocks(page, { canConnect: false, role: 'member' })
    await page.goto('/shopping')
    await expect(page.getByText(adminConnectLine)).toBeVisible()
    await page.screenshot({
      path: `/opt/cursor/artifacts/shopping-member-can-connect-${viewport.label}.png`,
      fullPage: true,
    })
  })

  test(`owner canConnect=true screenshot @${viewport.label}`, async ({ page }) => {
    await page.setViewportSize(viewport)
    await installShoppingCanConnectMocks(page, { canConnect: true, role: 'owner' })
    await page.goto('/shopping')
    await expect(page.getByRole('button', { name: 'Connect' })).toBeVisible()
    await page.screenshot({
      path: `/opt/cursor/artifacts/shopping-owner-can-connect-${viewport.label}.png`,
      fullPage: true,
    })
  })
}