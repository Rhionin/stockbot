import AxeBuilder from '@axe-core/playwright'
import { expect, test, type APIRequestContext, type Locator, type Page, type TestInfo } from '@playwright/test'
import { writeFileSync } from 'node:fs'
import { resetSharedState } from './helpers'

const minimumContrast = 4.5

// Deliberately separate from src/contrast.ts, so a wrong gamma there cannot
// also satisfy this check.
function channels(color: string): [number, number, number, number] {
  const match = color.match(/rgba?\((\d+),\s*(\d+),\s*(\d+)(?:,\s*([\d.]+))?\)/)
  if (!match) throw new Error(`Unparsed color: ${color}`)
  return [Number(match[1]), Number(match[2]), Number(match[3]), match[4] === undefined ? 1 : Number(match[4])]
}

function contrast(foreground: string, background: string) {
  const lin = (channel: number) => {
    const value = channel / 255
    return value <= 0.04045 ? value / 12.92 : ((value + 0.055) / 1.055) ** 2.4
  }
  const lum = (rgb: [number, number, number]) => 0.2126 * lin(rgb[0]) + 0.7152 * lin(rgb[1]) + 0.0722 * lin(rgb[2])
  const [foregroundRed, foregroundGreen, foregroundBlue] = channels(foreground)
  const [backgroundRed, backgroundGreen, backgroundBlue] = channels(background)
  const lighter = Math.max(
    lum([foregroundRed, foregroundGreen, foregroundBlue]),
    lum([backgroundRed, backgroundGreen, backgroundBlue]),
  )
  const darker = Math.min(
    lum([foregroundRed, foregroundGreen, foregroundBlue]),
    lum([backgroundRed, backgroundGreen, backgroundBlue]),
  )
  return (lighter + 0.05) / (darker + 0.05)
}

async function setScheme(page: Page, app: 'light' | 'dark', os: 'light' | 'dark') {
  await page.emulateMedia({ colorScheme: os })
  await page.evaluate((value) => {
    window.localStorage.setItem('mantine-color-scheme-value', value)
  }, app)
  await page.reload()
  await expect.poll(() => page.evaluate(() => document.documentElement.getAttribute('data-mantine-color-scheme'))).toBe(app)
  await expect.poll(() => page.evaluate(() => window.matchMedia('(prefers-color-scheme: dark)').matches)).toBe(os === 'dark')
}

async function paint(locator: Locator) {
  return locator.evaluate((el) => {
    const parse = (color: string) => {
      const match = color.match(/rgba?\((\d+),\s*(\d+),\s*(\d+)(?:,\s*([\d.]+))?\)/)
      if (!match) return null
      return {
        r: Number(match[1]),
        g: Number(match[2]),
        b: Number(match[3]),
        a: match[4] === undefined ? 1 : Number(match[4]),
      }
    }
    const over = (
      front: { r: number; g: number; b: number; a: number },
      back: { r: number; g: number; b: number; a: number },
    ) => ({
      r: front.r * front.a + back.r * (1 - front.a),
      g: front.g * front.a + back.g * (1 - front.a),
      b: front.b * front.a + back.b * (1 - front.a),
      a: front.a + back.a * (1 - front.a),
    })
    const layers: { r: number; g: number; b: number; a: number }[] = []
    let node: Element | null = el
    while (node) {
      const background = parse(getComputedStyle(node).backgroundColor)
      if (background && background.a > 0) layers.push(background)
      if (background && background.a >= 0.99) break
      node = node.parentElement
    }
    if (layers.length === 0 || layers[layers.length - 1].a < 0.99) {
      layers.push({ r: 255, g: 255, b: 255, a: 1 })
    }
    let solid = layers[layers.length - 1]
    for (let index = layers.length - 2; index >= 0; index -= 1) solid = over(layers[index], solid)
    const ink = parse(getComputedStyle(el).color) ?? { r: 0, g: 0, b: 0, a: 1 }
    const painted = over(ink, solid)
    const rgb = (color: { r: number; g: number; b: number }) =>
      `rgb(${Math.round(color.r)}, ${Math.round(color.g)}, ${Math.round(color.b)})`
    return { foreground: rgb(painted), background: rgb(solid) }
  })
}

function violationText(violations: { id: string; help: string; nodes: { target: string[]; html: string }[] }[]) {
  return violations.map((violation) => (
    `${violation.id}: ${violation.help} (${violation.nodes.map((node) => `${node.target.join(' ')} ${node.html.slice(0, 140)}`).join('; ')})`
  )).join('\n')
}

async function expectContrast(locator: Locator) {
  const sample = await paint(locator)
  const ratio = contrast(sample.foreground, sample.background)
  expect(ratio, JSON.stringify(sample)).toBeGreaterThanOrEqual(minimumContrast)
  return { ...sample, ratio: Math.round(ratio * 100) / 100 }
}

async function shot(locator: Locator, testInfo: TestInfo, name: string) {
  await locator.screenshot({ path: testInfo.outputPath(`${name}.png`) })
}

function colorContrast(page: Page) {
  return new AxeBuilder({ page }).withRules(['color-contrast'])
}

async function expectAxe(page: Page, where: string) {
  const result = await colorContrast(page).analyze()
  expect(result.violations, `${where}\n${violationText(result.violations)}`).toEqual([])
}

async function stock(request: APIRequestContext, barcode: string, direction: 'stock_in' | 'stock_out', unitCount: number) {
  const scan = await request.post('/api/scans', {
    data: { barcode, direction, userId: 'user-1', unitCount },
  })
  expect(scan.ok()).toBe(true)
  const entry = await scan.json() as { id: string }
  const commit = await request.post(`/api/scans/${entry.id}/commit`, { data: {} })
  expect(commit.ok(), await commit.text()).toBe(true)
}

async function createProduct(request: APIRequestContext, name: string, category: string, unitOfMeasure: string, barcode: string) {
  const created = await request.post('/api/products', { data: { name, category, unitOfMeasure } })
  expect(created.ok()).toBe(true)
  const products = await request.get('/api/products')
  const product = (await products.json() as { id: string; name: string }[]).find((row) => row.name === name)
  if (!product) throw new Error(`${name} was not created`)
  const override = await request.post('/api/products/overrides', { data: { barcode, productId: product.id } })
  expect(override.ok()).toBe(true)
  return product
}

async function seedShopping(request: APIRequestContext) {
  const started = await request.post('/api/onboarding/complete')
  expect(started.ok()).toBe(true)
  await createProduct(request, 'Rolled oats', 'Breakfast', 'bag', '0000002662662')
  await stock(request, '0000002662662', 'stock_in', 2)
  await stock(request, '0000002662662', 'stock_out', 1)
  const filled = await request.post('/api/shopping-list/fill')
  expect(filled.ok(), await filled.text()).toBe(true)
  await createProduct(request, 'Plain rice', 'Dry goods', 'bag', '0000002662661')
  await stock(request, '0000002662661', 'stock_in', 1)
  const inventory = await request.get('/api/inventory')
  expect(inventory.ok()).toBe(true)
  const rows = await inventory.json() as { item: { id: string; product: { name: string } } }[]
  const rice = rows.find((row) => row.item.product.name === 'Plain rice')
  if (!rice) throw new Error('inventory is missing Plain rice')
  const added = await request.post('/api/shopping-list/items', { data: { itemId: rice.item.id, quantity: 1 } })
  expect(added.ok()).toBe(true)
}

const modes = [
  { app: 'light', os: 'light' },
  { app: 'light', os: 'dark' },
  { app: 'dark', os: 'light' },
  { app: 'dark', os: 'dark' },
] as const

const widths = [390, 1440] as const

const routes = [
  { path: '/', name: 'scan queue', ready: 'Scans add to stock' },
  { path: '/shopping', name: 'shopping', ready: 'Kroger is unconfigured.' },
  { path: '/settings', name: 'settings', ready: 'Wipe inventory' },
  { path: '/diagnostics', name: 'diagnostics', ready: 'Diagnostics' },
  { path: '/inventory?review=1', name: 'inbox', ready: 'Suggestions' },
  { path: '/history', name: 'history', ready: 'History', heading: true },
  { path: '/inventory', name: 'inventory', ready: 'Inventory', heading: true },
] as const

// Literal, so dropping a mode, a route, or a width fails even when the loops
// are rewritten to match the shorter list. Home stays in the list.
const expectedCombinations = 56

test.beforeEach(async ({ request }) => {
  await resetSharedState(request)
})

test.afterEach(async ({ request }) => {
  await resetSharedState(request)
})

test('text contrast meets WCAG AA in every theme combination', async ({ page, request }, testInfo) => {
  test.setTimeout(240_000)
  const axePrototype = AxeBuilder.prototype as AxeBuilder & { exclude(selector: string): AxeBuilder }
  const originalExclude = axePrototype.exclude
  axePrototype.exclude = () => {
    throw new Error('color-contrast checks must not exclude selectors')
  }
  expect(routes.map((route) => route.path)).toEqual([
    '/',
    '/shopping',
    '/settings',
    '/diagnostics',
    '/inventory?review=1',
    '/history',
    '/inventory',
  ])
  expect(modes).toHaveLength(4)
  expect(widths).toHaveLength(2)
  await seedShopping(request)
  // The API does not send basis. Attach it in the browser so the stock-note
  // ink renders without changing what the server returns.
  await page.route('**/api/shopping-list**', async (route) => {
    const incoming = route.request()
    if (incoming.method() !== 'GET' || new URL(incoming.url()).pathname !== '/api/shopping-list') {
      await route.continue()
      return
    }
    const response = await route.fetch()
    const rows = await response.json() as { basis?: { instanceCount: number } }[]
    if (Array.isArray(rows)) {
      for (const row of rows) row.basis = { instanceCount: 1 }
    }
    await route.fulfill({ response, json: rows })
  })
  const measured: { name: string; app: string; os: string; width: number; foreground: string; background: string; ratio: number }[] = []
  let combinations = 0

  try {
    for (const width of widths) {
      await page.setViewportSize({ width, height: width === 390 ? 844 : 900 })
      for (const mode of modes) {
        for (const route of routes) {
          await page.goto(route.path)
          await setScheme(page, mode.app, mode.os)
          const landmark = 'heading' in route
            ? page.getByRole('heading', { name: route.ready })
            : page.getByText(route.ready).first()
          await expect(landmark).toBeVisible()
          const where = `${route.name} ${mode.app}/${mode.os} ${width}`
          if (route.path === '/') {
            const hint = page.getByText('Scans add to stock')
            const status = page.getByText('Scanner disconnected')
            await expect(status).toBeVisible()
            measured.push({ name: 'scan-direction-hint', ...mode, width, ...await expectContrast(hint) })
            measured.push({ name: 'scan-status', ...mode, width, ...await expectContrast(status) })
          }
          if (route.path === '/shopping') {
            await expect(page.getByText('Derived').first()).toBeVisible()
            await expect(page.getByText(/\d+ on hand/).first()).toBeVisible()
            await expect(page.getByText(/replacing \d+ you used/).first()).toBeVisible()
            const kroger = page.getByText('Kroger is unconfigured.')
            const remove = page.getByRole('button', { name: 'Remove Plain rice' })
            const note = page.getByText(/\d+ on hand/).first()
            measured.push({ name: 'kroger-unconfigured', ...mode, width, ...await expectContrast(kroger) })
            measured.push({ name: 'remove-plain-rice', ...mode, width, ...await expectContrast(remove) })
            measured.push({ name: 'stock-note', ...mode, width, ...await expectContrast(note) })
            if (mode.app === mode.os) {
              await shot(kroger, testInfo, `kroger-unconfigured-${mode.app}-${width}`)
              await shot(remove, testInfo, `remove-plain-rice-${mode.app}-${width}`)
            }
          }
          if (route.path === '/settings') {
            const wipe = page.getByRole('button', { name: 'Wipe inventory' })
            measured.push({ name: 'settings-wipe', ...mode, width, ...await expectContrast(wipe) })
          }
          if (route.path === '/shopping' || route.path === '/diagnostics') {
            const heading = page.getByRole('heading', { level: 2 }).first()
            await expect(heading).toBeVisible()
            measured.push({ name: `${route.name}-h2`, ...mode, width, ...await expectContrast(heading) })
          }
          await expectAxe(page, where)
          combinations += 1
          if (route.path === '/') {
            await page.getByRole('button', { name: 'Scan with camera' }).click()
            const alert = page.getByRole('alert').filter({ hasText: 'Camera scanning unavailable' })
            await expect(alert).toBeVisible()
            const title = alert.locator('.mantine-Alert-title')
            measured.push({ name: 'yellow-alert', ...mode, width, ...await expectContrast(title) })
            await expectAxe(page, `${where} yellow alert`)
          }
        }
      }
    }
    expect(combinations).toBe(expectedCombinations)

    const saved = await request.put('/api/providers/kroger/credentials', {
      data: {
        clientId: 'contrast-client',
        clientSecret: 'contrast-secret',
        redirectUri: 'http://127.0.0.1:5173/shopping',
        modality: 'PICKUP',
      },
    })
    expect(saved.ok()).toBe(true)

    for (const width of widths) {
      await page.setViewportSize({ width, height: width === 390 ? 844 : 900 })
      for (const mode of modes) {
        await page.goto('/shopping')
        await setScheme(page, mode.app, mode.os)
        await page.getByRole('button', { name: 'Menu' }).click()
        await page.getByRole('menuitem', { name: 'Manage Kroger connection' }).click()
        const dialog = page.getByRole('dialog', { name: 'Kroger connection' })
        await expect(dialog).toBeVisible()
        await expect(dialog).toHaveCSS('opacity', '1')
        const body = dialog.getByText('A client secret is saved and is not shown.')
        await expect(body).toBeVisible()
        measured.push({ name: 'dialog-body', ...mode, width, ...await expectContrast(body) })
        if (mode.app === mode.os) await shot(body, testInfo, `dialog-body-${mode.app}-${width}`)
        const dialogAxe = await colorContrast(page).include('[role="dialog"]').analyze()
        expect(dialogAxe.violations, violationText(dialogAxe.violations)).toEqual([])
        await page.keyboard.press('Escape')
      }
    }
  } finally {
    axePrototype.exclude = originalExclude
    writeFileSync(testInfo.outputPath('ratios.json'), JSON.stringify(measured, null, 2))
    await request.delete('/api/providers/kroger/credentials')
  }
})
