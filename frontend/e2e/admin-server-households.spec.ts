import { expect, test, type Page } from '@playwright/test'
import { install, resetStage, stage } from './householdStageFixtures'

// Untyped legacy-server payload: an old or buggy server may still send members.
// The admin list must never render any of it.
const legacyMembers = [
  { userId: 'u-hidden', email: 'secret@example.com', name: 'Hidden Test Member', role: 'owner', createdAt: '2026-04-01T00:00:00Z' },
]

async function routeHouseholds(page: Page, households: Record<string, unknown>[]) {
  await page.route('**/api/operator/households', (route) => route.fulfill({
    status: 200,
    contentType: 'application/json',
    body: JSON.stringify({ households: households.map((row) => ({ ...row, members: legacyMembers })) }),
  }))
}

const baseRow = { isPrimary: false, restockMode: 'auto', createdAt: '2026-04-01T00:00:00Z', pendingScans: 0 }

async function openAdmin(page: Page, width: number, height: number) {
  resetStage()
  stage.kind = 'account'
  stage.operator = true
  stage.householdName = 'Test Household'
  await page.setViewportSize({ width, height })
  await install(page)
}

async function expectNoMemberPii(page: Page) {
  const html = await page.content()
  for (const secret of ['secret@example.com', 'Hidden Test Member']) expect(html).not.toContain(secret)
  await expect(page.getByTestId('operator-households-section').getByText(/^\s*(Owner|Member)\s*$/)).toHaveCount(0)
}

for (const { width, height } of [{ width: 390, height: 844 }, { width: 1280, height: 800 }] as const) {
  test(`admin server households show counts only at ${width}x${height}`, async ({ page }) => {
    await openAdmin(page, width, height)
    await routeHouseholds(page, [{
      ...baseRow, id: 'hh-1', name: 'Test Household', isPrimary: true, memberCount: 3, productCount: 4, itemsOnHand: 2,
      scannersOnline: 2, scannersOffline: 1, scansThisWeek: 5, lastActivityAt: '2026-04-02T00:00:00Z',
    }])
    await page.goto('/households')
    await expect(page.getByRole('heading', { name: 'All households (Admin)' })).toBeVisible()
    const section = page.getByTestId('operator-households-section')
    await expect(section.getByText('Test Household', { exact: true }).first()).toBeVisible()
    await expect(section.getByTestId('operator-household-stats')).toHaveText(
      /^3 members · 4 products · 2 items on hand · 2 scanners online · 1 scanner offline · 5 scans this week · 0 pending scans · /,
    )
    await expectNoMemberPii(page)
    await expect(page.getByText(/\boperator\b/i)).toHaveCount(0)
  })
}

test('admin households wrap a long Test Household name inside the card at 390x844', async ({ page }) => {
  await openAdmin(page, 390, 844)
  const longName = `Test Household ${'X'.repeat(60)}`
  await routeHouseholds(page, [{
    ...baseRow, id: 'hh-long', name: longName, memberCount: 1, productCount: 1, itemsOnHand: 1,
    scannersOnline: 0, scannersOffline: 0, scansThisWeek: 0,
  }])
  await page.goto('/households')
  const section = page.getByTestId('operator-households-section')
  const name = section.getByText(longName, { exact: true })
  await expect(name).toBeVisible()
  await expect(section.getByTestId('operator-household-stats')).toHaveText(
    /^1 member · 1 product · 1 item on hand · 0 scanners online · 0 scanners offline · 0 scans this week · 0 pending scans · /,
  )
  const box = await name.boundingBox()
  const card = await section.boundingBox()
  expect(box!.x + box!.width).toBeLessThanOrEqual(card!.x + card!.width)
  expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(390)
  await expectNoMemberPii(page)
})

test('admin households show the load error inline, without the empty copy', async ({ page }) => {
  await openAdmin(page, 390, 844)
  await page.route('**/api/operator/households', (route) => route.fulfill({
    status: 500, contentType: 'application/json', body: JSON.stringify({ error: 'boom', code: 'internal' }),
  }))
  await page.goto('/households')
  const section = page.getByTestId('operator-households-section')
  await expect(section.getByRole('alert')).toHaveText('Something went wrong. Try again.')
  await expect(section.getByText('No households on this server yet.')).toHaveCount(0)
  await expect(page.getByText(/\boperator\b/i)).toHaveCount(0)
})
