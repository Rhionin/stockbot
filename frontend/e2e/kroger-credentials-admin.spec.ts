import { expect, test, type Page } from '@playwright/test'
import { install, resetStage, stage } from './householdStageFixtures'

const krogerProviders = (
  source: 'none' | 'environment' | 'saved',
  connectionState: 'connected' | 'disconnected' = 'disconnected',
) => ([
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
  await expect(page.getByRole('menuitem', { name: 'Disconnect Kroger' })).toHaveCount(0)
})

test('Test Household non-admin in the primary household does not see Kroger app keys when source is saved', async ({ page }) => {
  resetStage()
  stage.kind = 'account'
  stage.householdName = 'Test Household'
  stage.operator = false
  stage.role = 'member'
  await install(page)
  await stubShoppingSurface(page, krogerProviders('saved'))
  await page.goto('/shopping')
  await page.getByRole('button', { name: 'Menu' }).click()
  await expect(page.getByRole('menuitem', { name: 'Look for more groups' })).toBeVisible()
  await expect(page.getByRole('menuitem', { name: 'Manage Kroger connection' })).toHaveCount(0)
})

test('Test Household non-admin in the primary household disconnects without app keys', async ({ page }) => {
  resetStage()
  stage.kind = 'account'
  stage.householdName = 'Test Household'
  stage.operator = false
  stage.role = 'member'
  await install(page)
  let disconnected = false
  await page.route('**/api/providers/kroger/connection', (route) => {
    if (route.request().method() === 'DELETE') {
      disconnected = true
      return route.fulfill({ status: 200, contentType: 'application/json', body: '{}' })
    }
    return route.continue()
  })
  await stubShoppingSurface(page, krogerProviders('saved', 'connected'))
  await page.goto('/shopping')
  await page.getByRole('button', { name: 'Menu' }).click()
  await expect(page.getByRole('menuitem', { name: 'Disconnect Kroger' })).toBeVisible()
  await expect(page.getByRole('menuitem', { name: 'Manage Kroger connection' })).toHaveCount(0)
  await page.getByRole('menuitem', { name: 'Disconnect Kroger' }).click()
  const dialog = page.getByRole('dialog', { name: 'Disconnect Kroger?' })
  await expect(dialog).toBeVisible()
  await expect(page.getByRole('form', { name: 'Kroger credentials' })).toHaveCount(0)
  await expect(dialog.getByText(
    'This disconnects your Kroger account for everyone in your household. Nothing on your list is deleted. Sending to your Kroger cart stops until someone reconnects.',
  )).toBeVisible()
  const cancel = dialog.getByRole('button', { name: 'Cancel' })
  await expect.poll(async () => cancel.evaluate((el) => el === document.activeElement)).toBe(true)
  const disconnect = dialog.getByRole('button', { name: 'Disconnect', exact: true })
  const redFilled = await page.evaluate(() => {
    const probe = document.createElement('div')
    probe.style.backgroundColor = 'var(--mantine-color-red-filled)'
    document.body.appendChild(probe)
    const color = getComputedStyle(probe).backgroundColor
    document.body.removeChild(probe)
    return color
  })
  const disconnectBg = await disconnect.evaluate((el) => getComputedStyle(el).backgroundColor)
  const cancelBg = await cancel.evaluate((el) => getComputedStyle(el).backgroundColor)
  expect(disconnectBg).toBe(redFilled)
  expect(cancelBg).not.toBe(redFilled)
  const cancelBox = await cancel.boundingBox()
  const disconnectBox = await disconnect.boundingBox()
  expect(cancelBox).not.toBeNull()
  expect(disconnectBox).not.toBeNull()
  expect(cancelBox!.height).toBeGreaterThanOrEqual(43.99)
  expect(disconnectBox!.height).toBeGreaterThanOrEqual(43.99)
  await disconnect.click()
  await expect.poll(() => disconnected).toBe(true)
})

test('Test Household non-admin sees disconnect failure in the disconnect-only dialog', async ({ page }) => {
  resetStage()
  stage.kind = 'account'
  stage.householdName = 'Test Household'
  stage.operator = false
  stage.role = 'member'
  await install(page)
  const errorText = 'Kroger could not be disconnected'
  let deleteCount = 0
  await page.route('**/api/providers/kroger/connection', (route) => {
    if (route.request().method() === 'DELETE') {
      deleteCount += 1
      return route.fulfill({
        status: 500,
        contentType: 'application/json',
        body: JSON.stringify({ error: errorText }),
      })
    }
    return route.continue()
  })
  await stubShoppingSurface(page, krogerProviders('saved', 'connected'))
  await page.goto('/shopping')
  await page.getByRole('button', { name: 'Menu' }).click()
  await page.getByRole('menuitem', { name: 'Disconnect Kroger' }).click()
  const dialog = page.getByRole('dialog', { name: 'Disconnect Kroger?' })
  await expect(dialog).toBeVisible()
  await dialog.getByRole('button', { name: 'Disconnect', exact: true }).click()
  await expect(dialog.getByRole('alert')).toHaveText(errorText)
  await expect(dialog).toBeVisible()
  expect(deleteCount).toBe(1)
})

test('forbidden_operator keeps the admin alert visible and hides the app keys form', async ({ page }) => {
  resetStage()
  stage.kind = 'account'
  stage.householdName = 'Test Household'
  stage.operator = true
  stage.role = 'owner'
  await install(page)
  await page.route('**/api/providers/kroger/credentials', (route) => {
    if (route.request().method() === 'PUT') {
      return route.fulfill({
        status: 403,
        contentType: 'application/json',
        body: JSON.stringify({ error: 'Only an admin can do this.', code: 'forbidden_operator' }),
      })
    }
    return route.continue()
  })
  await stubShoppingSurface(page, krogerProviders('saved'))
  await page.goto('/shopping')
  await page.getByRole('button', { name: 'Menu' }).click()
  await page.getByRole('menuitem', { name: 'Manage Kroger connection' }).click()
  await page.getByRole('textbox', { name: 'Client ID' }).fill('fixture-client')
  await page.getByRole('textbox', { name: 'Client secret' }).fill('typed-once')
  await page.getByRole('textbox', { name: 'Redirect URI' }).fill('https://test-household.example/cb')
  await page.getByRole('button', { name: 'Save credentials' }).click()
  const dialog = page.getByRole('dialog', { name: 'Kroger connection' })
  await expect(dialog).toBeVisible()
  const alert = dialog.getByRole('alert')
  await expect(alert).toHaveText('Only the server admin can change the Kroger app keys.')
  await expect(alert).toBeFocused()
  await expect(page.getByRole('form', { name: 'Kroger credentials' })).toHaveCount(0)
  await expect(page.getByRole('button', { name: 'Save credentials' })).toHaveCount(0)
})
