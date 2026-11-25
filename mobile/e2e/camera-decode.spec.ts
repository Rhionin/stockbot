import { AxeBuilder } from '@axe-core/playwright'
import { expect, test, type Page } from '@playwright/test'
import { join } from 'node:path'

const email = 'e2e@stockbot.test'
const password = 'correct horse'
const shots = process.env.Q9_SHOTS
// One synthetic EAN-13 on a white card, looped as the browser's only camera.
// scripts/make-fake-camera-video.py writes it.
const fakeCamera = join(__dirname, 'fixtures', 'ean13-4006381333931.mjpeg')
const BARCODE = '4006381333931'

test.use({
  permissions: ['camera'],
  launchOptions: {
    args: [
      '--use-fake-device-for-media-stream',
      '--use-fake-ui-for-media-stream',
      `--use-file-for-fake-video-capture=${fakeCamera}`,
    ],
  },
})

interface ScanRow {
  id: string
  barcode: string
  direction: string
  status: string
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

async function openScans(page: Page): Promise<ScanRow[]> {
  const rows: ScanRow[] = []
  for (const status of ['pending', 'flagged', 'qr']) {
    const res = await api(page, 'GET', `/api/scans?userId=user-1&status=${status}`)
    expect(res.status, res.body).toBeLessThan(300)
    rows.push(...(JSON.parse(res.body) as ScanRow[]))
  }
  return rows
}

for (const width of [1280, 390]) {
  test.describe(`camera decode at ${width}px`, () => {
    test.use({ viewport: { width, height: 844 } })

    test.afterEach(async ({ page }) => {
      if (page.url() === 'about:blank') return
      for (const row of await openScans(page)) await api(page, 'PATCH', `/api/scans/${row.id}`, { status: 'cancelled' })
    })

    test('reads an EAN-13 from the camera with the WASM served from this origin', async ({ page, baseURL }) => {
      const origin = new URL(baseURL as string).origin
      const foreign: string[] = []
      const loaded: string[] = []
      page.on('request', (request) => {
        const url = new URL(request.url())
        if (['data:', 'blob:', 'about:'].includes(url.protocol)) return
        if (url.origin !== origin) foreign.push(request.url())
        else loaded.push(url.pathname)
      })

      await page.goto('/')
      await page.getByLabel('Email').fill(email)
      await page.getByLabel('Password').fill(password)
      await page.getByRole('button', { name: 'Sign in' }).click()
      await expect(page.getByRole('heading', { name: 'Scan queue' })).toBeVisible()
      await page.getByRole('tab', { name: 'Scan' }).click()
      const allow = page.getByRole('button', { name: 'Allow the camera' })
      if (await allow.isVisible({ timeout: 3000 }).catch(() => false)) await allow.click()
      await expect(page.locator('video')).toBeVisible({ timeout: 15_000 })

      await expect.poll(async () => (await openScans(page)).filter((row) => row.barcode === BARCODE).length, {
        timeout: 30_000,
      }).toBe(1)
      await expect(page.getByRole('main').getByText(/needs a name on the pantry site|Added /)).toBeVisible()
      await page.waitForTimeout(2500)
      expect((await openScans(page)).filter((row) => row.barcode === BARCODE)).toHaveLength(1)

      const nativeDetector = await page.evaluate(() => typeof (globalThis as { BarcodeDetector?: unknown }).BarcodeDetector !== 'undefined')
      if (!nativeDetector) expect(loaded).toContain('/zxing/zxing_reader.wasm')
      expect(foreign).toEqual([])

      if (shots) await page.screenshot({ path: `${shots}/camera-decoded-${width}.png`, fullPage: false })
      const result = await new AxeBuilder({ page }).analyze()
      expect(result.violations.map((v) => `${v.id}: ${v.help} (${v.nodes.length})`)).toEqual([])
      expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(width)
    })
  })
}
