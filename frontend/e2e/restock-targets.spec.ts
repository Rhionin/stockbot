import AxeBuilder from '@axe-core/playwright'
import { expect, test, type Locator, type Page } from '@playwright/test'
import { commitSelectedScan, createKnownProduct, inventoryRow, resetToStockIn, scanBarcode, setScannerMode } from './helpers'

const barcode = '210000231231'
const productName = 'Restock Oats'

function violationText(violations: { id: string; help: string; nodes: { html: string; target: string[] }[] }[]) {
  return violations.map((violation) => (
    `${violation.id}: ${violation.help} (${violation.nodes.map((node) => node.target.join(' ')).join('; ')})`
  )).join('\n')
}

async function expectContrast(page: Page, selector: string) {
  const results = await new AxeBuilder({ page }).include(selector).withRules(['color-contrast']).analyze()
  expect(results.violations, violationText(results.violations)).toEqual([])
}

async function setScheme(page: Page, scheme: 'light' | 'dark') {
  await page.evaluate((value) => {
    window.localStorage.setItem('mantine-color-scheme-value', value)
    document.documentElement.setAttribute('data-mantine-color-scheme', value)
  }, scheme)
  await page.reload()
  await expect.poll(() => page.evaluate(() => document.documentElement.getAttribute('data-mantine-color-scheme'))).toBe(scheme)
}

async function applySchemes(page: Page, os: 'light' | 'dark', app: 'light' | 'dark') {
  await page.emulateMedia({ colorScheme: os })
  await setScheme(page, app)
}

async function expectInside(target: Locator, containerSelector: string) {
  const box = await target.evaluate((el, selector) => {
    const parent = el.closest(selector)
    if (!parent) return { ok: false, reason: `missing ${selector}` }
    const elBox = el.getBoundingClientRect()
    const parentBox = parent.getBoundingClientRect()
    const leftGap = elBox.left - parentBox.left
    const rightGap = parentBox.right - elBox.right
    return {
      ok: leftGap >= -1
        && rightGap >= -1
        && el.scrollWidth <= el.clientWidth + 1
        && el.scrollHeight <= el.clientHeight + 1
        && parent.scrollWidth <= parent.clientWidth + 1
        && parentBox.right <= window.innerWidth + 1,
      leftGap,
      rightGap,
      elScroll: [el.scrollWidth, el.clientWidth, el.scrollHeight, el.clientHeight],
      parentScroll: [parent.scrollWidth, parent.clientWidth],
      parentRight: parentBox.right,
      viewport: window.innerWidth,
    }
  }, containerSelector)
  expect(box.ok, JSON.stringify(box)).toBe(true)
}

test('settings has one household default and readable restock screens', async ({ page, request }) => {
  await page.goto('/settings')
  await expect(page.getByRole('heading', { name: 'Settings' })).toBeVisible()
  await expect(page.getByRole('paragraph').filter({ hasText: /^Household default$/ })).toBeVisible()
  await expect(page.getByText(/How far ahead the shopping list looks/)).toHaveCount(0)
  await expect(page.getByRole('button', { name: 'Change' })).toHaveCount(1)
  await expectContrast(page, '.restock-settings')

  await page.getByRole('button', { name: 'Change' }).click()
  await expect(page.getByRole('dialog', { name: 'Household default' })).toBeVisible()
  await expect(page.getByText('Months of supply')).toBeVisible()
  await expect(page.getByText('Fixed amount')).toBeVisible()
  await expect(page.getByText('0 means don\'t restock.')).toBeVisible()
  await page.keyboard.press('Escape')

  const restock = await request.get('/api/restock')
  expect(restock.ok()).toBe(true)
  const body = await restock.json() as { categories: { id: string; name: string }[] }
  const produce = body.categories.find((category) => category.name === 'Produce')
  if (!produce) throw new Error('Produce category missing')
  await page.goto(`/inventory/categories/${produce.id}`)
  await expect(page.getByRole('heading', { name: 'Produce' })).toBeVisible()
  await expectContrast(page, '.restock-target')
  await setScheme(page, 'dark')
  await expect(page.getByRole('heading', { name: 'Produce' })).toBeVisible()
  await expectContrast(page, '.restock-target')
})

test('don\'t restock removes the shopping row and keeps it off after reload', async ({ page, request }) => {
  test.setTimeout(90_000)
  await createKnownProduct(request, {
    barcode,
    name: productName,
    category: 'Dry goods',
    unitOfMeasure: 'bag',
  })
  await page.goto('/')
  await resetToStockIn(page)
  await commitSelectedScan(page, await scanBarcode(page, barcode), '2032-04-10')
  await commitSelectedScan(page, await scanBarcode(page, barcode), '2032-08-20')

  await page.getByRole('link', { name: 'Inventory' }).click()
  const row = inventoryRow(page, productName)
  await expect(row.getByText('2 bag', { exact: true })).toBeVisible()
  const finishOpening = page.getByRole('button', { name: 'This scan is complete' })
  if (await finishOpening.isVisible()) {
    await finishOpening.click()
    await expect(page.getByRole('alert', { name: 'Opening inventory' })).toBeHidden()
  }

  await row.getByRole('button', { name: `Actions for ${productName}` }).click()
  const hint = page.locator('.bin-menu-hint', { hasText: 'Sets this item\'s target to 0' })
  await expect(hint).toBeVisible()
  await expect(page.getByRole('menuitem', { name: /Don't restock/ })).toBeVisible()
  await page.keyboard.press('Escape')

  await page.getByRole('link', { name: 'Scan Queue' }).click()
  await setScannerMode(page, 'stock_out')
  await commitSelectedScan(page, await scanBarcode(page, barcode))

  await page.getByRole('link', { name: 'Shopping List' }).click()
  await page.getByRole('button', { name: /the list$/ }).click()
  const derivedRow = page
    .getByRole('row')
    .filter({ hasText: productName })
    .filter({ has: page.getByText('Derived', { exact: true }) })
  await expect(derivedRow).toBeVisible()

  await derivedRow.getByRole('button', { name: `Actions for ${productName}` }).click()
  await page.getByRole('menuitem', { name: `Don't restock ${productName}` }).click()
  await expect(derivedRow).toHaveCount(0)

  await page.reload()
  await expect(page.getByRole('row').filter({ hasText: productName })).toHaveCount(0)
})

async function expectToken(target: Locator, token: string) {
  const colors = await target.evaluate((el, name) => {
    const probe = document.createElement('span')
    probe.style.color = `var(${name})`
    document.body.appendChild(probe)
    const tokenColor = getComputedStyle(probe).color
    probe.remove()
    return { color: getComputedStyle(el).color, tokenColor }
  }, token)
  expect(colors.color, token).toBe(colors.tokenColor)
}

test('restock headings follow the app theme when the OS scheme differs', async ({ page, request }) => {
  test.setTimeout(180_000)
  const themeBarcode = '210000240111'
  const themeName = 'Theme oats'
  await createKnownProduct(request, {
    barcode: themeBarcode,
    name: themeName,
    category: 'Dry goods',
    unitOfMeasure: 'bag',
  })
  await page.goto('/')
  await resetToStockIn(page)
  await commitSelectedScan(page, await scanBarcode(page, themeBarcode))

  const inventory = await request.get('/api/inventory')
  expect(inventory.ok()).toBe(true)
  const items = await inventory.json() as { item: { id: string; product: { name: string } } }[]
  const item = items.find((row) => row.item.product.name === themeName)
  if (!item) throw new Error('theme oats missing from inventory')
  const restock = await request.get('/api/restock')
  expect(restock.ok()).toBe(true)
  const snapshot = await restock.json() as { categories: { id: string; name: string }[] }
  const produce = snapshot.categories.find((category) => category.name === 'Produce')
  if (!produce) throw new Error('Produce category missing')
  const added = await request.post(`/api/restock/items/${item.item.id}/categories`, {
    data: { categoryId: produce.id },
  })
  expect(added.ok()).toBe(true)

  await page.route('**/api/providers', async (route) => {
    if (route.request().method() !== 'GET') {
      await route.continue()
      return
    }
    const response = await route.fetch()
    const rows = await response.json() as { credentialsConfigured?: boolean; connectionState?: string }[]
    if (Array.isArray(rows)) {
      for (const row of rows) {
        row.credentialsConfigured = true
        row.connectionState = 'connected'
      }
    }
    await route.fulfill({ response, json: rows })
  })

  for (const [os, app] of [['light', 'light'], ['dark', 'dark'], ['light', 'dark'], ['dark', 'light']] as const) {
    await page.goto('/settings')
    await applySchemes(page, os, app)
    const restockHeading = page.getByRole('heading', { name: 'Restock targets' })
    const categoriesHeading = page.getByRole('heading', { name: 'Categories' })
    await expect(restockHeading).toBeVisible()
    await expect(categoriesHeading).toBeVisible()
    await expectContrast(page, '.restock-settings h2')
    await expectToken(restockHeading, '--mantine-color-text')
    await expectToken(categoriesHeading, '--mantine-color-text')
    const link = page.locator('.restock-row .name a').first()
    await expect(link).toBeVisible()
    await expectContrast(page, '.restock-row .name a')
    const colors = await link.evaluate((el) => {
      const scheme = document.documentElement.getAttribute('data-mantine-color-scheme')
      const token = scheme === 'dark' ? '--mantine-color-teal-4' : '--mantine-color-teal-8'
      const probe = document.createElement('span')
      probe.style.color = `var(${token})`
      document.body.appendChild(probe)
      const tokenColor = getComputedStyle(probe).color
      probe.remove()
      return { color: getComputedStyle(el).color, tokenColor }
    })
    expect(colors.color).toBe(colors.tokenColor)

    await page.goto(`/inventory/categories/${produce.id}`)
    await page.getByRole('button', { name: 'Actions for Produce' }).click()
    await page.getByRole('menuitem', { name: /Don't restock/ }).click()
    const dialog = page.getByRole('dialog', { name: 'Stop restocking Produce?' })
    await expect(dialog).toBeVisible()
    await page.waitForFunction(() => {
      const content = document.querySelector('.mantine-Modal-content')
      if (!content) return false
      const style = getComputedStyle(content)
      const opacity = Number.parseFloat(style.opacity)
      const settled = style.transform === 'none' || style.transform === 'matrix(1, 0, 0, 1, 0, 0)'
      return opacity >= 0.99 && settled
    })
    const dialogTitle = dialog.getByRole('heading', { name: 'Stop restocking Produce?' })
    await expectContrast(page, '[role="dialog"] h2')
    await expectToken(dialogTitle, '--mantine-color-text')
    const stoppedAt = dialog.locator('.restock-stop-after', { hasText: '0' })
    await expect(stoppedAt).toBeVisible()
    await expectContrast(page, '.restock-stop-after')
    await expectToken(stoppedAt, '--mantine-color-text')
    await page.keyboard.press('Escape')

    await page.goto('/shopping')
    const cartNote = page.getByText('This forgets what was already sent and clears checked-off lines.')
    await expect(cartNote).toBeVisible()
    await expectContrast(page, '.cart-reset-note')
    await expectToken(cartNote, '--mantine-color-dimmed')
    await page.getByRole('button', { name: /the list$/ }).click()
    await expect(page.getByRole('heading', { name: 'Add an item' })).toBeVisible()
    await expectContrast(page, 'form h2')
  }
})

test('phone width keeps the default pill and the not-restocked filter inside', async ({ page, request }) => {
  const created = await request.post('/api/products', {
    data: { name: 'Narrow rice', category: 'Pantry', unitOfMeasure: 'bag' },
  })
  expect(created.ok()).toBe(true)
  const products = await request.get('/api/products')
  expect(products.ok()).toBe(true)
  const list = await products.json() as { id: string; name: string }[]
  const rice = list.find((product) => product.name === 'Narrow rice')
  if (!rice) throw new Error('narrow rice missing')
  const group = await request.post('/api/groups', {
    data: { name: 'Narrow bags', productIds: [rice.id] },
  })
  expect(group.ok()).toBe(true)
  const body = await group.json() as { id: string }

  await page.setViewportSize({ width: 390, height: 844 })
  await page.goto(`/groups/${body.id}`)
  const pill = page.locator('.seesaw-pill', { has: page.locator('.seesaw-default') })
  await expect(pill).toBeVisible()
  await expect(pill).toContainText('3 mo')
  await expectInside(pill, '.seesaw')

  await page.goto('/inventory')
  const chip = page.getByRole('button', { name: /^Not restocked/ })
  await expect(chip).toBeVisible()
  await expectInside(chip, '.shelf-filters')
})
