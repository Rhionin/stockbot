import { AxeBuilder } from '@axe-core/playwright'
import { expect, test, type Locator, type Page } from '@playwright/test'

const email = 'e2e@stockbot.test'
const password = 'correct horse'
const shots = process.env.Q7_SHOTS

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

interface Hit {
  productId: string
  itemId: string | null
  name: string
  brand?: string
  size?: string
  category?: string
  onHand: number
}

interface ProductRow {
  id: string
  name: string
  brand?: string
  size?: string
  category: string
}

async function hits(page: Page, query: string) {
  return json<Hit[]>(page, 'GET', `/api/products/search?q=${encodeURIComponent(query)}`)
}

async function onHand(page: Page, name: string) {
  const found = (await hits(page, name)).find((hit) => hit.name === name)
  expect(found, `${name} should be searchable`).toBeDefined()
  return (found as Hit).onHand
}

async function signIn(page: Page) {
  await page.goto('/')
  await page.getByLabel('Email').fill(email)
  await page.getByLabel('Password').fill(password)
  await page.getByRole('button', { name: 'Sign in' }).click()
  await expect(page.getByRole('heading', { name: 'Scan queue' })).toBeVisible()
}

async function setMode(page: Page, mode: 'stock_in' | 'stock_out') {
  await json(page, 'POST', '/api/scanner/mode', { mode })
  await page.reload()
  await expect(page.getByRole('heading', { name: 'Scan queue' })).toBeVisible()
  await expect(page.getByRole('tab', { name: mode === 'stock_in' ? /^Stock in/ : /^Stock out/ })).toHaveAttribute('aria-selected', 'true')
}

test.afterEach(async ({ page }) => {
  await api(page, 'POST', '/api/scanner/mode', { mode: 'stock_in' })
})

async function box(locator: Locator) {
  const found = await locator.boundingBox()
  expect(found).not.toBeNull()
  return found as { x: number; y: number; width: number; height: number }
}

async function expectNoViolations(page: Page) {
  const result = await new AxeBuilder({ page }).analyze()
  expect(result.violations.map((v) => `${v.id}: ${v.help} (${v.nodes.length}) ${JSON.stringify(v.nodes.map((n) => n.html.slice(0, 160)))}`)).toEqual([])
}

async function shot(page: Page, name: string, width: number) {
  if (shots) await page.screenshot({ path: `${shots}/${name}-${width}.png`, fullPage: false })
}

const findDialog = (page: Page) => page.getByRole('dialog', { name: 'Find an item' })
const addDialog = (page: Page) => page.getByRole('dialog', { name: 'Add an item' })
const findButton = (page: Page) => page.getByRole('button', { name: 'Find an item', exact: true }).first()

async function openFind(page: Page) {
  await findButton(page).click()
  await expect(findDialog(page)).toBeVisible()
  await expect(findDialog(page).getByRole('heading', { name: 'Find an item', level: 2 })).toBeFocused()
}

function search(page: Page) {
  return findDialog(page).getByRole('textbox', { name: 'Find an item', exact: true })
}

async function expectInside(locator: Locator, width: number, minHeight = 0) {
  const found = await box(locator)
  expect(found.x).toBeGreaterThanOrEqual(0)
  expect(found.x + found.width).toBeLessThanOrEqual(width)
  expect(found.height).toBeGreaterThanOrEqual(minHeight)
}

async function expectSheetLayout(page: Page, dialog: Locator, width: number) {
  expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(width)
  const heading = await box(dialog.getByRole('heading', { level: 2 }))
  expect(heading.x).toBeGreaterThanOrEqual(0)
  expect(heading.x + heading.width).toBeLessThanOrEqual(width)
  const close = dialog.getByRole('button', { name: 'Close' })
  await expectInside(close, width, 44)
  for (const field of await dialog.getByRole('textbox').all()) await expectInside(field, width, 44)
  for (const button of await dialog.getByRole('button').all()) await expectInside(button, width, 44)
  if (width >= 768) {
    const first = await box(dialog.getByRole('heading', { level: 2 }))
    const closeBox = await box(close)
    expect(closeBox.x + closeBox.width - first.x).toBeLessThanOrEqual(560)
  }
}

function toast(page: Page, text: string) {
  return page.getByTestId('confirm-toast').filter({ hasText: text })
}

for (const width of [1280, 390]) {
  test.describe(`find item at ${width}px`, () => {
    test.use({ viewport: { width, height: 844 } })

    test('stocks an existing item in and out and the on-hand count follows', async ({ page }) => {
      await signIn(page)
      const name = `E2E Find Rice ${width}`
      await json(page, 'POST', '/api/products', { name, brand: 'Millhouse', size: '32 oz', category: 'Pantry', unitOfMeasure: '', createItem: true })
      expect(await onHand(page, name)).toBe(0)
      await page.reload()
      await setMode(page, 'stock_in')
      await shot(page, 'queue-button', width)
      await expectNoViolations(page)
      await expectInside(findButton(page), width, 44)

      const writes: { method: string; url: string; body: string }[] = []
      page.on('request', (request) => {
        if (request.method() !== 'GET') writes.push({ method: request.method(), url: new URL(request.url()).pathname, body: request.postData() ?? '' })
      })

      await openFind(page)
      await expect(findDialog(page).getByText('Scans add to stock')).toBeVisible()
      await expectSheetLayout(page, findDialog(page), width)
      await shot(page, 'find-empty', width)
      await expectNoViolations(page)

      await search(page).fill('E')
      await expect(findDialog(page).getByText('Type at least 2 letters.')).toBeVisible()
      await expect(search(page)).toHaveAttribute('aria-invalid', 'true')
      await page.waitForTimeout(600)
      expect((await page.evaluate(() => performance.getEntriesByType('resource').map((entry) => entry.name))).filter((url) => url.includes('/api/products/search'))).toEqual([])
      await shot(page, 'find-too-short', width)

      await search(page).fill(`Find Rice ${width}`)
      const list = findDialog(page).getByRole('list', { name: 'Search results' })
      await expect(list).toBeVisible()
      const row = findDialog(page).getByRole('button', { name: `${name} Millhouse · 32 oz None on hand` })
      await expect(row).toBeVisible()
      await expect(list.getByRole('listitem')).toHaveCount(1)
      await expectInside(row, width, 44)
      await expect(findDialog(page).getByRole('button', { name: `Add "Find Rice ${width}"` })).toBeVisible()
      await expectSheetLayout(page, findDialog(page), width)
      await shot(page, 'find-results', width)
      await expectNoViolations(page)

      await row.click()
      await expect(toast(page, `Added 1 ${name}.`)).toBeVisible()
      await expect(findDialog(page)).toHaveCount(0)
      await expect(findButton(page)).toBeFocused()
      expect(await onHand(page, name)).toBe(1)
      await shot(page, 'stocked-in-toast', width)
      await expectNoViolations(page)

      await toast(page, `Added 1 ${name}.`).getByRole('button', { name: 'Undo' }).click()
      await expect.poll(() => onHand(page, name)).toBe(0)
      await openFind(page)
      await search(page).fill(`Find Rice ${width}`)
      await findDialog(page).getByRole('button', { name: new RegExp(`^${name}`) }).click()
      await expect(toast(page, `Added 1 ${name}.`)).toBeVisible()
      expect(await onHand(page, name)).toBe(1)

      await setMode(page, 'stock_out')
      await openFind(page)
      await expect(findDialog(page).getByText('Scans remove from stock')).toBeVisible()
      await search(page).fill(`Find Rice ${width}`)
      const stockedRow = findDialog(page).getByRole('button', { name: `${name} Millhouse · 32 oz 1 on hand` })
      await expect(stockedRow).toBeVisible()
      await shot(page, 'find-results-stock-out', width)
      await stockedRow.click()
      await expect(toast(page, `Took out 1 ${name}.`)).toBeVisible()
      await expect(findDialog(page)).toHaveCount(0)
      expect(await onHand(page, name)).toBe(0)

      expect(writes.filter((write) => write.url.endsWith('/stock-out')).map((write) => write.body)).toEqual(['{}'])
      expect(writes.filter((write) => write.url.endsWith('/instances') && write.method === 'POST')).toHaveLength(2)
    })

    test('asks before adding and taking out an item with none on hand', async ({ page }) => {
      await signIn(page)
      const name = `E2E Find Beans ${width}`
      await json(page, 'POST', '/api/products', { name, category: 'Pantry', unitOfMeasure: '', createItem: true })
      await page.reload()
      await setMode(page, 'stock_out')

      await openFind(page)
      await search(page).fill(`Find Beans ${width}`)
      await findDialog(page).getByRole('button', { name: `${name} None on hand` }).click()

      const region = findDialog(page).getByRole('region', { name: 'None on hand' })
      await expect(region).toBeVisible()
      await expect(region.getByText(`You have 0 of ${name} on hand. If you had one anyway, this adds it and takes it out in one step.`)).toBeVisible()
      await expect(findDialog(page).getByRole('list', { name: 'Search results' })).toHaveCount(0)
      expect(await onHand(page, name)).toBe(0)
      await expectSheetLayout(page, findDialog(page), width)
      await shot(page, 'find-none-on-hand', width)
      await expectNoViolations(page)

      await region.getByRole('button', { name: 'Back' }).click()
      await expect(findDialog(page).getByRole('list', { name: 'Search results' })).toBeVisible()
      await expect(search(page)).toBeFocused()

      await findDialog(page).getByRole('button', { name: `${name} None on hand` }).click()
      await region.getByRole('button', { name: 'Add it and take it out' }).click()
      await expect(toast(page, `Added and took out 1 ${name}.`)).toBeVisible()
      await expect(findDialog(page)).toHaveCount(0)
      expect(await onHand(page, name)).toBe(0)
      const history = await json<{ moves: { direction: string }[] }>(page, 'GET', '/api/history')
      test.info().annotations.push({ type: 'history', description: JSON.stringify(history).slice(0, 200) })
    })

    test('adds a new item that is not in the catalog and stocks it in', async ({ page }) => {
      await signIn(page)
      const name = `E2E Find Peach Jam ${width}`
      await setMode(page, 'stock_in')
      expect((await hits(page, name)).some((hit) => hit.name === name)).toBe(false)

      await openFind(page)
      await search(page).fill(name)
      await expect(findDialog(page).getByText(`Nothing called "${name}" yet.`)).toBeVisible()
      await findDialog(page).getByRole('button', { name: `Add "${name}"` }).click()

      await expect(addDialog(page)).toBeVisible()
      await expect(findDialog(page)).toHaveCount(0)
      await expect(addDialog(page).getByRole('heading', { name: 'Add an item', level: 2 })).toBeFocused()
      const nameField = addDialog(page).getByRole('textbox', { name: 'Name', exact: true })
      await expect(nameField).toHaveValue(name)
      await expectSheetLayout(page, addDialog(page), width)
      await shot(page, 'add-item', width)
      await expectNoViolations(page)

      await nameField.fill('   ')
      await addDialog(page).getByRole('button', { name: 'Add and stock in' }).click()
      await expect(addDialog(page).getByText('Enter a name.')).toBeVisible()
      await expect(nameField).toHaveAttribute('aria-invalid', 'true')
      await expect(nameField).toBeFocused()
      expect((await hits(page, name)).some((hit) => hit.name === name)).toBe(false)
      await shot(page, 'add-item-error', width)
      await expectNoViolations(page)

      await nameField.fill(name)
      await addDialog(page).getByRole('textbox', { name: 'Brand', exact: true }).fill('Orchard')
      await addDialog(page).getByRole('textbox', { name: 'Size', exact: true }).fill('12 oz')
      await addDialog(page).getByRole('textbox', { name: 'Category', exact: true }).fill('Preserves')
      await shot(page, 'add-item-filled', width)
      await addDialog(page).getByRole('button', { name: 'Add and stock in' }).click()

      await expect(toast(page, `Added 1 ${name}.`)).toBeVisible()
      await expect(addDialog(page)).toHaveCount(0)
      await expect(findDialog(page)).toHaveCount(0)
      await expect(findButton(page)).toBeFocused()
      const created = (await json<ProductRow[]>(page, 'GET', '/api/products')).filter((product) => product.name === name)
      expect(created).toHaveLength(1)
      expect(created[0].category).toBe('Preserves')
      const found = (await hits(page, name)).filter((hit) => hit.name === name)
      expect(found).toHaveLength(1)
      expect(found[0]).toMatchObject({ brand: 'Orchard', size: '12 oz', category: 'Preserves' })
      expect(await onHand(page, name)).toBe(1)
    })

    test('adds a new item and takes it out in one step on the stock out tab', async ({ page }) => {
      await signIn(page)
      const name = `E2E Find Plum Jam ${width}`
      await setMode(page, 'stock_out')

      await openFind(page)
      await search(page).fill(name)
      await findDialog(page).getByRole('button', { name: `Add "${name}"` }).click()
      await addDialog(page).getByRole('button', { name: 'Add it and take it out' }).click()

      await expect(toast(page, `Added and took out 1 ${name}.`)).toBeVisible()
      expect((await json<ProductRow[]>(page, 'GET', '/api/products')).filter((product) => product.name === name)).toHaveLength(1)
      expect(await onHand(page, name)).toBe(0)
    })

    test('closes with Escape one sheet at a time and hands focus back', async ({ page }) => {
      await signIn(page)
      const name = `E2E Find Never Saved ${width}`
      await setMode(page, 'stock_in')
      await openFind(page)
      await search(page).fill(name)
      await findDialog(page).getByRole('button', { name: `Add "${name}"` }).click()
      await expect(addDialog(page)).toBeVisible()

      await page.keyboard.press('Escape')
      await expect(addDialog(page)).toHaveCount(0)
      await expect(findDialog(page)).toBeVisible()
      await expect(findDialog(page).getByRole('heading', { name: 'Find an item', level: 2 })).toBeFocused()
      await expect(search(page)).toHaveValue(name)

      await page.keyboard.press('Escape')
      await expect(findDialog(page)).toHaveCount(0)
      await expect(findButton(page)).toBeFocused()
      expect((await json<ProductRow[]>(page, 'GET', '/api/products')).some((product) => product.name === name)).toBe(false)
    })
  })
}
