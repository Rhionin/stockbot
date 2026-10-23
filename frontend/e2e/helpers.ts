import { expect, type APIRequestContext, type Locator, type Page } from '@playwright/test'

interface Product {
  id: string
  name: string
}

interface ProductInput {
  barcode: string
  name: string
  category: string
  unitOfMeasure: string
}

export const createKnownProduct = async (
  request: APIRequestContext,
  input: ProductInput,
): Promise<void> => {
  const createResponse = await request.post('/api/products', {
    data: {
      name: input.name,
      category: input.category,
      unitOfMeasure: input.unitOfMeasure,
    },
  })
  expect(createResponse.ok()).toBe(true)

  const productsResponse = await request.get('/api/products')
  expect(productsResponse.ok()).toBe(true)
  const products = await productsResponse.json() as Product[]
  const product = products.find((candidate) => candidate.name === input.name)
  if (product === undefined) {
    throw new Error(`Created product was not returned: ${input.name}`)
  }

  const overrideResponse = await request.post('/api/products/overrides', {
    data: { barcode: input.barcode, productId: product.id },
  })
  expect(overrideResponse.ok()).toBe(true)
}

/**
 * Type a barcode into the HID scanner input and press Enter, the way a physical
 * scanner does. The scan is stamped with the queue's current scanner mode, so
 * the caller must switch the mode first when a stock_out scan is wanted.
 * Returns the scan card locator for that barcode.
 */
export const scanBarcode = async (page: Page, barcode: string): Promise<Locator> => {
  const scannerInput = page.getByRole('textbox', { name: 'Barcode scanner input' })
  await scannerInput.fill(barcode)
  await scannerInput.press('Enter')

  // The in-flight placeholder is "Scan <barcode> processing". A nothing-on-hand
  // card appends its title after a comma. Neither should match the other.
  const scanCard = page.getByRole('article', { name: new RegExp(`^Scan ${barcode}(,|$)`) })
  await expect(scanCard).toBeVisible()
  return scanCard
}

/**
 * Reset the scanner to stock_in mode so a spec starts from a known direction.
 * Scanner mode is server-side global state that persists for the whole
 * `npx playwright test` run (one webServer, one DB), so a prior spec that
 * switched to stock_out would otherwise leak into the next. This scans the
 * STOCK_IN control barcode only when the banner is not already stock_in.
 */
export const resetToStockIn = async (page: Page): Promise<void> => {
  // Wait for the mode banner to settle (it is seeded async from
  // GET /api/scanner/config) before deciding whether a switch is needed.
  await expect(page.getByRole('alert').filter({ hasText: /^Mode: stock_(in|out)/ })).toBeVisible()
  if (await page.getByText('Mode: stock_in').isVisible()) return
  await setScannerMode(page, 'stock_in')
}

/**
 * Switch the scanner mode the way a user does: scan a reserved control barcode.
 * A control barcode does NOT create a scan card - ScanQueuePage.captureBarcode
 * classifies it, calls setScannerMode, and returns early - so we wait for the
 * on-screen `Mode: <mode>` banner instead of a card. The default control
 * strings are 'STOCK_IN' / 'STOCK_OUT' (GET /api/scanner/config).
 */
export const setScannerMode = async (
  page: Page,
  mode: 'stock_in' | 'stock_out',
): Promise<void> => {
  const controlBarcode = mode === 'stock_out' ? 'STOCK_OUT' : 'STOCK_IN'
  const scannerInput = page.getByRole('textbox', { name: 'Barcode scanner input' })
  await scannerInput.fill(controlBarcode)
  await scannerInput.press('Enter')
  await expect(page.getByText(`Mode: ${mode}`)).toBeVisible()
}

/**
 * Confirm a single pending scan through its per-card Confirm button, the real
 * current flow (see .agents/skills/verify-stockbot/scripts/drive-stock-in.mjs).
 * Direction is not set here: it is stamped from the scanner mode at scan time,
 * so the caller scans in the correct mode (use setScannerMode for stock_out).
 *
 * When expirationDate is provided, the stock-in card's collapsed expiration
 * control is opened (`Add expiration`), the date is typed into `Expiration
 * date`, and it is committed on blur. The field patches on blur via
 * `PATCH /api/scans/:id`, and approving commits the entry to inventory through
 * a separate request, so we must WAIT for that PATCH to persist before
 * approving. Otherwise a slow runner can commit the instance before its date
 * lands, leaving an instance with no expiry (the stock-out-oldest flake).
 * After approval the card detaches.
 */
export const commitSelectedScan = async (
  page: Page,
  scanCard: Locator,
  expirationDate?: string,
): Promise<void> => {
  if (expirationDate !== undefined) {
    const addExpiration = scanCard.getByRole('button', { name: 'Add expiration' })
    const expiry = scanCard.getByLabel('Expiration date')
    // The row can paint after the scan response. Wait for the control instead
    // of sampling isVisible once and then filling a field that was never opened.
    await expect(addExpiration.or(expiry)).toBeVisible()
    if (await addExpiration.isVisible()) {
      await addExpiration.click()
    }
    await expiry.fill(expirationDate)
    // The date field persists on blur via PATCH /api/scans/:id, and approving
    // commits the entry to inventory through a separate request. We MUST wait
    // for that PATCH to land before approving: otherwise a slow runner can
    // process the commit before the date is persisted, committing an instance
    // with no expiry (the stock-out-oldest flake that only surfaced under CI
    // latency). Arm the response listener before blurring so we never miss it.
    //
    // Pressing Tab does NOT blur this composite date input (the browser moves
    // focus between its day/month/year segments), so the onBlur PATCH only ever
    // fired when Confirm was clicked - racing the commit. Blur explicitly to
    // fire the PATCH deterministically, then await it.
    const patchResponse = page.waitForResponse(
      (response) =>
        /\/api\/scans\/[^/]+$/.test(new URL(response.url()).pathname) &&
        response.request().method() === 'PATCH' &&
        response.ok(),
    )
    await expiry.blur()
    await patchResponse
    // The stock-in confirmation folds the field into "Expires …" once the
    // PATCH lands. Wait for that summary so approval cannot race the save.
    await expect(scanCard.getByText(/^Expires /)).toBeVisible()
  }
  await scanCard.getByRole('button', { name: 'Confirm', exact: true }).click()
  await expect(scanCard).toHaveCount(0)
}

export const inventoryRow = (page: Page, productName: string): Locator =>
  page.getByRole('article').filter({ has: page.getByRole('heading', { name: productName }) })

// Specs share one SQLite database and one scanner mode for the whole
// Playwright run. Clear shopping-list rows, on-hand stock, and the opening
// snapshot, and put the scanner back to stock_in, so a spec neither inherits
// nor leaves that state.
export const resetSharedState = async (request: APIRequestContext): Promise<void> => {
  const wiped = await request.post('/api/inventory/wipe', {
    data: { confirmation: 'WIPE INVENTORY' },
  })
  expect(wiped.ok()).toBe(true)
  const mode = await request.post('/api/scanner/mode', {
    data: { mode: 'stock_in' },
  })
  expect(mode.ok()).toBe(true)
}
