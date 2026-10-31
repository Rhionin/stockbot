import { expect, test, type Page, type Route } from '@playwright/test'
import { mkdirSync } from 'node:fs'
import { join } from 'node:path'

const shotDir = process.env.PROVISION_EXPORT_SHOTS ?? '/opt/cursor/artifacts/481'
mkdirSync(shotDir, { recursive: true })

const householdName = 'Test Household'

const krogerProvider = {
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
}

const stagedEntry = {
  id: 'sli-test-1',
  itemId: 'item-test-1',
  quantity: 2,
  source: 'manual',
  purchasedAt: null,
}

const longName = 'Organic extra virgin cold-pressed avocado oil with a name long enough to wrap at three hundred twenty pixels without horizontal scrolling'

const viewports = [
  { width: 390, height: 844, label: '390' },
  { width: 1280, height: 800, label: '1280' },
] as const

type ExportCase =
  | 'ideal'
  | 'all-sent'
  | 'loading'
  | 'many-skipped'
  | 'nothing-added'
  | 'batch-rejected'
  | 'error-try-again'
  | 'reconnect'
  | 'legacy'
  | 'overflow'

interface MockOptions {
  exportBody?: Record<string, unknown>
  exportStatus?: number
  delayExportMs?: number
  sessionHousehold?: string
}

const idealExport = () => ({
  provider: 'kroger',
  exported: 2,
  sentCount: 2,
  skippedCount: 1,
  failedCount: 1,
  unknownCount: 1,
  results: [
    { entryId: 'a', itemId: '1', name: 'Milk', quantity: 1, status: 'sent' },
    { entryId: 'b', itemId: '2', name: 'Bread', quantity: 1, status: 'sent' },
    { entryId: 'c', itemId: '3', name: 'No barcode item', quantity: 1, status: 'skipped', reason: 'no_upc' },
    { entryId: 'd', itemId: '4', name: 'Rejected soup', quantity: 1, status: 'failed', reason: 'batch_rejected' },
    { entryId: 'e', itemId: '5', name: 'Oats', quantity: 1, status: 'unknown', reason: 'no_per_item_result' },
  ],
  entries: [{ entryId: 'e', itemId: '5', name: 'Oats', quantity: 1, outcome: 'unknown' }],
})

const exportBodies: Record<ExportCase, Record<string, unknown>> = {
  ideal: idealExport(),
  'all-sent': {
    provider: 'kroger',
    exported: 2,
    sentCount: 2,
    results: [
      { entryId: 'a', itemId: '1', name: 'Milk', quantity: 1, status: 'sent' },
      { entryId: 'b', itemId: '2', name: 'Bread', quantity: 1, status: 'sent' },
    ],
    handoff: { url: 'https://example.test/kroger-cart' },
  },
  loading: idealExport(),
  'many-skipped': {
    provider: 'kroger',
    exported: 0,
    results: Array.from({ length: 12 }, (_, index) => ({
      entryId: `s-${index}`,
      itemId: `i-${index}`,
      name: `Skipped ${index}`,
      quantity: 1,
      status: 'skipped',
      reason: 'no_upc',
    })),
  },
  'nothing-added': {
    provider: 'kroger',
    exported: 0,
    results: [
      { entryId: 'a', itemId: '1', name: 'Milk', quantity: 1, status: 'skipped', reason: 'no_match' },
    ],
  },
  'batch-rejected': {
    provider: 'kroger',
    exported: 0,
    results: [
      { entryId: 'a', itemId: '1', name: 'Soup', quantity: 1, status: 'failed', reason: 'batch_rejected' },
    ],
  },
  'error-try-again': {
    provider: 'kroger',
    exported: 0,
    error: 'provider_unavailable',
  },
  reconnect: {
    provider: 'kroger',
    exported: 0,
    error: 'reconnect_required',
  },
  legacy: {
    provider: 'kroger',
    exported: 1,
    failedItems: ['Jam'],
    unknownItems: ['Oats'],
    entries: [{ entryId: 'sli-oats', itemId: 'oats', name: 'Oats', quantity: 1, outcome: 'unknown' }],
  },
  overflow: {
    provider: 'kroger',
    exported: 0,
    results: [
      { entryId: 'long', itemId: 'long', name: longName, quantity: 1, status: 'skipped', reason: 'no_upc' },
    ],
  },
}

async function installShoppingMocks(page: Page, options: MockOptions) {
  await page.route('**/api/auth/session', async (route) => {
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({
        kind: 'account',
        user: { id: 'user-1', email: 'admin@testhousehold.test', name: 'Admin User' },
        memberships: [{ householdId: 'hh-test', name: options.sessionHousehold ?? householdName, role: 'owner', deletedAt: null }],
        households: [{ id: 'hh-test', name: options.sessionHousehold ?? householdName, role: 'owner', deletedAt: null }],
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
      body: JSON.stringify([krogerProvider]),
    })
  })

  await page.route('**/api/shopping-list**', async (route: Route) => {
    const url = route.request().url()
    if (route.request().method() === 'GET' && !url.includes('/export')) {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify([stagedEntry]),
      })
      return
    }
    if (route.request().method() === 'POST' && url.includes('/export')) {
      if (options.delayExportMs) {
        await new Promise((resolve) => setTimeout(resolve, options.delayExportMs))
      }
      await route.fulfill({
        status: options.exportStatus ?? 200,
        contentType: 'application/json',
        body: JSON.stringify(options.exportBody ?? idealExport()),
      })
      return
    }
    await route.continue()
  })

  await page.route('**/api/inventory', async (route) => {
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify([]),
    })
  })

  await page.route('**/api/restock', async (route) => {
    await route.fulfill({ status: 404, body: 'not found' })
  })
}

async function openShoppingAndSend(page: Page) {
  await page.goto('/shopping')
  await expect(page.getByRole('heading', { name: 'Shopping plan' })).toBeVisible()
  await page.getByRole('button', { name: 'Send to Kroger' }).click()
}

async function saveShot(page: Page, filename: string) {
  await page.screenshot({ path: join(shotDir, filename), fullPage: true })
}

for (const viewport of viewports) {
  test(`ideal mixed result panel at ${viewport.width}px`, async ({ page }) => {
    await page.setViewportSize({ width: viewport.width, height: viewport.height })
    await installShoppingMocks(page, { exportBody: exportBodies.ideal })
    await openShoppingAndSend(page)
    const panel = page.getByRole('status', { name: 'Cart send result' })
    await expect(panel).toBeVisible()
    await expect(panel).toHaveAttribute('aria-live', 'polite')
    await expect(panel.getByText('Not added')).toBeVisible()
    await expect(panel.getByText('Not confirmed')).toBeVisible()
    if (viewport.label === '390') {
      await saveShot(page, 'ideal-390.png')
    } else {
      await saveShot(page, 'ideal-1280.png')
    }
  })
}

const singleViewportCases: { case: ExportCase; file: string; expectText: RegExp | string }[] = [
  { case: 'all-sent', file: 'all-sent-390.png', expectText: /2 items sent to your Kroger cart/ },
  { case: 'many-skipped', file: 'many-skipped-390.png', expectText: 'Show all 12' },
  { case: 'nothing-added', file: 'nothing-added-390.png', expectText: /Nothing was sent to your cart/ },
  { case: 'batch-rejected', file: 'batch-rejected-390.png', expectText: /didn't accept this item/ },
  { case: 'error-try-again', file: 'error-try-again-390.png', expectText: "Kroger isn't responding right now" },
  { case: 'reconnect', file: 'reconnect-390.png', expectText: "Couldn't connect to your Kroger account" },
  { case: 'legacy', file: 'legacy-390.png', expectText: 'It reached Kroger' },
]

for (const scenario of singleViewportCases) {
  test(`export ${scenario.case} at 390px`, async ({ page }) => {
    await page.setViewportSize({ width: 390, height: 844 })
    await installShoppingMocks(page, { exportBody: exportBodies[scenario.case] })
    await openShoppingAndSend(page)
    const panel = page.getByRole('status', { name: 'Cart send result' })
    await expect(panel).toBeVisible()
    await expect(panel.getByText(scenario.expectText)).toBeVisible()
    await saveShot(page, scenario.file)
  })
}

test('loading clears the previous panel at 390px', async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 })
  await installShoppingMocks(page, {
    exportBody: exportBodies['all-sent'],
    delayExportMs: 600,
  })
  await page.goto('/shopping')
  await page.getByRole('button', { name: 'Send to Kroger' }).click()
  await expect(page.getByRole('button', { name: 'Sending…' })).toBeVisible()
  await expect(page.getByRole('status', { name: 'Cart send result' })).toHaveCount(0)
  await saveShot(page, 'loading-390.png')
  await expect(page.getByRole('status', { name: 'Cart send result' })).toBeVisible()
})

test('long names wrap within 320px without horizontal overflow at 390px', async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 })
  await installShoppingMocks(page, { exportBody: exportBodies.overflow })
  await openShoppingAndSend(page)
  const item = page.locator('.provision-result-item').first()
  await expect(item).toBeVisible()
  await expect.poll(() => item.evaluate((node) => node.scrollWidth <= node.clientWidth + 1)).toBe(true)
  await saveShot(page, 'overflow-320-390.png')
})

test('HTTP export errors show Try again at 390 and 1280', async ({ page }) => {
  for (const viewport of viewports) {
    await page.setViewportSize({ width: viewport.width, height: viewport.height })
    await installShoppingMocks(page, {
      exportStatus: 409,
      exportBody: { error: 'Kroger is not connected', code: 'conflict' },
    })
    await openShoppingAndSend(page)
    const panel = page.getByRole('status', { name: 'Cart send result' })
    await expect(panel.getByText('Kroger is not connected')).toBeVisible()
    await expect(panel.getByRole('button', { name: 'Try again' })).toBeVisible()
  }
})

test('whole-request Try again and Reconnect actions are tappable', async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 })
  await installShoppingMocks(page, { exportBody: exportBodies['error-try-again'] })
  await openShoppingAndSend(page)
  const tryAgain = page.getByRole('button', { name: 'Try again' })
  await expect(tryAgain).toBeVisible()
  const box = await tryAgain.boundingBox()
  expect(box).not.toBeNull()
  expect(box!.width).toBeGreaterThanOrEqual(44)
  expect(box!.height).toBeGreaterThanOrEqual(44)
})
