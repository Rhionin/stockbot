import { AxeBuilder } from '@axe-core/playwright'
import { expect, test, type Locator, type Page } from '@playwright/test'

const email = 'e2e@stockbot.test'
const password = 'correct horse'
const shots = process.env.Q4_SHOTS

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
  unitCount: number
  expiresAt: string | null
  status: string
}

interface InstanceRow {
  id: string
  expiresAt: string | null
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

const code = (width: number, kind: number, n: number) => `0987${String(width).padStart(4, '0')}${kind}${n}`

async function product(page: Page, name: string, barcode: string) {
  await json(page, 'POST', '/api/products', { name, category: 'Pantry', unitOfMeasure: 'ea', barcode })
  const products = await json<{ id: string; name: string }[]>(page, 'GET', '/api/products')
  const productId = products.find((candidate) => candidate.name === name)?.id
  expect(productId).toBeTruthy()
  await json(page, 'POST', '/api/products/overrides', { barcode, productId })
  return productId as string
}

async function scan(page: Page, barcode: string, direction: 'stock_in' | 'stock_out') {
  const posted = await json<{ id: string }>(page, 'POST', '/api/scans', { barcode, direction, userId: 'user-1' })
  seeded.push(posted.id)
  return posted.id
}

async function pendingScan(page: Page, id: string) {
  const rows = await json<ScanRow[]>(page, 'GET', '/api/scans?userId=user-1&status=pending')
  return rows.find((row) => row.id === id)
}

async function stockedItem(page: Page, name: string, barcode: string, expiries: string[]) {
  const productId = await product(page, name, barcode)
  for (const expiresAt of expiries) {
    const id = await scan(page, barcode, 'stock_in')
    await json(page, 'PATCH', `/api/scans/${id}`, { expiresAt: `${expiresAt}T00:00:00.000Z` })
    await json(page, 'POST', `/api/scans/${id}/commit`, {})
  }
  const rows = await json<{ item: { id: string; productId: string } }[]>(page, 'GET', '/api/inventory')
  const itemId = rows.find((row) => row.item.productId === productId)?.item.id
  expect(itemId).toBeTruthy()
  return itemId as string
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

function countField(row: Locator) {
  return row.getByRole('textbox', { name: 'Unit count' })
}

function card(page: Page, name: string) {
  return page.locator('[data-testid^="scan-"]').filter({ hasText: name })
}

for (const width of [1280, 390]) {
  test.describe(`card details at ${width}px`, () => {
    test.use({ viewport: { width, height: 844 } })

    test('edits the unit count and the server keeps it', async ({ page }) => {
      await signIn(page)
      await product(page, `Count Lentils ${width}`, code(width, 1, 1))
      const id = await scan(page, code(width, 1, 1), 'stock_in')
      await page.reload()
      const row = card(page, `Count Lentils ${width}`)
      await expect(row).toBeVisible()

      const patches: string[] = []
      page.on('request', (request) => {
        if (request.method() === 'PATCH') patches.push(request.postData() ?? '')
      })

      await row.getByLabel('Increase unit count').click()
      await row.getByLabel('Increase unit count').click()
      await expect(countField(row)).toHaveValue('3')
      await expect.poll(async () => (await pendingScan(page, id))?.unitCount).toBe(3)

      await countField(row).fill('0')
      await countField(row).blur()
      await expect(row.getByText('Enter a whole number, 1 or more.')).toBeVisible()
      await expect(countField(row)).toHaveValue('3')
      expect(patches.some((body) => body.includes('"unitCount":0'))).toBe(false)
      expect((await pendingScan(page, id))?.unitCount).toBe(3)
      await shot(page, 'unit-count-error', width)
      await expectNoViolations(page)

      await countField(row).fill('7')
      await expect.poll(async () => (await pendingScan(page, id))?.unitCount).toBe(7)
      await expect(row.getByText('Enter a whole number, 1 or more.')).toHaveCount(0)
    })

    test('sets and clears an expiration date', async ({ page }) => {
      await signIn(page)
      await product(page, `Expiry Barley ${width}`, code(width, 2, 1))
      const id = await scan(page, code(width, 2, 1), 'stock_in')
      await page.reload()
      const row = card(page, `Expiry Barley ${width}`)
      await expect(row).toBeVisible()

      await row.getByRole('button', { name: 'Add expiration' }).click()
      const field = row.getByLabel('Expiration date')
      await expect(field).toBeFocused()
      await shot(page, 'expiry-editing', width)
      await field.fill('2027-03-04')
      await field.blur()
      await expect(row.getByText(/^Expires .*2027/)).toBeVisible()
      await expect.poll(async () => (await pendingScan(page, id))?.expiresAt).toMatch(/^2027-03-04/)
      await shot(page, 'expiry-set', width)

      await row.getByRole('button', { name: 'Clear expiration' }).click()
      await expect(row.getByRole('button', { name: 'Add expiration' })).toBeVisible()
      await expect.poll(async () => (await pendingScan(page, id))?.expiresAt).toBeNull()
      await expectNoViolations(page)
    })

    test('stocks out the chosen instance', async ({ page }) => {
      await signIn(page)
      const itemId = await stockedItem(page, `Pick Rice ${width}`, code(width, 3, 1), ['2027-04-01', '2027-06-01'])
      const before = await json<InstanceRow[]>(page, 'GET', `/api/inventory/${itemId}/instances`)
      expect(before).toHaveLength(2)
      const later = before.find((unit) => unit.expiresAt?.startsWith('2027-06-01'))
      expect(later).toBeTruthy()

      await scan(page, code(width, 3, 1), 'stock_out')
      await page.reload()
      await page.getByRole('tab', { name: /^Stock out/ }).click()
      const row = card(page, `Pick Rice ${width}`)
      await expect(row).toBeVisible()

      await row.getByRole('button', { name: 'More' }).click()
      const group = row.getByTestId(`instances-${itemId}`)
      await expect(group).toBeVisible()
      const radios = group.getByRole('radio')
      await expect(radios).toHaveCount(3)
      await expect(radios.nth(0)).toHaveAccessibleName('Use oldest available automatically')
      await expect(radios.nth(1)).toHaveAccessibleName(/Apr 1, 2027/)
      await expect(radios.nth(2)).toHaveAccessibleName(/Jun 1, 2027/)
      await radios.nth(2).click()
      await expect(radios.nth(2)).toHaveAttribute('aria-checked', 'true')
      await shot(page, 'instance-picker', width)
      await expectNoViolations(page)

      const picker = await box(group)
      const rowBox = await box(row)
      expect(picker.x).toBeGreaterThanOrEqual(rowBox.x)
      expect(picker.x + picker.width).toBeLessThanOrEqual(rowBox.x + rowBox.width + 1)
      expect(picker.y + picker.height).toBeLessThanOrEqual(rowBox.y + rowBox.height + 1)
      expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(width)
      for (const target of await radios.all()) expect((await box(target)).height).toBeGreaterThanOrEqual(44)

      await row.getByRole('button', { name: `Confirm Pick Rice ${width}` }).click()
      await expect(row).toHaveCount(0)

      const after = await json<InstanceRow[]>(page, 'GET', `/api/inventory/${itemId}/instances`)
      expect(after.map((unit) => unit.id)).not.toContain(later?.id)
      expect(after).toHaveLength(1)
      expect(after[0].expiresAt).toMatch(/^2027-04-01/)
    })

    test('takes the oldest unit when none is chosen', async ({ page }) => {
      await signIn(page)
      const itemId = await stockedItem(page, `Auto Millet ${width}`, code(width, 4, 1), ['2027-04-01', '2027-06-01'])
      await scan(page, code(width, 4, 1), 'stock_out')
      await page.reload()
      await page.getByRole('tab', { name: /^Stock out/ }).click()
      const row = card(page, `Auto Millet ${width}`)

      await row.getByRole('button', { name: `Confirm Auto Millet ${width}` }).click()
      await expect(row).toHaveCount(0)

      const after = await json<InstanceRow[]>(page, 'GET', `/api/inventory/${itemId}/instances`)
      expect(after).toHaveLength(1)
      expect(after[0].expiresAt).toMatch(/^2027-06-01/)
    })

    test('undoes an add-and-take-out and the ledger goes back', async ({ page }) => {
      await signIn(page)
      const barcode = code(width, 5, 1)
      const quinoaItem = await stockedItem(page, `Undo Quinoa ${width}`, barcode, ['2027-04-01'])
      const emptied = await scan(page, barcode, 'stock_out')
      await json(page, 'POST', `/api/scans/${emptied}/commit`, {})
      await scan(page, barcode, 'stock_out')
      await page.reload()
      await page.getByRole('tab', { name: /^Stock out/ }).click()
      const row = card(page, `Undo Quinoa ${width}`)
      await expect(row).toBeVisible()
      await shot(page, 'amber-card', width)
      await row.getByRole('button', { name: /Add it and take it out/ }).click()

      const toast = page.getByTestId('confirm-toast')
      await expect(toast).toContainText('Added and taken out')
      await shot(page, 'undo-toast', width)
      const toastBox = await box(toast)
      expect(toastBox.x).toBeGreaterThanOrEqual(0)
      expect(toastBox.x + toastBox.width).toBeLessThanOrEqual(width)
      expect(toastBox.y + toastBox.height).toBeLessThanOrEqual(844)
      const undo = toast.getByRole('button', { name: 'Undo' })
      expect((await box(undo)).height).toBeGreaterThanOrEqual(44)
      await expectNoViolations(page)

      const history = () => json<{ moves: unknown[]; onHand: number }>(page, 'GET', `/api/items/${quinoaItem}/history`)
      expect((await history()).moves).toHaveLength(4)

      await undo.click()
      await expect(toast).toHaveCount(0)
      await expect.poll(async () => (await history()).moves.length).toBe(2)
      expect((await history()).onHand).toBe(0)
    })

    test('keeps the expanded card inside its column', async ({ page }) => {
      await signIn(page)
      const itemId = await stockedItem(page, `Bounds Oats ${width}`, code(width, 6, 1), ['2027-04-01', '2027-06-01'])
      await scan(page, code(width, 6, 1), 'stock_out')
      await page.reload()
      await page.getByRole('tab', { name: /^Stock out/ }).click()
      const row = card(page, `Bounds Oats ${width}`)
      await row.getByRole('button', { name: 'More' }).click()
      await expect(row.getByTestId(`instances-${itemId}`)).toBeVisible()

      const rowBox = await box(row)
      expect(rowBox.x).toBeGreaterThanOrEqual(0)
      expect(rowBox.x + rowBox.width).toBeLessThanOrEqual(width)
      const stepper = await box(row.getByTestId(/^stepper-/))
      const confirm = await box(row.getByRole('button', { name: /^Confirm/ }))
      const more = await box(row.getByRole('button', { name: 'More' }))
      const picker = await box(row.getByTestId(`instances-${itemId}`))
      for (const part of [stepper, confirm, more, picker]) {
        expect(part.x).toBeGreaterThanOrEqual(rowBox.x - 1)
        expect(part.x + part.width).toBeLessThanOrEqual(rowBox.x + rowBox.width + 1)
      }
      expect(confirm.y + confirm.height).toBeLessThanOrEqual(stepper.y + 1)
      expect(stepper.y + stepper.height).toBeLessThanOrEqual(picker.y + 1)
      expect(stepper.x + stepper.width).toBeLessThanOrEqual(more.x + 1)
      expect(stepper.height).toBeGreaterThanOrEqual(44)
      expect(more.height).toBeGreaterThanOrEqual(44)
      expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(width)
      await shot(page, 'expanded-card', width)
    })
  })
}
