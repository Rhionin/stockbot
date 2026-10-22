import { mkdirSync } from 'node:fs'
import { join } from 'node:path'
import { expect, test, type APIRequestContext, type Page } from '@playwright/test'
import { commitSelectedScan, resetToStockIn, scanBarcode } from './helpers'

const artifacts = process.env.PANTRY_ARTIFACTS

async function shot(page: Page, name: string) {
  if (!artifacts) return
  mkdirSync(artifacts, { recursive: true })
  // The shell scrolls inside main, and dialogs are position:fixed. A full-page
  // capture misses both the rest of the list and the open sheet.
  await page.screenshot({ path: join(artifacts, name) })
}

async function fit(page: Page, width: number, minHeight: number) {
  const height = await page.evaluate(() => {
    const frame = document.querySelector('.page-frame')
    return Math.ceil(56 + (frame?.scrollHeight ?? 800) + 48)
  })
  await page.setViewportSize({ width, height: Math.max(minHeight, Math.min(height, 2200)) })
}

interface Product {
  id: string
  name: string
}

async function createProduct(
  request: APIRequestContext,
  input: { name: string; category: string; unitOfMeasure: string; netAmount?: number; netUnit?: string },
): Promise<Product> {
  const response = await request.post('/api/products', { data: input })
  if (!response.ok()) throw new Error(`create ${input.name}: ${response.status()} ${await response.text()}`)
  return response.json() as Promise<Product>
}

test('product group bin at phone and desktop widths', async ({ page, request }) => {
  test.setTimeout(60_000)
  const lemon = await createProduct(request, {
    name: 'Gatorade Lemon-Lime', category: 'Drinks', unitOfMeasure: 'canister', netAmount: 18.3, netUnit: 'oz',
  })
  const glacier = await createProduct(request, {
    name: 'Gatorade Glacier Freeze', category: 'Drinks', unitOfMeasure: 'canister', netAmount: 50.9, netUnit: 'oz',
  })
  const punch = await createProduct(request, {
    name: 'Gatorade Fruit Punch', category: 'Drinks', unitOfMeasure: 'canister', netAmount: 18.3, netUnit: 'oz',
  })
  const peanut = await createProduct(request, {
    name: 'Creamy peanut butter', category: 'Spreads', unitOfMeasure: 'jar', netAmount: 16, netUnit: 'oz',
  })
  const oat = await createProduct(request, {
    name: 'Oat milk', category: 'Dairy', unitOfMeasure: 'carton',
  })
  const extra = await createProduct(request, {
    name: 'Lemonade powder', category: 'Drinks', unitOfMeasure: 'canister', netAmount: 18, netUnit: 'oz',
  })

  const barcode = '052000338881'
  const override = await request.post('/api/products/overrides', { data: { barcode, productId: lemon.id } })
  expect(override.ok()).toBe(true)

  await page.goto('/')
  await resetToStockIn(page)
  await commitSelectedScan(page, await scanBarcode(page, barcode))

  const groupResponse = await request.post('/api/groups', {
    data: {
      name: 'Gatorade powder',
      productIds: [lemon.id, glacier.id, punch.id],
      target: { quantity: 48, dimension: 'mass' },
    },
  })
  if (!groupResponse.ok()) throw new Error(await groupResponse.text())
  const group = await groupResponse.json() as { id: string }
  expect((await request.put(`/api/groups/${group.id}/rule`, {
    data: { rule: 'same_as_ran_out', confirm: true },
  })).ok()).toBe(true)

  const peanutResponse = await request.post('/api/groups', {
    data: { name: 'Peanut butter', productIds: [peanut.id], target: { windowMonths: 3 } },
  })
  if (!peanutResponse.ok()) throw new Error(await peanutResponse.text())
  const peanutGroup = await peanutResponse.json() as { id: string }
  expect((await request.put(`/api/groups/${peanutGroup.id}/rule`, {
    data: { rule: 'favorite', pinnedProductId: peanut.id, confirm: true },
  })).ok()).toBe(true)

  const oatResponse = await request.post('/api/groups', { data: { name: 'Oat milk', productIds: [oat.id] } })
  if (!oatResponse.ok()) throw new Error(await oatResponse.text())

  expect((await request.post('/api/onboarding/complete')).ok()).toBe(true)
  await page.setViewportSize({ width: 390, height: 844 })
  await page.goto('/groups')
  await expect(page).toHaveURL(/\/inventory\?filter=groups/)
  await expect(page.getByRole('heading', { name: 'Inventory' })).toBeVisible()
  await expect(page.getByRole('button', { name: 'Groups', exact: true })).toHaveAttribute('aria-pressed', 'true')
  await expect(page.getByRole('textbox', { name: 'Search products or groups' })).toBeVisible()
  await expect(page.getByRole('button', { name: 'Look for more groups' })).toBeVisible()

  const powder = page.getByRole('article').filter({ has: page.getByRole('heading', { name: 'Gatorade powder' }) })
  await expect(powder.getByText('18.3 oz on hand')).toBeVisible()
  await expect(powder.getByText('48 oz')).toBeVisible()
  const peanutRow = page.getByRole('article').filter({ has: page.getByRole('heading', { name: 'Peanut butter' }) })
  await expect(peanutRow.getByText('Creamy peanut butter')).toBeVisible()
  const oatRow = page.getByRole('article').filter({ has: page.getByRole('heading', { name: 'Oat milk' }) })
  await expect(oatRow.getByText('Pick a rule')).toBeVisible()

  const rows = page.locator('.shelf-row')
  const firstBox = await rows.nth(0).boundingBox()
  const secondBox = await rows.nth(1).boundingBox()
  const photoBox = await powder.locator('.shelf-photo').boundingBox()
  const nameBox = await powder.locator('.shelf-name').boundingBox()
  expect(firstBox && secondBox && photoBox && nameBox).toBeTruthy()
  expect(secondBox!.y).toBeGreaterThan(firstBox!.y + firstBox!.height - 4)
  expect(photoBox!.x).toBeLessThan(nameBox!.x)
  await fit(page, 390, 844)
  await shot(page, 'after-group-list-390.png')
  await page.setViewportSize({ width: 390, height: 844 })

  const expand = powder.getByRole('button', { name: /^(Expand|Collapse) Gatorade powder$/ })
  await powder.getByRole('button', { name: 'Expand Gatorade powder' }).focus()
  await page.keyboard.press('Enter')
  await expect(expand).toHaveAttribute('aria-expanded', 'true')
  await expect(powder.getByText('18.3 oz canister, 1 on hand')).toBeVisible()
  const settings = powder.getByRole('link', { name: 'Open group settings ›' })
  await settings.scrollIntoViewIfNeeded()
  await settings.click()
  await expect(page).toHaveURL(new RegExp(`/groups/${group.id}`))
  const detail = page.locator('.bin-hero')
  await expect(page.getByRole('heading', { name: 'Gatorade powder' })).toBeVisible()
  await expect(page.getByText('18.3 oz on hand')).toBeVisible()
  await expect(detail.getByRole('meter', { name: 'Stock on hand' })).toHaveAttribute('aria-valuenow', '18.3')
  await expect(detail.getByRole('meter', { name: 'Stock on hand' })).toHaveAttribute('aria-valuemax', '48')
  await expect(page.getByRole('button', { name: '48 oz', exact: true })).toBeVisible()
  await expect(page.getByText('Fills in once usage is known')).toBeVisible()
  await expect(page.getByRole('button', { name: 'Change rule, Same product' })).toBeVisible()
  await expect(page.getByText('18.3 oz canister, 1 on hand')).toBeVisible()
  await expect(page.getByText('50.9 oz canister, none on hand')).toBeVisible()
  await expect(page.getByText('Barcode: 052000338881')).toBeVisible()
  await expect(page.getByLabel('Ounces', { exact: true })).toHaveCount(0)
  await expect(page.getByRole('button', { name: 'Rename' })).toBeVisible()

  const bin = page.locator('.bin-hero')
  const rule = page.locator('.rule-pill')
  const binBox = await bin.boundingBox()
  const ruleBox = await rule.boundingBox()
  const wellBox = await page.locator('.bin-hero .bin-well').boundingBox()
  const fillBox = await page.locator('.bin-hero .bin-fill').boundingBox()
  const captionBox = await page.locator('.bin-caption').boundingBox()
  expect(binBox && ruleBox && wellBox && fillBox && captionBox).toBeTruthy()
  expect(binBox!.y + binBox!.height).toBeLessThanOrEqual(ruleBox!.y + 4)
  expect(wellBox!.height).toBeLessThan(64)
  expect(fillBox!.width).toBeGreaterThan(8)
  expect(fillBox!.width).toBeLessThan(wellBox!.width - 8)
  expect(captionBox!.y).toBeGreaterThanOrEqual(wellBox!.y)
  expect(captionBox!.y + captionBox!.height).toBeLessThanOrEqual(wellBox!.y + wellBox!.height + 1)
  const fillRight = fillBox!.x + fillBox!.width
  const crossesFill = captionBox!.x < fillRight - 1 && captionBox!.x + captionBox!.width > fillRight + 1
  expect(crossesFill).toBe(false)
  await fit(page, 390, 844)
  await shot(page, 'after-group-detail-390.png')
  await page.setViewportSize({ width: 390, height: 844 })

  await page.getByRole('button', { name: 'Set time', exact: true }).click()
  await expect(page.getByLabel('Months')).toBeVisible()
  await expect(page.getByRole('button', { name: 'Save' })).toBeVisible()
  await expect(page.getByRole('button', { name: 'Use the household default' })).toBeVisible()
  await shot(page, 'after-change-target-390.png')
  await page.getByRole('button', { name: 'Cancel' }).click()

  await fit(page, 390, 844)
  await page.getByRole('button', { name: 'Change rule, Same product' }).click()
  const ruleDialog = page.getByRole('dialog', { name: 'What to buy next' })
  await expect(ruleDialog.getByRole('radio', { name: 'Same product' })).toBeChecked()
  await expect(ruleDialog.getByText('Rebuy the exact product you used last.')).toBeVisible()
  await expect(ruleDialog.getByText('Lowest price per ounce.')).toBeVisible()
  await expect(ruleDialog.getByRole('button', { name: 'Save' })).toBeVisible()
  await shot(page, 'after-change-rule-390.png')
  await page.keyboard.press('Escape')
  await expect(ruleDialog).toBeHidden()
  await page.setViewportSize({ width: 390, height: 844 })

  await page.setViewportSize({ width: 1440, height: 900 })
  await expect(page.getByRole('heading', { name: 'Gatorade powder' })).toBeVisible()
  const wideBin = await page.locator('.bin-hero').boundingBox()
  const wideRule = await page.locator('.rule-pill').boundingBox()
  expect(wideBin && wideRule).toBeTruthy()
  expect(wideRule!.y).toBeGreaterThanOrEqual(wideBin!.y + wideBin!.height - 4)
  expect(Math.abs(wideRule!.x - wideBin!.x)).toBeLessThan(8)
  await fit(page, 1440, 900)
  await shot(page, 'after-group-detail-1440.png')
  await page.setViewportSize({ width: 1440, height: 900 })

  await page.setViewportSize({ width: 390, height: 844 })
  await page.getByRole('button', { name: 'Set time', exact: true }).click()
  await page.getByLabel('Months').fill('6')
  await page.getByRole('button', { name: 'Save' }).click()
  await expect(page.getByRole('button', { name: '6 mo', exact: true })).toBeVisible()
  await expect(page.locator('.bin-hero .bin-fill')).toHaveCount(0)

  await page.getByRole('button', { name: 'Set amount', exact: true }).click()
  await page.getByLabel('Ounces').fill('48')
  await page.getByRole('button', { name: 'Save' }).click()
  await expect(page.getByRole('button', { name: '48 oz', exact: true })).toBeVisible()
  await expect(page.locator('.bin-hero .bin-fill')).toHaveCount(1)

  await page.getByRole('button', { name: 'Remove or move Gatorade Glacier Freeze' }).click()
  await expect(page.getByRole('menuitem', { name: 'Remove' })).toBeVisible()
  await page.keyboard.press('Escape')

  await page.getByRole('button', { name: 'Add a product' }).click()
  await expect(page.getByRole('checkbox', { name: 'Lemonade powder' })).toBeVisible()
  await expect(page.getByRole('checkbox', { name: /Gatorade Lemon-Lime/ })).toHaveCount(0)
  await page.getByRole('button', { name: 'Close' }).click()

  await page.getByRole('button', { name: 'Change rule, Same product' }).click()
  await page.getByRole('radio', { name: 'Favorite' }).click()
  await page.getByRole('combobox', { name: 'Product' }).click()
  await page.getByRole('option', { name: 'Gatorade Lemon-Lime' }).click()
  await page.getByRole('button', { name: 'Save' }).click()
  await expect(page.getByRole('button', { name: 'Change rule, Gatorade Lemon-Lime' })).toBeVisible()
  await expect(page.getByRole('button', { name: '48 oz', exact: true })).toBeVisible()

  expect(extra.id).not.toBe('')
})

test.afterEach(async ({ request }) => {
  const listed = await request.get('/api/groups')
  if (!listed.ok()) return
  const groups = await listed.json() as { id: string; name: string }[]
  for (const group of groups) {
    if (group.name === 'Gatorade powder' || group.name === 'Peanut butter' || group.name === 'Oat milk') {
      await request.delete(`/api/groups/${group.id}`)
    }
  }
})
