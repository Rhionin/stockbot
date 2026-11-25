import { AxeBuilder } from '@axe-core/playwright'
import { expect, test, type Page } from '@playwright/test'

const email = 'e2e@stockbot.test'
const password = 'correct horse'
const shots = process.env.Q9_SHOTS
const INPUT = 'barcode-scanner-input'

interface ScanRow {
  id: string
  barcode: string
  direction: string
  status: string
  unitCount: number
}

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

async function openScans(page: Page): Promise<ScanRow[]> {
  const rows: ScanRow[] = []
  for (const status of ['pending', 'flagged', 'qr']) {
    rows.push(...(await json<ScanRow[]>(page, 'GET', `/api/scans?userId=user-1&status=${status}`)))
  }
  return rows
}

async function scansFor(page: Page, barcode: string) {
  return (await openScans(page)).filter((row) => row.barcode === barcode)
}

test.afterEach(async ({ page }) => {
  if (page.url() === 'about:blank') return
  for (const row of await openScans(page)) await api(page, 'PATCH', `/api/scans/${row.id}`, { status: 'cancelled' })
  await api(page, 'POST', '/api/scanner/mode', { mode: 'stock_in' })
})

async function signIn(page: Page) {
  await page.goto('/')
  await page.getByLabel('Email').fill(email)
  await page.getByLabel('Password').fill(password)
  await page.getByRole('button', { name: 'Sign in' }).click()
  await expect(page.getByRole('heading', { name: 'Scan queue' })).toBeVisible()
  await expect(scannerField(page)).toHaveCount(1)
}

async function product(page: Page, name: string, barcode: string) {
  await json(page, 'POST', '/api/products', { name, category: 'Pantry', unitOfMeasure: 'ea', barcode })
  const all = await json<{ id: string; name: string }[]>(page, 'GET', '/api/products')
  const productId = all.find((candidate) => candidate.name === name)?.id
  await json(page, 'POST', '/api/products/overrides', { barcode, productId })
}

async function blurScannerField(page: Page) {
  await page.evaluate(() => (document.activeElement as HTMLElement | null)?.blur())
  await expect(scannerField(page)).toHaveCount(0)
}

async function expectNoViolations(page: Page) {
  const result = await new AxeBuilder({ page }).analyze()
  expect(result.violations.map((v) => `${v.id}: ${v.help} (${v.nodes.length}) ${JSON.stringify(v.nodes.map((n) => n.html.slice(0, 160)))}`)).toEqual([])
}

async function expectNoSideScroll(page: Page, width: number) {
  expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(width)
}

async function shot(page: Page, name: string, width: number) {
  if (shots) await page.screenshot({ path: `${shots}/${name}-${width}.png`, fullPage: false })
}

function watchOrigins(page: Page, baseURL: string) {
  const foreign: string[] = []
  const origin = new URL(baseURL).origin
  page.on('request', (request) => {
    const url = new URL(request.url())
    if (url.protocol === 'data:' || url.protocol === 'blob:' || url.protocol === 'about:') return
    if (url.origin !== origin) foreign.push(request.url())
  })
  return foreign
}

const scannerField = (page: Page) => page.locator(`input[data-testid="${INPUT}"]:focus`)

const HIGHLIGHT = 'rgba(13, 148, 136, 0.1)'

for (const width of [1280, 390]) {
  const code = (n: number) => `97800${width}${String(n).padStart(4, '0')}`

  test.describe(`keyboard-wedge scanner at ${width}px`, () => {
    test.use({ viewport: { width, height: 844 } })

    test('a scan typed fast into the focused field makes the scan and highlights its card', async ({ page }) => {
      await signIn(page)
      await product(page, `Wedge Oats ${width}`, code(1))
      await page.reload()
      await expect(scannerField(page)).toHaveCount(1)
      const posts: string[] = []
      page.on('request', (request) => {
        if (request.method() === 'POST' && new URL(request.url()).pathname === '/api/scans') posts.push(request.postData() ?? '')
      })

      await page.keyboard.type(`${code(1)}\n`, { delay: 8 })

      await expect.poll(async () => (await scansFor(page, code(1))).length).toBe(1)
      const [row] = await scansFor(page, code(1))
      expect(row.direction).toBe('stock_in')
      expect(JSON.parse(posts[0])).toEqual({ barcode: code(1), direction: 'stock_in', userId: 'user-1' })
      const card = page.getByTestId(`scan-${row.id}`)
      await expect(card).toBeVisible()
      await expect(card.getByText(`Wedge Oats ${width}`)).toBeVisible()
      await expect(card).toHaveCSS('background-color', HIGHLIGHT)
      await shot(page, 'wedge-captured', width)
      await expectNoViolations(page)
      await expectNoSideScroll(page, width)
      await expect(card).not.toHaveCSS('background-color', HIGHLIGHT, { timeout: 8000 })
    })

    test('a scan typed anywhere on the page is read, and slow typing is not', async ({ page }) => {
      await signIn(page)
      await blurScannerField(page)

      await page.keyboard.type(`${code(2)}\n`, { delay: 8 })
      await expect.poll(async () => (await scansFor(page, code(2))).length).toBe(1)

      await blurScannerField(page)
      await page.keyboard.type(`${code(3)}\n`, { delay: 160 })
      await page.waitForTimeout(600)
      expect(await scansFor(page, code(3))).toEqual([])
    })

    test('typing in another field is left alone, and a sheet in front takes no scans', async ({ page }) => {
      await signIn(page)
      await page.getByRole('button', { name: 'Find an item', exact: true }).first().click()
      const dialog = page.getByRole('dialog', { name: 'Find an item' })
      await expect(dialog).toBeVisible()
      const search = dialog.getByRole('textbox', { name: 'Find an item', exact: true })

      await search.click()
      await page.keyboard.type(`${code(4)}\n`, { delay: 8 })
      await expect(search).toHaveValue(code(4))
      await page.waitForTimeout(400)
      expect(await scansFor(page, code(4))).toEqual([])

      await blurScannerField(page)
      await page.keyboard.type(`${code(5)}\n`, { delay: 8 })
      await page.waitForTimeout(400)
      expect(await scansFor(page, code(5))).toEqual([])
    })

    test('repeated reads of one code make one scan, and the control codes switch the mode', async ({ page }) => {
      await signIn(page)
      const config = await json<{ stockOutBarcode: string; stockInBarcode: string }>(page, 'GET', '/api/scanner/config')
      const posts: string[] = []
      page.on('request', (request) => {
        if (request.method() === 'POST' && new URL(request.url()).pathname === '/api/scans') posts.push(request.postData() ?? '')
      })

      await page.keyboard.type(`${code(6)}\n`, { delay: 8 })
      await expect.poll(async () => (await scansFor(page, code(6))).length).toBe(1)
      await page.keyboard.type(`${code(6)}\n`, { delay: 8 })
      await page.waitForTimeout(500)
      expect(posts).toHaveLength(1)

      await page.keyboard.type(`${config.stockOutBarcode}\n`, { delay: 8 })
      await expect(page.getByRole('tab', { name: /^Stock out/ })).toHaveAttribute('aria-selected', 'true')
      expect((await json<{ currentMode: string }>(page, 'GET', '/api/scanner/config')).currentMode).toBe('stock_out')
      expect(posts).toHaveLength(1)

      await page.keyboard.type(`${code(7)}\n`, { delay: 8 })
      await expect.poll(async () => (await scansFor(page, code(7))).map((row) => row.direction)).toEqual(['stock_out'])

      await page.keyboard.type(`${config.stockInBarcode}\n`, { delay: 8 })
      await expect(page.getByRole('tab', { name: /^Stock in/ })).toHaveAttribute('aria-selected', 'true')
    })

    test('the new card is scrolled into view when the queue is long', async ({ page }) => {
      await signIn(page)
      for (let n = 10; n < 24; n += 1) {
        await json(page, 'POST', '/api/scans', { barcode: code(n), direction: 'stock_in', userId: 'user-1' })
      }
      await page.reload()
      await expect(page.locator('[data-testid^="scan-"]').first()).toBeVisible()
      await expect(scannerField(page)).toHaveCount(1)
      const cards = page.locator('[data-testid^="scan-"]')
      await cards.last().scrollIntoViewIfNeeded()
      await expect(cards.first()).not.toBeInViewport()

      await page.keyboard.type(`${code(30)}\n`, { delay: 8 })

      await expect.poll(async () => (await scansFor(page, code(30))).length).toBe(1)
      const [row] = await scansFor(page, code(30))
      await expect(page.getByTestId(`scan-${row.id}`)).toBeInViewport({ ratio: 0.99 })
      await shot(page, 'wedge-scrolled', width)
    })

    test('rescan on a QR card focuses the scanner field instead of leaving the queue', async ({ page }) => {
      await signIn(page)
      const posted = await json<{ id: string }>(page, 'POST', '/api/scans', {
        barcode: `https://qr-${width}.example.com/x`,
        direction: 'stock_in',
        userId: 'user-1',
      })
      await expect(page.getByTestId(`qr-scan-${posted.id}`)).toBeVisible({ timeout: 8000 })
      await blurScannerField(page)

      await page.getByTestId(`qr-scan-${posted.id}`).getByRole('button', { name: 'Rescan the barcode' }).click()

      await expect(scannerField(page)).toHaveCount(1)
      expect(new URL(page.url()).pathname).toBe('/')
      await page.keyboard.type(`${code(8)}\n`, { delay: 8 })
      await expect.poll(async () => (await scansFor(page, code(8))).length).toBe(1)
    })

    test('the scan screen reads a wedge scan and loads nothing from another origin', async ({ page, baseURL }) => {
      const foreign = watchOrigins(page, baseURL as string)
      await signIn(page)
      await page.getByRole('tab', { name: 'Scan' }).click()
      await expect(page).toHaveURL(/\/scan$/)
      await expect(scannerField(page)).toHaveCount(1)
      await expect(page.getByText('The camera reads the barcode on the package.')).toBeVisible()
      await shot(page, 'scan-screen', width)
      await expectNoViolations(page)
      await expectNoSideScroll(page, width)

      await page.keyboard.type(`${code(9)}\n`, { delay: 8 })

      await expect.poll(async () => (await scansFor(page, code(9))).length).toBe(1)
      await expect(page.getByRole('main').getByText(/needs a name on the pantry site|Added /)).toBeVisible()
      await shot(page, 'scan-screen-captured', width)
      expect(foreign).toEqual([])
    })

    test('only the screen in front reads a scan', async ({ page }) => {
      await signIn(page)
      await page.getByRole('tab', { name: 'Scan' }).click()
      await expect(scannerField(page)).toHaveCount(1)
      await page.getByRole('tab', { name: 'Queue' }).click()
      await expect(page.getByRole('heading', { name: 'Scan queue' })).toBeVisible()
      await expect(scannerField(page)).toHaveCount(1)
      const posts: string[] = []
      page.on('request', (request) => {
        if (request.method() === 'POST' && new URL(request.url()).pathname === '/api/scans') posts.push(request.postData() ?? '')
      })

      await blurScannerField(page)
      await page.keyboard.type(`${code(11)}\n`, { delay: 8 })

      await expect.poll(async () => (await scansFor(page, code(11))).length).toBe(1)
      expect(posts).toHaveLength(1)
    })
  })
}
