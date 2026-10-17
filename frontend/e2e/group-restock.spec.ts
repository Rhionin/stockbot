import AxeBuilder from '@axe-core/playwright'
import { expect, test, type APIRequestContext, type Page } from '@playwright/test'
import { commitSelectedScan, resetToStockIn, scanBarcode } from './helpers'

// The suite shares one database. These scans have to land after opening so
// variety can see a stock-in time. Shopping list, which runs later, still
// expects the opening snapshot, so the household is put back when this spec ends.
let createdGroupID = ''

async function restoreOpeningSnapshot(request: APIRequestContext) {
  if (createdGroupID !== '') {
    await request.delete(`/api/groups/${createdGroupID}`)
    createdGroupID = ''
  }
  const wiped = await request.post('/api/inventory/wipe', {
    data: { confirmation: 'WIPE INVENTORY' },
  })
  expect(wiped.ok()).toBe(true)
  const settings = await request.get('/api/settings/supply')
  expect(settings.ok()).toBe(true)
  const body = await settings.json() as { opening: boolean }
  expect(body.opening).toBe(true)
}

async function shot(page: Page, name: string) {
  // Named screenshots belong in this test's output directory (test-results/).
  await page.screenshot({ path: test.info().outputPath(name) })
}

async function createProduct(
  request: APIRequestContext,
  input: { name: string; category: string; unitOfMeasure: string; netAmount?: number; netUnit?: string },
) {
  const response = await request.post('/api/products', { data: input })
  if (!response.ok()) throw new Error(`create ${input.name}: ${response.status()} ${await response.text()}`)
  return response.json() as Promise<{ id: string; name: string }>
}

async function setScheme(page: Page, scheme: 'light' | 'dark') {
  await page.evaluate((value) => {
    window.localStorage.setItem('mantine-color-scheme-value', value)
    document.documentElement.setAttribute('data-mantine-color-scheme', value)
  }, scheme)
  await page.reload()
  await expect.poll(() => page.evaluate(() => document.documentElement.getAttribute('data-mantine-color-scheme'))).toBe(scheme)
}

test.afterEach(async ({ request }) => {
  await restoreOpeningSnapshot(request)
})

test('favor variety and don\'t restock on a phone', async ({ page, request }) => {
  test.setTimeout(90_000)
  const bran = await createProduct(request, {
    name: 'Kroger bran', category: 'Baking', unitOfMeasure: 'box', netAmount: 18, netUnit: 'oz',
  })
  const berry = await createProduct(request, {
    name: 'Kroger blueberry', category: 'Baking', unitOfMeasure: 'box', netAmount: 18.3, netUnit: 'oz',
  })
  const fancy = await createProduct(request, {
    name: 'Lehi roller', category: 'Baking', unitOfMeasure: 'box', netAmount: 18.3, netUnit: 'oz',
  })
  const berryOverride = await request.post('/api/products/overrides', {
    data: { barcode: '052000449911', productId: berry.id },
  })
  expect(berryOverride.ok()).toBe(true)
  const fancyOverride = await request.post('/api/products/overrides', {
    data: { barcode: '052000449912', productId: fancy.id },
  })
  expect(fancyOverride.ok()).toBe(true)

  expect((await request.post('/api/onboarding/complete')).ok()).toBe(true)

  await page.goto('/')
  await resetToStockIn(page)
  await commitSelectedScan(page, await scanBarcode(page, '052000449911'))
  await commitSelectedScan(page, await scanBarcode(page, '052000449912'))

  const created = await request.post('/api/groups', {
    data: {
      name: 'Muffin mix',
      productIds: [bran.id, berry.id, fancy.id],
      target: { quantity: 48, dimension: 'mass' },
    },
  })
  if (!created.ok()) throw new Error(await created.text())
  const group = await created.json() as { id: string }
  createdGroupID = group.id
  expect((await request.put(`/api/groups/${group.id}/rule`, {
    data: { rule: 'same_as_ran_out', confirm: true },
  })).ok()).toBe(true)

  await page.setViewportSize({ width: 390, height: 900 })
  await page.goto(`/groups/${group.id}`)
  await expect(page.getByRole('heading', { name: 'Muffin mix' })).toBeVisible()
  await expect(page.getByRole('button', { name: 'Change rule, Same product' })).toBeVisible()
  await expect(page.getByRole('heading', { name: 'Not restocked' })).toHaveCount(0)
  await shot(page, 'before-group-390.png')

  await page.getByRole('button', { name: 'Remove or move Lehi roller' }).click()
  await expect(page.getByText('Stays in the group and counts toward stock')).toBeVisible()
  await page.getByRole('menuitem', { name: /Don't restock/ }).click()
  await expect(page.getByRole('heading', { name: 'Not restocked' })).toBeVisible()
  await expect(page.getByText('⊘ no restock')).toBeVisible()
  await expect(page.getByRole('meter', { name: 'Stock on hand' })).toHaveAttribute('aria-valuenow', '36.6')

  await page.getByRole('button', { name: 'Change rule, Same product' }).click()
  const picker = page.getByRole('dialog', { name: 'What to buy next' })
  await expect(picker.getByText('Products marked "Don\'t restock" are skipped.')).toBeVisible()
  await picker.getByRole('radio', { name: 'Favor variety' }).click()
  await expect(picker.getByText("Rotate to the product you've had least recently.")).toBeVisible()
  await picker.getByRole('button', { name: 'Save' }).click()
  await expect(page.getByRole('button', { name: 'Change rule, Favor variety' })).toBeVisible()
  await expect(page.getByText('Next up: Kroger bran · rotates through 2')).toBeVisible()
  await expect(page.getByRole('heading', { name: 'In rotation' })).toBeVisible()
  await expect(page.getByRole('meter', { name: 'Stock on hand' })).toHaveAttribute('aria-valuenow', '36.6')
  await shot(page, 'after-variety-group-390.png')

  await page.getByRole('button', { name: 'Remove or move Kroger bran' }).click()
  await expect(page.getByRole('menuitem', { name: /Don't restock/ })).toBeVisible()
  await expect(page.getByText('Stays in the group and counts toward stock')).toBeVisible()
  await shot(page, 'after-variety-menu-390.png')
  await page.keyboard.press('Escape')

  await page.getByRole('button', { name: 'Change rule, Favor variety' }).click()
  await expect(picker.getByRole('radio', { name: 'Favor variety' })).toBeChecked()
  await shot(page, 'after-variety-picker-390.png')
  await page.keyboard.press('Escape')

  await setScheme(page, 'dark')
  await expect(page.getByText('Next up: Kroger bran · rotates through 2')).toBeVisible()
  await expect(page.getByRole('heading', { name: 'Not restocked' })).toBeVisible()
  await shot(page, 'after-variety-dark-390.png')

  const violations = await new AxeBuilder({ page }).include('.bin-page').analyze()
  expect(violations.violations).toEqual([])
})
