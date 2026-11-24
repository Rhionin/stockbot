import { AxeBuilder } from '@axe-core/playwright'
import { expect, test, type Locator, type Page } from '@playwright/test'

const email = 'e2e@stockbot.test'
const password = 'correct horse'
const shots = process.env.Q5_SHOTS

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
  direction: string | null
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

const code = (width: number, kind: number, n: number) => `0765${String(width).padStart(4, '0')}${kind}${n}`

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

async function statusOf(page: Page, id: string) {
  for (const status of ['pending', 'committed', 'cancelled']) {
    const rows = await json<ScanRow[]>(page, 'GET', `/api/scans?userId=user-1&status=${status}`)
    if (rows.some((row) => row.id === id)) return status
  }
  return 'missing'
}

async function box(locator: Locator) {
  const found = await locator.boundingBox()
  expect(found).not.toBeNull()
  return found as { x: number; y: number; width: number; height: number }
}

async function expectNoViolations(page: Page) {
  const result = await new AxeBuilder({ page }).analyze()
  expect(result.violations.map((v) => `${v.id}: ${v.help} (${v.nodes.map((n) => n.html.slice(0, 200)).join(' | ')})`)).toEqual([])
}

async function shot(page: Page, name: string, width: number) {
  if (shots) await page.screenshot({ path: `${shots}/${name}-${width}.png`, fullPage: false })
}

function card(page: Page, name: string) {
  return page.locator('[data-testid^="scan-"]').filter({ hasText: name })
}

async function expectInside(parent: Locator, children: Locator[]) {
  const outer = await box(parent)
  for (const child of children) {
    const inner = await box(child)
    expect(inner.x).toBeGreaterThanOrEqual(outer.x - 1)
    expect(inner.x + inner.width).toBeLessThanOrEqual(outer.x + outer.width + 1)
  }
}

for (const width of [1280, 390]) {
  test.describe(`batches at ${width}px`, () => {
    test.use({ viewport: { width, height: 844 } })

    test('selects a subset, reviews it, and commits exactly those', async ({ page }) => {
      await signIn(page)
      const names = ['Oats', 'Rice', 'Beans', 'Lentils'].map((name) => `Batch ${name} ${width}`)
      const ids: string[] = []
      for (const [index, name] of names.entries()) {
        await product(page, name, code(width, 1, index))
        ids.push(await scan(page, code(width, 1, index), 'stock_in'))
      }
      await page.reload()
      await expect(card(page, names[0])).toBeVisible()

      const session = page.getByRole('region', { name: /^Scan session/ }).first()
      const selectAll = session.getByRole('checkbox', { name: /^Select all eligible scans in/ })
      await expect(selectAll).toHaveAttribute('aria-checked', 'false')

      await page.getByRole('checkbox', { name: `Select ${names[1]}` }).click()
      await page.getByRole('checkbox', { name: `Select ${names[3]}` }).click()
      await expect(page.getByRole('checkbox', { name: `Select ${names[1]}` })).toHaveAttribute('aria-checked', 'true')
      await expect(page.getByRole('checkbox', { name: `Select ${names[0]}` })).toHaveAttribute('aria-checked', 'false')
      await expect(selectAll).toHaveAttribute('aria-checked', 'mixed')
      await shot(page, 'selected', width)

      await page.getByRole('button', { name: 'Review 2 selected' }).click()
      const panel = page.getByTestId('batch-review')
      await expect(panel).toBeVisible()
      await expect(panel.getByRole('heading', { name: 'Confirm 2 scans' })).toBeVisible()
      await expect(panel.getByText(names[1])).toBeVisible()
      await expect(panel.getByText(names[3])).toBeVisible()
      await expect(panel.getByText(names[0])).toHaveCount(0)
      await expect(panel).toBeFocused()
      await shot(page, 'review', width)
      await expectNoViolations(page)

      const panelBox = await box(panel)
      expect(panelBox.x).toBeGreaterThanOrEqual(0)
      expect(panelBox.x + panelBox.width).toBeLessThanOrEqual(width)
      expect(panelBox.y + panelBox.height).toBeLessThanOrEqual(844)
      const confirm = panel.getByRole('button', { name: 'Confirm 2 selected scans' })
      const back = panel.getByRole('button', { name: 'Back' })
      await expectInside(panel, [confirm, back])
      expect((await box(confirm)).height).toBeGreaterThanOrEqual(44)
      expect((await box(back)).height).toBeGreaterThanOrEqual(44)
      expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(width)

      await back.click()
      await expect(panel).toHaveCount(0)
      await expect(page.getByRole('button', { name: 'Review 2 selected' })).toBeFocused()
      await page.getByRole('button', { name: 'Review 2 selected' }).click()
      await panel.getByRole('button', { name: 'Confirm 2 selected scans' }).click()

      await expect(card(page, names[1])).toHaveCount(0)
      await expect(card(page, names[3])).toHaveCount(0)
      await expect(card(page, names[0])).toBeVisible()
      await expect(card(page, names[2])).toBeVisible()
      await expect(page.getByRole('heading', { name: 'Scan queue' })).toBeFocused()
      expect(await Promise.all(ids.map((id) => statusOf(page, id)))).toEqual(['pending', 'committed', 'pending', 'committed'])
      await expectNoViolations(page)
    })

    test('select all within the tab leaves the other tab alone', async ({ page }) => {
      await signIn(page)
      const inName = `Batch Tab In ${width}`
      const outName = `Batch Tab Out ${width}`
      await product(page, inName, code(width, 2, 1))
      await product(page, outName, code(width, 2, 2))
      const inId = await scan(page, code(width, 2, 1), 'stock_in')
      const outId = await scan(page, code(width, 2, 2), 'stock_out')
      await page.reload()
      await expect(card(page, inName)).toBeVisible()

      await page.getByRole('checkbox', { name: /^Select all eligible scans in/ }).click()
      await page.getByRole('tab', { name: /^Stock out/ }).click()
      await expect(card(page, outName)).toBeVisible()
      await expect(page.getByRole('button', { name: /^Review/ })).toHaveCount(0)
      await expect(page.getByRole('checkbox', { name: `Select ${outName}` })).toHaveAttribute('aria-checked', 'false')
      await page.getByRole('tab', { name: /^Stock in/ }).click()
      await expect(page.getByRole('checkbox', { name: `Select ${inName}` })).toHaveAttribute('aria-checked', 'false')
      expect(await statusOf(page, inId)).toBe('pending')
      expect(await statusOf(page, outId)).toBe('pending')
    })

    test('keeps the scan that failed, with its notice, and commits the rest', async ({ page }) => {
      await signIn(page)
      const goodName = `Batch Good ${width}`
      const neverName = `Batch Never Stocked ${width}`
      await product(page, goodName, code(width, 3, 1))
      await product(page, neverName, code(width, 3, 2))
      const stockedIn = await scan(page, code(width, 3, 1), 'stock_in')
      await json(page, 'POST', `/api/scans/${stockedIn}/commit`, {})
      const goodScan = await scan(page, code(width, 3, 1), 'stock_out')
      const neverScan = await scan(page, code(width, 3, 2), 'stock_out')
      await page.reload()
      await page.getByRole('tab', { name: /^Stock out/ }).click()
      await expect(card(page, goodName)).toBeVisible()

      await page.getByRole('checkbox', { name: /^Select all eligible scans in/ }).click()
      await page.getByRole('button', { name: 'Review 2 selected' }).click()
      await page.getByTestId('batch-review').getByRole('button', { name: 'Confirm 2 selected scans' }).click()

      await expect(card(page, goodName)).toHaveCount(0)
      await expect(card(page, neverName)).toHaveCount(1)
      await expect(page.getByText('Some scans could not be confirmed.')).toBeVisible()
      const banner = await box(page.getByTestId('server-notice-banner'))
      expect(banner.height).toBeLessThan(80)
      expect(banner.x + banner.width).toBeLessThanOrEqual(width)
      await expect(card(page, neverName).getByText(/./).filter({ hasText: /stock|item|which/i }).first()).toBeVisible()
      await expect(page.getByRole('checkbox', { name: `Select ${neverName}` })).toHaveAttribute('aria-checked', 'true')
      await expect(page.getByRole('button', { name: 'Review 1 selected' })).toBeVisible()
      await shot(page, 'partial-failure', width)
      expect(await statusOf(page, goodScan)).toBe('committed')
      expect(await statusOf(page, neverScan)).toBe('pending')
      expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(width)
      await expectNoViolations(page)
    })

    test('drops a scan from the selection when another device leaves nothing on hand', async ({ page }) => {
      await signIn(page)
      const staleName = `Batch Stale ${width}`
      const freshName = `Batch Fresh ${width}`
      const staleCode = code(width, 4, 1)
      const freshCode = code(width, 4, 2)
      const spareCode = code(width, 4, 3)
      const staleProduct = await product(page, staleName, staleCode)
      await product(page, freshName, freshCode)
      await json(page, 'POST', '/api/products/overrides', { barcode: spareCode, productId: staleProduct })
      for (const barcode of [staleCode, freshCode]) {
        const stockedIn = await scan(page, barcode, 'stock_in')
        await json(page, 'POST', `/api/scans/${stockedIn}/commit`, {})
      }
      const staleScan = await scan(page, staleCode, 'stock_out')
      const freshScan = await scan(page, freshCode, 'stock_out')
      const takesTheLastOne = await scan(page, spareCode, 'stock_out')
      await page.reload()
      await page.getByRole('tab', { name: /^Stock out/ }).click()
      await expect(card(page, staleName).first()).toBeVisible()

      await page.getByRole('checkbox', { name: `Select ${staleName}` }).first().click()
      await page.getByRole('checkbox', { name: `Select ${freshName}` }).click()
      await page.getByRole('button', { name: 'Review 2 selected' }).click()
      const panel = page.getByTestId('batch-review')
      await expect(panel.getByText(staleName)).toBeVisible()

      await json(page, 'POST', `/api/scans/${takesTheLastOne}/commit`, {})
      await expect(panel.getByRole('heading', { name: 'Confirm 1 scan' })).toBeVisible()
      await expect(panel.getByText(staleName)).toHaveCount(0)
      await expect(panel.getByText('Confirm skips the 1 with nothing on hand.')).toBeVisible()
      await shot(page, 'review-skips-nothing-on-hand', width)
      await panel.getByRole('button', { name: 'Confirm 1 selected scans' }).click()

      await expect(card(page, freshName)).toHaveCount(0)
      expect(await statusOf(page, freshScan)).toBe('committed')
      expect(await statusOf(page, staleScan)).toBe('pending')
      expect(await statusOf(page, takesTheLastOne)).toBe('committed')
      await expectNoViolations(page)
    })
  })
}
