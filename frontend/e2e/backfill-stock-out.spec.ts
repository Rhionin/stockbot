import { expect, test, type Page } from '@playwright/test'
import { resetToStockIn, scanBarcode, setScannerMode } from './helpers'

const barcode = '2940000002941'
const sentence = 'Nothing on hand to take out.'
const added = 'Added and taken out.'
const historyLabel = 'Added when taking out'

const historyMove = {
  id: 'move-backfill',
  direction: 'in',
  quantity: 1,
  at: '2026-10-18T16:40:00.000Z',
  source: 'backfill',
  productId: 'prod-sour',
  productName: 'Sour cream',
}

const shoot = async (page: Page, slug: string) => {
  for (const width of [390, 1280] as const) {
    for (const scheme of ['light', 'dark'] as const) {
      await page.setViewportSize({ width, height: width === 390 ? 844 : 900 })
      await page.emulateMedia({ colorScheme: scheme })
      await expect(page.locator('html')).toHaveAttribute('data-mantine-color-scheme', scheme)
      const card = page.getByRole('article', { name: `Scan ${barcode}`, exact: true })
      if (await card.count()) await card.scrollIntoViewIfNeeded()
      await page.screenshot({
        path: `/opt/cursor/artifacts/backfill-${slug}-${width}-${scheme}.png`,
      })
    }
  }
}

test('unknown barcode, stock-out, add as a new item, backfill, toast, undo', async ({ page, request }) => {
  test.setTimeout(180_000)
  await page.setViewportSize({ width: 390, height: 844 })
  const seen: { product?: { name?: string }; override?: { barcode?: string; productId?: string }; patch?: { productId?: string; status?: string }; backfill?: unknown; undo?: string } = {}

  // The current server flags a barcode lookup that has no name. The backfill
  // contract shows that scan on the pending "Which item is this?" card instead.
  await page.route((url) => url.pathname === '/api/scans' && url.searchParams.get('status') === 'flagged', async (route) => {
    const response = await route.fetch()
    const rows = await response.json() as { barcode: string }[]
    await route.fulfill({
      status: response.status(),
      contentType: 'application/json',
      body: JSON.stringify(rows.map((row) => row.barcode === barcode
        ? { ...row, status: 'pending', productId: null, product: null }
        : row)),
    })
  })

  await page.route((url) => url.pathname.endsWith('/commit'), async (route) => {
    if (route.request().method() !== 'POST') {
      await route.continue()
      return
    }
    const body = route.request().postDataJSON() as { backfill?: boolean; linkProductId?: string } | null
    if (body?.backfill === true) {
      seen.backfill = body
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          removed: 1,
          backfilled: 1,
          itemId: 'item-sour',
          backfill: { stockInMoveId: 'move-backfill', stockOutMoveId: 'move-out' },
        }),
      })
      return
    }
    await route.fulfill({
      status: 409,
      contentType: 'application/json',
      body: JSON.stringify({ error: 'barcode is not linked', code: 'no_item', suggestions: [] }),
    })
  })

  await page.route((url) => url.pathname === '/api/products', async (route) => {
    if (route.request().method() !== 'POST') {
      await route.continue()
      return
    }
    const body = route.request().postDataJSON() as { name?: string }
    seen.product = body
    await route.fulfill({
      status: 201,
      contentType: 'application/json',
      body: JSON.stringify({
        id: 'prod-sour',
        name: body.name,
        category: '',
        unitOfMeasure: '',
        createdAt: '2026-10-18T16:40:00.000Z',
      }),
    })
  })

  await page.route((url) => url.pathname === '/api/products/overrides', async (route) => {
    const body = route.request().postDataJSON() as { barcode?: string; productId?: string }
    seen.override = body
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({ ...body, source: 'user' }),
    })
  })

  await page.route((url) => /^\/api\/scans\/[^/]+$/.test(url.pathname), async (route) => {
    if (route.request().method() !== 'PATCH') {
      await route.continue()
      return
    }
    const body = route.request().postDataJSON() as { productId?: string; status?: string }
    if (body.productId === undefined) {
      await route.continue()
      return
    }
    seen.patch = body
    const scanId = new URL(route.request().url()).pathname.split('/').at(-1)
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({
        id: scanId,
        userId: 'user-1',
        barcode,
        scannedAt: '2026-10-18T16:40:00.000Z',
        direction: 'stock_out',
        unitCount: 1,
        expiresAt: null,
        status: 'pending',
        productId: 'prod-sour',
        onHand: 0,
        product: { id: 'prod-sour', name: 'Sour cream', category: '', unitOfMeasure: '' },
        committedAt: null,
        createdAt: '2026-10-18T16:40:00.000Z',
      }),
    })
  })

  await page.route((url) => url.pathname === '/api/items/item-sour/history', async (route) => {
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({
        kind: 'product',
        id: 'item-sour',
        name: 'Sour cream',
        onHand: 0,
        pace: { known: false, daysLeftKnown: false, sparkline: [] },
        moves: [historyMove, { ...historyMove, id: 'move-out', direction: 'out', source: 'scan' }],
      }),
    })
  })

  await page.route((url) => url.pathname === '/api/moves/move-backfill', async (route) => {
    if (route.request().method() === 'DELETE') seen.undo = new URL(route.request().url()).pathname
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({
        kind: 'product',
        id: 'item-sour',
        name: 'Sour cream',
        onHand: 0,
        pace: { known: false, daysLeftKnown: false, sparkline: [] },
        moves: [],
      }),
    })
  })

  let listOnHand = false
  await page.route((url) => url.pathname === '/api/inventory', async (route) => {
    if (!listOnHand || route.request().method() !== 'GET') {
      await route.continue()
      return
    }
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify([{
        item: {
          id: 'item-sour',
          userId: 'user-1',
          productId: 'prod-sour',
          product: { id: 'prod-sour', name: 'Sour cream', category: 'Dairy', unitOfMeasure: 'tub' },
          targetQuantity: null,
          createdAt: '2026-10-18T16:40:00.000Z',
        },
        instanceCount: 0,
        nearExpiryCount: 0,
        expiredCount: 0,
        needsAttention: false,
      }]),
    })
  })

  await page.route((url) => url.pathname === '/api/history', async (route) => {
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({ moves: [historyMove] }),
    })
  })

  await page.goto('/')
  await resetToStockIn(page)
  await setScannerMode(page, 'stock_out')
  const card = await scanBarcode(page, barcode)
  await card.getByRole('button', { name: 'Confirm', exact: true }).click()

  await expect(card.getByRole('region', { name: 'Which item is this?' })).toBeVisible()
  await expect(card.getByRole('button', { name: 'Add as a new item' })).toBeDisabled()
  await expect(card.getByRole('button', { name: 'Add it and take it out' })).toHaveCount(0)
  await expect(card.getByRole('textbox', { name: 'Product name' })).toHaveValue('')
  await shoot(page, 'define')

  await card.getByRole('textbox', { name: 'Product name' }).fill('Sour cream')
  await card.getByRole('button', { name: 'Add as a new item' }).click()
  await expect(page.locator('.restock-toast').filter({ hasText: sentence })).toBeVisible()
  await expect(card.getByRole('button', { name: 'Add it and take it out' })).toBeVisible()
  await expect(card.getByText(sentence)).toBeVisible()
  await expect(card.getByRole('region', { name: 'Which item is this?' })).toHaveCount(0)
  await expect(card.getByText('Sour cream')).toBeVisible()
  expect(seen.product).toMatchObject({ name: 'Sour cream' })
  expect(seen.override).toEqual({ barcode, productId: 'prod-sour' })
  expect(seen.patch).toEqual({ productId: 'prod-sour', status: 'pending' })
  await shoot(page, 'offer')

  await page.clock.install()
  await card.getByRole('button', { name: 'Add it and take it out' }).click()
  await page.clock.runFor(400)
  const toast = page.locator('.restock-toast').filter({ hasText: added })
  await expect(toast).toBeVisible()
  await expect(toast.getByRole('button', { name: 'Undo' })).toBeVisible()
  expect(seen.backfill).toEqual({ backfill: true })
  await shoot(page, 'toast')

  await toast.getByRole('button', { name: 'Undo' }).click()
  await expect.poll(() => seen.undo).toBe('/api/moves/move-backfill')

  await page.clock.runFor(6000)
  listOnHand = true
  await page.getByRole('link', { name: 'History' }).click()
  await expect(page.getByText(historyLabel)).toBeVisible()
  await expect(page.getByText('Sour cream')).toBeVisible()
  await shoot(page, 'history')

  await page.getByRole('button', { name: /Actions for Sour cream/ }).click()
  await page.getByRole('menuitem', { name: 'Undo this stock in' }).click()
  const confirm = page.getByRole('dialog', { name: 'Undo "Add it and take it out"?' })
  await expect(confirm.getByText('This removes both the add and the take-out for Sour cream, as if neither happened.')).toBeVisible()
  await expect(confirm.getByText('Sour cream will be hidden from your inventory again, since none are on hand.')).toBeVisible()
  await expect(confirm.getByRole('button', { name: 'Undo', exact: true })).toBeVisible()
  await expect(confirm.getByRole('button', { name: 'Cancel', exact: true })).toBeVisible()
  await shoot(page, 'undo-confirm')

  await page.unrouteAll({ behavior: 'ignoreErrors' })
  const pending = await request.get('/api/scans?userId=user-1&status=pending')
  const rows = await pending.json() as { id: string; barcode: string }[]
  const leftover = rows.find((row) => row.barcode === barcode)
  if (leftover !== undefined) {
    await request.patch(`/api/scans/${leftover.id}`, { data: { status: 'cancelled' } })
  }
})
