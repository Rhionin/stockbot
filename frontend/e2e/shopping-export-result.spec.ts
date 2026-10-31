import { expect, test, type Page, type Route } from '@playwright/test'
import { mkdirSync } from 'node:fs'
import { join } from 'node:path'
import { tmpdir } from 'node:os'

const shotDir = process.env.PROVISION_EXPORT_SHOTS ?? join(tmpdir(), 'stockbot-provision-export-shots')
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
  | 'all-unknown'
  | 'mixed'
  | 'unavailable'
  | 'loading'
  | 'many-skipped'
  | 'nothing-added'
  | 'batch-rejected'
  | 'error-try-again'
  | 'reconnect'
  | 'partial-reconnect'
  | 'legacy'
  | 'legacy-200-error'
  | 'overflow'

interface MockOptions {
  exportBody?: Record<string, unknown>
  exportStatus?: number
  delayExportMs?: number
  sessionHousehold?: string
  exportEnvelope?: { status: number; code: string; error?: string }
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

const spuriousUnknownResults = Array.from({ length: 5 }, (_, index) => ({
  entryId: `spurious-${index}`,
  itemId: `item-${index}`,
  name: `Staged ${index}`,
  quantity: 1,
  status: 'unknown',
  reason: 'request_incomplete',
}))

const exportBodies: Record<ExportCase, Record<string, unknown>> = {
  ideal: idealExport(),
  mixed: idealExport(),
  'all-unknown': {
    provider: 'kroger',
    exported: 0,
    results: [
      { entryId: 'a', itemId: '1', name: 'Oats', quantity: 1, status: 'unknown', reason: 'no_per_item_result' },
      { entryId: 'b', itemId: '2', name: 'Rice', quantity: 1, status: 'unknown' },
    ],
  },
  unavailable: {
    provider: 'kroger',
    exported: 0,
    results: [
      { entryId: 'a', itemId: '1', name: 'Soup', quantity: 1, status: 'skipped', reason: 'unavailable' },
    ],
  },
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
    error: 'Kroger is unreachable',
    code: 'provider_unavailable',
  },
  reconnect: {
    error: 'Token revoked',
    code: 'reconnect_required',
  },
  'partial-reconnect': {
    provider: 'kroger',
    exported: 1,
    sentCount: 1,
    reconnectRequired: true,
    results: [
      { entryId: 'a', itemId: '1', name: 'Milk', quantity: 1, status: 'sent' },
      { entryId: 'b', itemId: '2', name: 'Bread', quantity: 1, status: 'failed', reason: 'reconnect_required' },
      { entryId: 'c', itemId: '3', name: 'Butter', quantity: 1, status: 'failed', reason: 'reconnect_required' },
    ],
  },
  'legacy-200-error': {
    provider: 'kroger',
    exported: 0,
    error: 'provider_unavailable',
    results: spuriousUnknownResults,
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
      if (options.exportEnvelope) {
        const { status, code, error } = options.exportEnvelope
        await route.fulfill({
          status,
          contentType: 'application/json',
          body: JSON.stringify({ error: error ?? 'export failed', code }),
        })
        return
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
    await expect(panel.getByText("Couldn't be sent (2)")).toBeVisible()
    await expect(panel.getByText('Check these in your cart (1)')).toBeVisible()
    if (viewport.label === '390') {
      // ideal-390.png captured by dedicated screenshot test
    } else {
      await saveShot(page, 'ideal-1280.png')
    }
  })
}

const singleViewportCases: { case: ExportCase; file: string; role?: 'button'; expectText: RegExp | string }[] = [
  { case: 'all-sent', file: 'all-sent-390.png', expectText: /2 items sent to your Kroger cart/ },
  { case: 'many-skipped', file: 'many-skipped-390.png', expectText: 'Show all 12' },
  { case: 'nothing-added', file: 'nothing-added-390.png', expectText: /Nothing was sent to your cart/ },
  { case: 'batch-rejected', file: 'batch-rejected-390.png', expectText: /didn't accept this item/ },
  { case: 'error-try-again', file: 'provider-unavailable-502-390.png', expectText: 'Nothing was sent' },
  { case: 'reconnect', file: 'reconnect-required-409-390.png', expectText: "Couldn't connect to your Kroger account. Nothing was sent." },
  { case: 'partial-reconnect', file: 'partial-reconnect-390.png', expectText: 'Sent (1)' },
  { case: 'legacy-200-error', file: 'legacy-502-body-390.png', expectText: 'Nothing was sent' },
  { case: 'legacy', file: 'legacy-390.png', role: 'button', expectText: "It's in my cart: Oats" },
]

const uxShots: { case: ExportCase; file: string; envelope?: MockOptions['exportEnvelope'] }[] = [
  { case: 'ideal', file: 'ideal-390.png' },
  { case: 'all-unknown', file: 'all-unknown-390.png' },
  { case: 'mixed', file: 'mixed-390.png' },
  { case: 'unavailable', file: 'unavailable-390.png' },
  { case: 'error-try-again', file: 'provider-unavailable-502-390.png', envelope: { status: 502, code: 'provider_unavailable' } },
  { case: 'reconnect', file: 'reconnect-required-409-390.png', envelope: { status: 409, code: 'reconnect_required' } },
  { case: 'partial-reconnect', file: 'partial-reconnect-390.png' },
]

for (const shot of uxShots) {
  test(`screenshot ${shot.file}`, async ({ page }) => {
    await page.setViewportSize({ width: 390, height: 844 })
    await installShoppingMocks(page, {
      exportBody: exportBodies[shot.case],
      exportEnvelope: shot.envelope,
    })
    await openShoppingAndSend(page)
    await expect(page.getByRole('status', { name: 'Cart send result' })).toBeVisible()
    await saveShot(page, shot.file)
  })
}

test('screenshot mixed at 320px and 125% text', async ({ page }) => {
  await page.setViewportSize({ width: 320, height: 720 })
  await page.addInitScript(() => {
    document.addEventListener('DOMContentLoaded', () => {
      document.documentElement.style.fontSize = '125%'
    })
  })
  await installShoppingMocks(page, { exportBody: exportBodies.mixed })
  await openShoppingAndSend(page)
  await expect(page.getByRole('status', { name: 'Cart send result' })).toBeVisible()
  await saveShot(page, 'mixed-320-125.png')
})

for (const scenario of singleViewportCases) {
  test(`export ${scenario.case} at 390px`, async ({ page }) => {
    await page.setViewportSize({ width: 390, height: 844 })
    const envelope = scenario.case === 'error-try-again'
      ? { status: 502, code: 'provider_unavailable' }
      : scenario.case === 'reconnect'
        ? { status: 409, code: 'reconnect_required' }
        : undefined
    await installShoppingMocks(page, {
      exportBody: exportBodies[scenario.case],
      exportEnvelope: envelope,
    })
    await openShoppingAndSend(page)
    const panel = page.getByRole('status', { name: 'Cart send result' })
    await expect(panel).toBeVisible()
    if (scenario.role === 'button') {
      await expect(panel.getByRole('button', { name: scenario.expectText })).toBeVisible()
    } else {
      await expect(panel.getByText(scenario.expectText)).toBeVisible()
    }
    if (scenario.case === 'reconnect') {
      await expect(panel.getByRole('button', { name: 'Reconnect Kroger' })).toBeVisible()
      await expect(panel.getByRole('button', { name: 'Try again' })).toHaveCount(0)
      await expect(panel.getByText(/might not have reached your cart/)).toHaveCount(0)
      await expect(panel.getByText('Check these in your cart')).toHaveCount(0)
    }
    if (scenario.case === 'error-try-again' || scenario.case === 'legacy-200-error') {
      await expect(panel.getByRole('button', { name: 'Try again' })).toBeVisible()
      await expect(panel.getByRole('button', { name: 'Reconnect Kroger' })).toHaveCount(0)
    }
    if (scenario.case === 'partial-reconnect') {
      await expect(panel.getByRole('button', { name: 'Reconnect Kroger' })).toBeVisible()
    }
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

test('long names wrap without horizontal overflow at 320px', async ({ page }) => {
  await page.setViewportSize({ width: 320, height: 720 })
  await installShoppingMocks(page, { exportBody: exportBodies.overflow })
  await openShoppingAndSend(page)
  const item = page.locator('.provision-result-item').first()
  await expect(item).toBeVisible()
  await expect.poll(() => item.evaluate((node) => node.scrollWidth <= node.clientWidth + 1)).toBe(true)
  await expect.poll(() => page.evaluate(() => document.documentElement.scrollWidth <= document.documentElement.clientWidth)).toBe(true)
  await saveShot(page, 'overflow-320.png')
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

test('nothing-sent error Reconnect control is at least 44px tall', async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 })
  await installShoppingMocks(page, {
    exportEnvelope: { status: 409, code: 'reconnect_required' },
  })
  await openShoppingAndSend(page)
  const reconnect = page.getByRole('button', { name: 'Reconnect Kroger' })
  await expect(reconnect).toBeVisible()
  const box = await reconnect.boundingBox()
  expect(box).not.toBeNull()
  expect(box!.width).toBeGreaterThanOrEqual(44)
  expect(box!.height).toBeGreaterThanOrEqual(44)
})

for (const scenario of [
  { width: 390, rootPercent: 100 },
  { width: 320, rootPercent: 100 },
  { width: 390, rootPercent: 125 },
  { width: 320, rootPercent: 125 },
]) {
  for (const exportCase of ['ideal', 'all-sent', 'partial-reconnect', 'many-skipped'] as const) {
    test(`every control is at least 44x44 for ${exportCase} at ${scenario.width}px and ${scenario.rootPercent}% text`, async ({ page }) => {
      await page.setViewportSize({ width: scenario.width, height: 800 })
      await page.addInitScript((percent) => {
        document.addEventListener('DOMContentLoaded', () => {
          document.documentElement.style.fontSize = `${percent}%`
        })
      }, scenario.rootPercent)
      await installShoppingMocks(page, { exportBody: exportBodies[exportCase] })
      await openShoppingAndSend(page)
      const panel = page.getByRole('status', { name: 'Cart send result' })
      await expect(panel).toBeVisible()
      const controls = page.locator('.shopping-send, .provision-result-panel button, .provision-result-panel a')
      const count = await controls.count()
      expect(count).toBeGreaterThan(0)
      for (let i = 0; i < count; i += 1) {
        const box = await controls.nth(i).boundingBox()
        const name = await controls.nth(i).innerText()
        expect(box, name).not.toBeNull()
        expect(box!.width, name).toBeGreaterThanOrEqual(44)
        expect(box!.height, name).toBeGreaterThanOrEqual(44)
      }
      await expect.poll(() => page.evaluate(() => document.documentElement.scrollWidth <= document.documentElement.clientWidth)).toBe(true)
    })
  }
}
