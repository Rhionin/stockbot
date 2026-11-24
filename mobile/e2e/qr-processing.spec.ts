import { AxeBuilder } from '@axe-core/playwright'
import { expect, test, type Locator, type Page } from '@playwright/test'

const email = 'e2e@stockbot.test'
const password = 'correct horse'
const shots = process.env.Q8_SHOTS

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

const seeded: string[] = []

test.afterEach(async ({ page }) => {
  for (const id of seeded.splice(0)) await api(page, 'PATCH', `/api/scans/${id}`, { status: 'cancelled' })
})

async function signIn(page: Page) {
  await page.goto('/')
  await page.getByLabel('Email').fill(email)
  await page.getByLabel('Password').fill(password)
  await page.getByRole('button', { name: 'Sign in' }).click()
  await expect(page.getByRole('heading', { name: 'Scan queue' })).toBeVisible()
}

async function qrScan(page: Page, payload: string) {
  const posted = await json<{ id: string; status: string }>(page, 'POST', '/api/scans', {
    barcode: payload,
    direction: 'stock_in',
    userId: 'user-1',
  })
  expect(posted.status).toBe('qr')
  seeded.push(posted.id)
  return posted.id
}

async function qrIds(page: Page) {
  const rows = await json<{ id: string }[]>(page, 'GET', '/api/scans?userId=user-1&status=qr')
  return rows.map((row) => row.id)
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

async function expectCardFits(page: Page, card: Locator, width: number, children: Locator[]) {
  const outer = await box(card)
  expect(outer.x).toBeGreaterThanOrEqual(0)
  expect(outer.x + outer.width).toBeLessThanOrEqual(width)
  for (const child of children) {
    const inner = await box(child)
    expect(inner.x).toBeGreaterThanOrEqual(outer.x - 1)
    expect(inner.x + inner.width).toBeLessThanOrEqual(outer.x + outer.width + 1)
    expect(inner.y).toBeGreaterThanOrEqual(outer.y - 1)
    expect(inner.y + inner.height).toBeLessThanOrEqual(outer.y + outer.height + 1)
  }
  expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(width)
}

for (const width of [1280, 390]) {
  test.describe(`QR and processing cards at ${width}px`, () => {
    test.use({ viewport: { width, height: 844 } })

    test('shows a QR misread with its host only, and dismiss cancels it through the API', async ({ page }) => {
      await signIn(page)
      const id = await qrScan(page, `https://qr-${width}.example.com/groceries/milk?token=synthetic-secret`)
      await page.reload()

      const card = page.getByTestId(`qr-scan-${id}`)
      await expect(card).toBeVisible()
      await expect(card.getByText('This was a QR code, not the barcode')).toBeVisible()
      await expect(card.getByText(`qr-${width}.example.com`)).toBeVisible()
      await expect(page.getByText('synthetic-secret')).toHaveCount(0)
      await expect(card).toHaveAccessibleName(`This was a QR code, not the barcode. qr-${width}.example.com`)

      const rescan = card.getByRole('button', { name: 'Rescan the barcode' })
      const dismiss = card.getByRole('button', { name: 'Dismiss' })
      await expectCardFits(page, card, width, [rescan, dismiss])
      expect((await box(rescan)).height).toBeGreaterThanOrEqual(44)
      expect((await box(dismiss)).height).toBeGreaterThanOrEqual(44)
      await shot(page, 'qr-card', width)
      await expectNoViolations(page)

      expect(await qrIds(page)).toContain(id)
      await dismiss.click()
      await expect(card).toHaveCount(0)
      expect(await qrIds(page)).not.toContain(id)
      await expectNoViolations(page)
    })

    test('a QR misread dismissed through the API leaves the open queue without a reload', async ({ page }) => {
      await signIn(page)
      const id = await qrScan(page, `https://live-${width}.example.com/`)
      await expect(page.getByTestId(`qr-scan-${id}`)).toBeVisible({ timeout: 5000 })

      await json(page, 'PATCH', `/api/scans/${id}`, { status: 'cancelled' })

      await expect(page.getByTestId(`qr-scan-${id}`)).toHaveCount(0, { timeout: 5000 })
    })

    test('rescan goes to the scan screen', async ({ page }) => {
      await signIn(page)
      const id = await qrScan(page, `https://rescan-${width}.example.com/`)
      await page.reload()

      await page.getByTestId(`qr-scan-${id}`).getByRole('button', { name: 'Rescan the barcode' }).click()

      await expect(page).toHaveURL(/\/scan$/)
    })

    test('shows a lookup in progress as a status card, then clears it with the failure message', async ({ page }) => {
      await signIn(page)
      const processing =
        `event: scan_processing\ndata: ${JSON.stringify({
          id: `lookup-${width}`,
          userId: 'user-1',
          barcode: '0765123456789',
          direction: 'stock_in',
          scannedAt: new Date().toISOString(),
        })}\n\n`
      // The first stream can open before the queue screen listens, and a repeated id is ignored.
      const frames = [processing, processing, processing]
      await page.route('**/api/events', async (route) => {
        const next = frames.shift()
        if (next === undefined) {
          await route.continue()
          return
        }
        await route.fulfill({
          status: 200,
          headers: { 'Content-Type': 'text/event-stream', 'Cache-Control': 'no-cache' },
          body: next,
        })
      })
      await page.reload()

      const card = page.getByTestId(`processing-lookup-${width}`)
      await expect(card).toBeVisible({ timeout: 10_000 })
      await expect(card.getByText('Looking up product')).toBeVisible()
      await expect(card.getByText('Barcode: 0765123456789')).toBeVisible()
      await expect(card.getByText('Checking the open food database…')).toBeVisible()
      await expect(card).toHaveAccessibleName('Scan 0765123456789 processing')
      await expect(card.getByRole('status')).toBeVisible()
      await expectCardFits(page, card, width, [card.getByRole('status')])
      await shot(page, 'processing-card', width)
      await expectNoViolations(page)

      frames.splice(
        0,
        frames.length,
        `event: scan_processing_failed\ndata: ${JSON.stringify({
          id: `lookup-${width}`,
          barcode: '0765123456789',
          message: "Couldn't look up that barcode.",
        })}\n\n`,
      )
      await expect(card).toHaveCount(0, { timeout: 15_000 })
      await expect(page.getByText("Couldn't look up that barcode.")).toBeVisible()
      await shot(page, 'processing-failed', width)
      await expectNoViolations(page)
    })
  })
}
