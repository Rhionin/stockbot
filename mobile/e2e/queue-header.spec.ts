import { AxeBuilder } from '@axe-core/playwright'
import { expect, test, type Locator, type Page } from '@playwright/test'

const email = 'e2e@stockbot.test'
const password = 'correct horse'

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

const seeded: string[] = []

const code = (width: number, n: number) => `0123456${String(width).padStart(4, '0')}${n}`

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

async function scan(page: Page, name: string, code: string, direction: 'stock_in' | 'stock_out') {
  const created = await api(page, 'POST', '/api/products', { name, category: 'Pantry', unitOfMeasure: 'ea', barcode: code })
  expect(created.status, created.body).toBeLessThan(300)
  const products = JSON.parse((await api(page, 'GET', '/api/products')).body) as { id: string; name: string }[]
  const productId = products.find((candidate) => candidate.name === name)?.id
  expect((await api(page, 'POST', '/api/products/overrides', { barcode: code, productId })).status).toBeLessThan(300)
  const posted = await api(page, 'POST', '/api/scans', { barcode: code, direction, userId: 'user-1' })
  expect(posted.status, posted.body).toBe(201)
  seeded.push((JSON.parse(posted.body) as { id: string }).id)
}

async function mode(page: Page) {
  const res = await api(page, 'GET', '/api/scans/mode')
  expect(res.status, res.body).toBe(200)
  return JSON.parse(res.body) as unknown
}

async function box(locator: Locator) {
  const found = await locator.boundingBox()
  expect(found).not.toBeNull()
  return found!
}

async function expectNoViolations(page: Page) {
  const result = await new AxeBuilder({ page }).analyze()
  expect(result.violations.map((v) => `${v.id}: ${v.help} (${v.nodes.length})`)).toEqual([])
}

for (const width of [1280, 390]) {
  test.describe(`queue header at ${width}px`, () => {
    test.use({ viewport: { width, height: 844 } })

    test('counts each direction, switches the scanner mode and filters the list', async ({ page }) => {
      await signIn(page)
      await scan(page, 'Header Lentils', code(width, 1), 'stock_in')
      await scan(page, 'Header Barley', code(width, 2), 'stock_out')
      await scan(page, 'Header Millet', code(width, 3), 'stock_out')
      await page.reload()

      const stockIn = page.getByRole('tab', { name: /^Stock in/ })
      const stockOut = page.getByRole('tab', { name: /^Stock out/ })
      await expect(stockIn).toHaveAccessibleName('Stock in 1 to review')
      await expect(stockOut).toHaveAccessibleName('Stock out 2 to review')
      await expect(stockIn).toHaveAttribute('aria-selected', 'true')
      await expect(page.getByRole('tablist', { name: 'Scan direction' })).toBeVisible()
      await expect(page.getByText('Scans add to stock')).toBeVisible()
      await expect(page.getByText('Header Lentils')).toBeVisible()
      await expect(page.getByText('Header Barley')).toHaveCount(0)
      await expectNoViolations(page)

      await stockOut.click()
      await expect(stockOut).toHaveAttribute('aria-selected', 'true')
      await expect(page.getByText('Scans remove from stock')).toBeVisible()
      await expect(page.getByText('Header Barley')).toBeVisible()
      await expect(page.getByText('Header Millet')).toBeVisible()
      await expect(page.getByText('Header Lentils')).toHaveCount(0)
      await expect.poll(() => mode(page)).toMatchObject({ mode: 'stock_out' })
      await expectNoViolations(page)

      await stockIn.click()
      await expect(page.getByText('Header Lentils')).toBeVisible()
      await expect.poll(() => mode(page)).toMatchObject({ mode: 'stock_in' })
    })

    test('follows a mode change made elsewhere', async ({ page }) => {
      await signIn(page)
      expect((await api(page, 'POST', '/api/scanner/mode', { mode: 'stock_out' })).status).toBeLessThan(300)

      await expect(page.getByRole('tab', { name: /^Stock out/ })).toHaveAttribute('aria-selected', 'true', { timeout: 5000 })
      await expect(page.getByText('Scans remove from stock')).toBeVisible()
    })

    test('keeps the header inside the viewport without overlap', async ({ page }) => {
      await signIn(page)
      await scan(page, 'Bounds Oats', code(width, 4), 'stock_in')
      await page.reload()
      await expect(page.getByText('Bounds Oats')).toBeVisible()

      const title = await box(page.getByRole('heading', { name: 'Scan queue' }))
      const hint = await box(page.getByText('Scans add to stock'))
      const tabs = await box(page.getByRole('tablist', { name: 'Scan direction' }))
      const stockIn = await box(page.getByRole('tab', { name: /^Stock in/ }))
      const stockOut = await box(page.getByRole('tab', { name: /^Stock out/ }))
      const card = await box(page.locator('[data-testid^="scan-"]').first())

      for (const part of [title, hint, tabs]) {
        expect(part.x).toBeGreaterThanOrEqual(0)
        expect(part.x + part.width).toBeLessThanOrEqual(width)
      }
      expect(tabs.width).toBeLessThanOrEqual(736)
      expect(title.y + title.height).toBeLessThanOrEqual(hint.y + 1)
      expect(hint.y + hint.height).toBeLessThanOrEqual(tabs.y + 1)
      expect(tabs.y + tabs.height).toBeLessThanOrEqual(card.y + 1)
      expect(stockIn.x + stockIn.width).toBeLessThanOrEqual(stockOut.x + 1)
      expect(stockIn.height).toBeGreaterThanOrEqual(44)
      expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(width)
    })
  })
}
