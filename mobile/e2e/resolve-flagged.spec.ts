import { AxeBuilder } from '@axe-core/playwright'
import { expect, test, type Locator, type Page } from '@playwright/test'

const email = 'e2e@stockbot.test'
const password = 'correct horse'
const shots = process.env.Q6_SHOTS

async function api(page: Page, method: string, path: string, body?: unknown) {
  return page.evaluate(
    async ({ method, path, body }) => {
      const res = await fetch(path, {
        method,
        credentials: 'same-origin',
        headers: { 'Content-Type': 'application/json' },
        body: body === undefined ? undefined : JSON.stringify(body),
      })
      return { status: res.status, body: await res.text() }
    },
    { method, path, body },
  )
}

async function json<T>(page: Page, method: string, path: string, body?: unknown): Promise<T> {
  const res = await api(page, method, path, body)
  expect(res.status, res.body).toBeLessThan(300)
  return (res.body === '' ? undefined : JSON.parse(res.body)) as T
}

interface ScanRow {
  id: string
  status: string
  productId: string | null
  unitCount: number
  direction: string | null
  expiresAt: string | null
}

interface ProductRow {
  id: string
  name: string
  unitOfMeasure: string
  netAmount?: number
  netUnit?: string
  packCount?: number
}

interface InventoryRow {
  item: { id: string; productId: string }
  instanceCount: number
}

const seeded: string[] = []

test.afterEach(async ({ page }) => {
  for (const id of seeded.splice(0)) await api(page, 'PATCH', `/api/scans/${id}`, { status: 'cancelled' })
  await api(page, 'POST', '/api/scanner/mode', { mode: 'stock_in' })
})

async function signIn(page: Page) {
  await page.goto('/')
  await page.getByLabel('Email').fill(email)
  await page.getByLabel('Password').fill(password)
  await page.getByRole('button', { name: 'Sign in' }).click()
  await expect(page.getByRole('heading', { name: 'Scan queue' })).toBeVisible()
}

const code = (width: number, kind: number) => `0771${String(width).padStart(4, '0')}${kind}00`

async function scan(page: Page, barcode: string, direction: 'stock_in' | 'stock_out' = 'stock_in') {
  const posted = await json<ScanRow>(page, 'POST', '/api/scans', { barcode, direction, userId: 'user-1' })
  seeded.push(posted.id)
  return posted
}

async function scanById(page: Page, id: string, status: string) {
  const rows = await json<ScanRow[]>(page, 'GET', `/api/scans?userId=user-1&status=${status}`)
  return rows.find((row) => row.id === id)
}

async function products(page: Page) {
  return json<ProductRow[]>(page, 'GET', '/api/products')
}

async function box(locator: Locator) {
  const found = await locator.boundingBox()
  expect(found).not.toBeNull()
  return found as { x: number; y: number; width: number; height: number }
}

async function expectNoViolations(page: Page) {
  const result = await new AxeBuilder({ page }).analyze()
  expect(result.violations.map((v) => `${v.id}: ${v.help} (${v.nodes.length})`)).toEqual([])
}

async function shot(page: Page, name: string, width: number) {
  if (shots) await page.screenshot({ path: `${shots}/${name}-${width}.png`, fullPage: false })
}

function card(page: Page, id: string) {
  return page.getByTestId(`scan-${id}`)
}

function dialog(page: Page) {
  return page.getByRole('dialog', { name: 'What is it?' })
}

async function expectSheetLayout(page: Page, width: number) {
  const sheet = await box(dialog(page).getByRole('heading', { name: 'What is it?', level: 2 }))
  expect(sheet.x).toBeGreaterThanOrEqual(0)
  expect(sheet.x + sheet.width).toBeLessThanOrEqual(width)
  expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(width)
  const labels = ['Product name (required)', 'Brand', 'Size', 'Unit', 'Package', 'Pack count']
  for (const label of labels) {
    const found = await box(dialog(page).getByText(label, { exact: true }).first())
    expect(found.x).toBeGreaterThanOrEqual(0)
    expect(found.x + found.width).toBeLessThanOrEqual(width)
  }
  for (const field of ['Product name', 'Brand', 'Size', 'Package', 'Pack count']) {
    const input = await box(dialog(page).getByRole('textbox', { name: field, exact: true }))
    expect(input.x).toBeGreaterThanOrEqual(0)
    expect(input.x + input.width).toBeLessThanOrEqual(width)
    expect(input.height).toBeGreaterThanOrEqual(44)
  }
  for (const target of await dialog(page).getByRole('radio').all()) {
    expect((await box(target)).height).toBeGreaterThanOrEqual(44)
  }
  const wide = width >= 768
  const panel = await box(dialog(page).getByRole('button', { name: 'Create and use product' }))
  if (wide) expect(panel.width).toBeLessThanOrEqual(560)
  else expect(panel.width).toBeGreaterThan(width - 60)
}

for (const width of [1280, 390]) {
  test.describe(`naming a flagged scan at ${width}px`, () => {
    test.use({ viewport: { width, height: 844 } })

    test('names an unknown barcode through the form, confirms it and the stock moves', async ({ page }) => {
      await signIn(page)
      const barcode = code(width, 1)
      const name = `E2E Oat Flour ${width}`
      const flagged = await scan(page, barcode)
      expect(flagged.status).toBe('flagged')
      await page.reload()

      const row = card(page, flagged.id)
      await expect(row).toBeVisible()
      await expect(row.getByText(/^Needs a name/)).toBeVisible()
      const rule = await row.evaluate((el) => {
        const style = getComputedStyle(el)
        return { width: style.borderLeftWidth, color: style.borderLeftColor }
      })
      expect(rule).toEqual({ width: '4px', color: 'rgb(146, 64, 14)' })
      await expect(row.getByRole('button', { name: `Confirm ${barcode}` })).toHaveCount(0)
      await shot(page, 'flagged-card', width)
      await expectNoViolations(page)

      const writes: { method: string; url: string; body: string }[] = []
      page.on('request', (request) => {
        if (request.method() !== 'GET') writes.push({ method: request.method(), url: new URL(request.url()).pathname, body: request.postData() ?? '' })
      })

      await row.getByRole('button', { name: `What is it? ${barcode}` }).click()
      await expect(dialog(page)).toBeVisible()
      await expect(dialog(page).getByRole('heading', { name: 'What is it?', level: 2 })).toBeFocused()
      await expect(dialog(page).getByText(`Barcode ${barcode}`)).toBeVisible()
      await expectSheetLayout(page, width)
      await shot(page, 'sheet-empty', width)
      await expectNoViolations(page)

      await dialog(page).getByRole('button', { name: 'Create and use product' }).click()
      const nameField = dialog(page).getByRole('textbox', { name: 'Product name', exact: true })
      await expect(dialog(page).getByText('Enter a name for this product.')).toBeVisible()
      await expect(nameField).toHaveAttribute('aria-invalid', 'true')
      await expect(nameField).toHaveAttribute('aria-describedby', 'resolve-name-error')
      await expect(page.locator('#resolve-name-error')).toHaveText('Enter a name for this product.')
      await expect(nameField).toBeFocused()

      await nameField.fill(name)
      await dialog(page).getByRole('textbox', { name: 'Brand', exact: true }).fill('Millhouse')
      await dialog(page).getByRole('textbox', { name: 'Size', exact: true }).fill('16')
      await dialog(page).getByRole('textbox', { name: 'Pack count', exact: true }).fill('0')
      await dialog(page).getByRole('button', { name: 'Create and use product' }).click()
      await expect(dialog(page).getByText('Enter a size and a unit together.')).toBeVisible()
      await expect(dialog(page).getByText('Pack count has to be at least 1.')).toBeVisible()
      await shot(page, 'sheet-errors', width)
      await expectNoViolations(page)
      expect(writes.filter((write) => write.url === '/api/products')).toEqual([])
      expect((await products(page)).some((product) => product.name === name)).toBe(false)
      expect((await scanById(page, flagged.id, 'flagged'))?.status).toBe('flagged')

      await dialog(page).getByRole('radio', { name: 'oz', exact: true }).click()
      await dialog(page).getByRole('textbox', { name: 'Package', exact: true }).fill('bag')
      await dialog(page).getByRole('textbox', { name: 'Pack count', exact: true }).fill('2')
      await expect(dialog(page).getByText('Pack count has to be at least 1.')).toHaveCount(0)
      await shot(page, 'sheet-filled', width)
      await dialog(page).getByRole('button', { name: 'Create and use product' }).click()
      await expect(dialog(page)).toHaveCount(0)

      await expect(row.getByRole('button', { name: `Confirm ${name}` })).toBeVisible()
      await expect(row.getByRole('button', { name: /^What is it\?/ })).toHaveCount(0)
      await shot(page, 'resolved-card', width)
      await expectNoViolations(page)

      const created = (await products(page)).find((product) => product.name === name)
      expect(created).toBeTruthy()
      expect(created).toMatchObject({ unitOfMeasure: 'bag', netAmount: 16, netUnit: 'oz', packCount: 2 })
      const productId = (created as ProductRow).id
      const resolved = await scanById(page, flagged.id, 'pending')
      expect(resolved?.productId).toBe(productId)
      const lookup = await json<{ product: { id: string } | null }>(page, 'GET', `/api/products/lookup?barcode=${barcode}`)
      expect(lookup.product?.id).toBe(productId)

      const posted = writes.filter((write) => write.method === 'POST' && write.url === '/api/products')
      expect(posted).toHaveLength(1)
      expect(JSON.parse(posted[0].body)).toEqual({
        name,
        brand: 'Millhouse',
        category: '',
        unitOfMeasure: 'bag',
        netAmount: 16,
        netUnit: 'oz',
        packCount: 2,
        barcode,
      })
      const patch = writes.find((write) => write.method === 'PATCH' && write.url === `/api/scans/${flagged.id}`)
      expect(JSON.parse(patch?.body ?? '{}')).toEqual({ productId, status: 'pending' })
      const override = writes.find((write) => write.url === '/api/products/overrides')
      expect(JSON.parse(override?.body ?? '{}')).toEqual({ barcode, productId })

      await row.getByRole('button', { name: `Confirm ${name}` }).click()
      await expect(row).toHaveCount(0)
      const inventory = await json<InventoryRow[]>(page, 'GET', '/api/inventory')
      // Pack count 2: one scan of this barcode adds two units.
      expect(inventory.find((entry) => entry.item.productId === productId)?.instanceCount).toBe(2)
      expect(await scanById(page, flagged.id, 'pending')).toBeUndefined()
    })

    test('links an unknown barcode to an item we already have', async ({ page }) => {
      await signIn(page)
      const shelfCode = code(width, 2)
      const unknownCode = code(width, 3)
      const name = `E2E Link Rice ${width}`
      await json(page, 'POST', '/api/products', { name, category: 'Pantry', unitOfMeasure: 'bag', barcode: shelfCode })
      const productId = (await products(page)).find((product) => product.name === name)?.id as string
      expect(productId).toBeTruthy()
      await json(page, 'POST', '/api/products/overrides', { barcode: shelfCode, productId })
      const stocked = await scan(page, shelfCode)
      await json(page, 'POST', `/api/scans/${stocked.id}/commit`, {})

      const flagged = await scan(page, unknownCode)
      expect(flagged.status).toBe('flagged')
      await page.reload()
      const row = card(page, flagged.id)
      await row.getByRole('button', { name: `What is it? ${unknownCode}` }).click()

      const search = dialog(page).getByRole('textbox', { name: 'Search products', exact: true })
      await search.fill('E')
      await expect(dialog(page).getByText('Type at least 2 letters.')).toBeVisible()
      await search.fill(`Link Rice ${width}`)
      const hitRow = dialog(page).getByRole('radio', { name })
      await expect(hitRow).toBeVisible()
      await expect(dialog(page).getByText(/1 on hand/)).toBeVisible()
      const useSelected = dialog(page).getByRole('button', { name: 'Use selected product' })
      await expect(useSelected).toBeDisabled()
      await hitRow.click()
      await expect(hitRow).toHaveAttribute('aria-checked', 'true')
      await expect(useSelected).toBeEnabled()
      await shot(page, 'sheet-link', width)
      await expectNoViolations(page)
      expect((await box(hitRow)).height).toBeGreaterThanOrEqual(44)

      await useSelected.click()
      await expect(dialog(page)).toHaveCount(0)
      await expect(row.getByRole('button', { name: `Confirm ${name}` })).toBeVisible()
      const resolved = await scanById(page, flagged.id, 'pending')
      expect(resolved?.productId).toBe(productId)
      const lookup = await json<{ product: { id: string } | null }>(page, 'GET', `/api/products/lookup?barcode=${unknownCode}`)
      expect(lookup.product?.id).toBe(productId)
      expect((await products(page)).filter((product) => product.name === name)).toHaveLength(1)

      await row.getByRole('button', { name: `Confirm ${name}` }).click()
      await expect(row).toHaveCount(0)
      const inventory = await json<InventoryRow[]>(page, 'GET', '/api/inventory')
      expect(inventory.find((entry) => entry.item.productId === productId)?.instanceCount).toBe(2)
    })

    test('closes with Escape, hands focus back and leaves the scan flagged', async ({ page }) => {
      await signIn(page)
      const barcode = code(width, 4)
      const flagged = await scan(page, barcode)
      await page.reload()
      const row = card(page, flagged.id)
      const trigger = row.getByRole('button', { name: `What is it? ${barcode}` })
      await trigger.click()
      await dialog(page).getByRole('textbox', { name: 'Product name', exact: true }).fill('Half typed')

      await page.keyboard.press('Escape')

      await expect(dialog(page)).toHaveCount(0)
      await expect(trigger).toBeFocused()
      expect((await scanById(page, flagged.id, 'flagged'))?.status).toBe('flagged')
      expect((await products(page)).some((product) => product.name === 'Half typed')).toBe(false)
    })

    test('a resolve leaves the scan count, direction and expiry as the server holds them (#327)', async ({ page }) => {
      await signIn(page)
      const shelfCode = code(width, 5)
      const unknownCode = code(width, 6)
      const name = `E2E Keep Beans ${width}`
      await json(page, 'POST', '/api/products', { name, category: 'Pantry', unitOfMeasure: 'can', barcode: shelfCode })
      const productId = (await products(page)).find((product) => product.name === name)?.id as string
      const flagged = await scan(page, unknownCode)
      await json(page, 'PATCH', `/api/scans/${flagged.id}`, { unitCount: 3, expiresAt: '2027-03-01T00:00:00.000Z' })
      const before = (await scanById(page, flagged.id, 'flagged')) as ScanRow
      test.info().annotations.push({
        type: 'flagged scan fields the server kept',
        description: JSON.stringify({ unitCount: before.unitCount, expiresAt: before.expiresAt, direction: before.direction }),
      })
      await page.reload()
      await card(page, flagged.id).getByRole('button', { name: `What is it? ${unknownCode}` }).click()
      await dialog(page).getByRole('textbox', { name: 'Search products', exact: true }).fill(`Keep Beans ${width}`)
      await dialog(page).getByRole('radio', { name }).click()
      await dialog(page).getByRole('button', { name: 'Use selected product' }).click()
      await expect(dialog(page)).toHaveCount(0)

      const after = (await scanById(page, flagged.id, 'pending')) as ScanRow
      expect(after.productId).toBe(productId)
      expect(after.unitCount).toBe(before.unitCount)
      expect(after.expiresAt).toBe(before.expiresAt)
      expect(after.direction).toBe(before.direction)
    })
  })
}
