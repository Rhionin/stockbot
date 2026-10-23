import { mkdirSync } from 'node:fs'
import { join } from 'node:path'
import { expect, test, type Page, type TestInfo } from '@playwright/test'
import { createKnownProduct, resetToStockIn, scanBarcode, setScannerMode } from './helpers'

const reviewDir = process.env.REVIEW_SHOTS
if (reviewDir !== undefined) mkdirSync(reviewDir, { recursive: true })

const saveReview = async (page: Page, name: string) => {
  if (reviewDir === undefined) return
  await page.setViewportSize({ width: 390, height: 844 })
  await page.emulateMedia({ colorScheme: 'light' })
  await expect.poll(() => page.evaluate(() => document.documentElement.getAttribute('data-mantine-color-scheme'))).toBe('light')
  await page.screenshot({ path: join(reviewDir, name), fullPage: true })
}

const barcode = '2940000002941'
const sentence = 'Nothing on hand to take out.'
const added = 'Added and taken out.'
const historyLabel = 'Added when taking out'

const backfillCommit = {
  removed: 2,
  backfilled: 2,
  itemId: 'item-sour-cream',
  backfill: {
    stockInMoveId: 'move-cream-in',
    stockOutMoveId: 'move-cream-out',
  },
}

const historyMove = {
  id: 'move-cream-in',
  direction: 'in',
  quantity: 2,
  at: '2026-10-18T16:40:00.000Z',
  source: 'backfill',
  productId: 'prod-sour',
  productName: 'Sour cream',
}

// The pair-undo dialog fades in. A shot on the first visible frame is still see-through.
const settleUndoModal = async (page: Page) => {
  const content = page.locator('.mantine-Modal-content')
  const overlay = page.locator('.mantine-Modal-overlay')
  await expect(content).toBeVisible()
  await expect(overlay).toBeVisible()
  await page.clock.runFor(400)
  await page.waitForFunction(() => {
    const node = document.querySelector('.mantine-Modal-content')
    const scrim = document.querySelector('.mantine-Modal-overlay')
    if (!(node instanceof HTMLElement) || !(scrim instanceof HTMLElement)) return false
    const running = [
      ...node.getAnimations({ subtree: true }),
      ...scrim.getAnimations({ subtree: true }),
    ].some((animation) => animation.playState === 'running')
    const background = getComputedStyle(node).backgroundColor
    const channels = background.match(/[\d.]+/g) ?? []
    const backgroundOpaque = channels.length === 3 || (channels.length === 4 && Number(channels[3]) === 1)
    return getComputedStyle(node).opacity === '1'
      && getComputedStyle(scrim).opacity === '1'
      && backgroundOpaque
      && !running
  })
  await expect(content).toHaveCSS('opacity', '1')
  await expect(overlay).toHaveCSS('opacity', '1')
}

const shoot = async (page: Page, testInfo: TestInfo, slug: string) => {
  for (const width of [390, 1280] as const) {
    for (const scheme of ['light', 'dark'] as const) {
      await page.setViewportSize({ width, height: width === 390 ? 844 : 900 })
      await page.emulateMedia({ colorScheme: scheme })
      await expect(page.locator('html')).toHaveAttribute('data-mantine-color-scheme', scheme)
      if (slug.startsWith('undo-confirm')) await settleUndoModal(page)
      const card = page.getByRole('article', { name: `Scan ${barcode}`, exact: true })
      if (await card.count()) await card.scrollIntoViewIfNeeded()
      await page.screenshot({
        path: testInfo.outputPath(`backfill-${slug}-${width}-${scheme}.png`),
      })
    }
  }
}

test('unknown barcode, stock-out, add as a new item, backfill, toast, undo', async ({ page, request }, testInfo) => {
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
        body: JSON.stringify(backfillCommit),
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

  let reshelvesOnUndo = true
  const seenPreview: { path?: string } = {}

  await page.route((url) => url.pathname === '/api/items/item-sour-cream/history', async (route) => {
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({
        kind: 'product',
        id: 'item-sour-cream',
        name: 'Sour cream',
        onHand: 0,
        pace: { known: false, daysLeftKnown: false, sparkline: [] },
        moves: [historyMove, { ...historyMove, id: 'move-cream-out', direction: 'out', source: 'scan' }],
      }),
    })
  })

  await page.route((url) => url.pathname === '/api/moves/move-cream-in', async (route) => {
    if (route.request().method() === 'DELETE') seen.undo = new URL(route.request().url()).pathname
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({
        kind: 'product',
        id: 'item-sour-cream',
        name: 'Sour cream',
        onHand: 0,
        pace: { known: false, daysLeftKnown: false, sparkline: [] },
        moves: [],
      }),
    })
  })

  await page.route((url) => url.pathname === '/api/history', async (route) => {
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({ moves: [historyMove] }),
    })
  })

  // The history rows above are a fixture, so this answers the preview the dialog
  // requests instead of looking up move-cream-in on the server.
  await page.route((url) => url.pathname === '/api/moves/move-cream-in/preview', async (route) => {
    seenPreview.path = new URL(route.request().url()).pathname
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({
        effects: [{
          itemId: 'item-sour-cream',
          productId: 'prod-sour',
          name: 'Sour cream',
          stockInMoveId: 'move-cream-in',
          stockOutMoveId: 'move-cream-out',
          onHand: 0,
          reshelvesOnUndo,
        }],
      }),
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
  await shoot(page, testInfo, 'define')

  await card.getByRole('textbox', { name: 'Product name' }).fill('Sour cream')
  await card.getByRole('button', { name: 'Add as a new item' }).click()
  await expect(page.locator('.restock-toast').filter({ hasText: sentence })).toHaveCount(0)
  await expect(card.getByRole('button', { name: 'Add it and take it out' })).toBeVisible()
  await expect(card.getByText('Stockbot thinks you have none. If you had one anyway, this adds it and takes it out in one step.')).toBeVisible()
  await expect(card.getByRole('button', { name: 'Increase unit count' })).toBeEnabled()
  await expect(card.getByText(sentence)).toBeVisible()
  await expect(card.getByRole('region', { name: 'Which item is this?' })).toHaveCount(0)
  await expect(card.getByText('Sour cream')).toBeVisible()
  expect(seen.product).toMatchObject({ name: 'Sour cream' })
  expect(seen.override).toEqual({ barcode, productId: 'prod-sour' })
  expect(seen.patch).toEqual({ productId: 'prod-sour', status: 'pending' })
  await shoot(page, testInfo, 'offer')

  await page.clock.install()
  await card.getByRole('button', { name: 'Add it and take it out' }).click()
  await page.clock.runFor(400)
  const toast = page.locator('.restock-toast').filter({ hasText: added })
  await expect(toast).toBeVisible()
  await expect(toast.getByRole('button', { name: 'Undo' })).toBeVisible()
  await expect(page.locator('.restock-toast').filter({ hasText: sentence })).toHaveCount(0)
  expect(seen.backfill).toEqual({ backfill: true })
  await shoot(page, testInfo, 'toast')

  await toast.getByRole('button', { name: 'Undo' }).click()
  await expect.poll(() => seen.undo).toBe('/api/moves/move-cream-in')

  await page.clock.runFor(6000)
  await page.getByRole('link', { name: 'History' }).click()
  await expect(page.getByText(historyLabel)).toBeVisible()
  await expect(page.getByText('Sour cream')).toBeVisible()
  await expect(page.locator('.restock-toast')).toHaveCount(0)
  await shoot(page, testInfo, 'history')

  const hideLine = 'Sour cream will be hidden from your inventory again, since none are on hand.'
  const pairBody = 'This removes both the add and the take-out for Sour cream, as if neither happened.'
  await page.getByRole('button', { name: /Actions for Sour cream/ }).click()
  await page.getByRole('menuitem', { name: 'Undo this stock in' }).click()
  const confirm = page.getByRole('dialog', { name: 'Undo "Add it and take it out"?' })
  await expect.poll(() => seenPreview.path).toBe('/api/moves/move-cream-in/preview')
  await expect(confirm.getByText(pairBody)).toBeVisible()
  await expect(confirm.getByText(hideLine)).toBeVisible()
  await expect(confirm.getByRole('button', { name: 'Undo', exact: true })).toBeVisible()
  await expect(confirm.getByRole('button', { name: 'Cancel', exact: true })).toBeVisible()
  await settleUndoModal(page)
  await saveReview(page, 'undo-390-light.png')
  await shoot(page, testInfo, 'undo-confirm')

  await confirm.getByRole('button', { name: 'Cancel', exact: true }).click()
  await expect(confirm).toBeHidden()
  reshelvesOnUndo = false
  seenPreview.path = undefined
  await page.getByRole('link', { name: 'Inventory' }).click()
  await page.getByRole('link', { name: 'History' }).click()
  await expect(page.getByText(historyLabel)).toBeVisible()
  const previewSettled = page.waitForResponse((response) => new URL(response.url()).pathname === '/api/moves/move-cream-in/preview')
  await page.getByRole('button', { name: /Actions for Sour cream/ }).click()
  await page.getByRole('menuitem', { name: 'Undo this stock in' }).click()
  const plain = page.getByRole('dialog', { name: 'Undo "Add it and take it out"?' })
  await previewSettled
  await expect.poll(() => seenPreview.path).toBe('/api/moves/move-cream-in/preview')
  await expect(plain.getByText(pairBody)).toBeVisible()
  await expect(plain.getByText(hideLine)).toBeHidden()
  await expect(plain.getByRole('button', { name: 'Undo', exact: true })).toBeEnabled()
  await shoot(page, testInfo, 'undo-confirm-plain')

  await page.unrouteAll({ behavior: 'ignoreErrors' })
  const pending = await request.get('/api/scans?userId=user-1&status=pending')
  const rows = await pending.json() as { id: string; barcode: string }[]
  const leftover = rows.find((row) => row.barcode === barcode)
  if (leftover !== undefined) {
    await request.patch(`/api/scans/${leftover.id}`, { data: { status: 'cancelled' } })
  }
})

test('an offline backfill shows the connection sentence', async ({ page, request }) => {
  test.setTimeout(90_000)
  const emptyBarcode = '2940000003891'
  const offlineLine = "Can't reach Stockbot. Check your connection and try again."
  await createKnownProduct(request, {
    barcode: emptyBarcode,
    name: 'Offline cream',
    category: 'Dairy',
    unitOfMeasure: 'tub',
  })
  const pending = await request.get('/api/scans?userId=user-1&status=pending')
  expect(pending.ok()).toBe(true)
  const rows = await pending.json() as { id: string }[]
  for (const row of rows) {
    const cancelled = await request.patch(`/api/scans/${row.id}`, { data: { status: 'cancelled' } })
    expect(cancelled.ok()).toBe(true)
  }
  const stock = async (direction: 'stock_in' | 'stock_out') => {
    const created = await request.post('/api/scans', {
      data: { barcode: emptyBarcode, direction, userId: 'user-1' },
    })
    expect(created.ok(), await created.text()).toBe(true)
    const body = await created.json() as { id: string }
    const committed = await request.post(`/api/scans/${body.id}/commit`, { data: {} })
    expect(committed.ok(), await committed.text()).toBe(true)
  }
  await stock('stock_in')
  await stock('stock_out')

  await page.route((url) => url.pathname.endsWith('/commit'), async (route) => {
    if (route.request().method() !== 'POST') {
      await route.continue()
      return
    }
    const body = route.request().postDataJSON() as { backfill?: boolean } | null
    if (body?.backfill === true) {
      await route.abort('failed')
      return
    }
    await route.continue()
  })

  await page.setViewportSize({ width: 390, height: 844 })
  await page.emulateMedia({ colorScheme: 'light' })
  await page.goto('/')
  await expect.poll(() => page.evaluate(() => document.documentElement.getAttribute('data-mantine-color-scheme'))).toBe('light')
  await resetToStockIn(page)
  await setScannerMode(page, 'stock_out')
  const card = await scanBarcode(page, emptyBarcode)
  await expect(card.getByRole('button', { name: 'Add it and take it out' })).toBeVisible()
  await card.getByRole('button', { name: 'Add it and take it out' }).click()
  await expect(page.getByText(offlineLine)).toBeVisible()
  await expect(page.getByText('Failed to fetch')).toHaveCount(0)
  await expect(page.getByText('Added and taken out.')).toHaveCount(0)
  await saveReview(page, 'offline-390-light.png')
})
