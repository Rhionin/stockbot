import type { Page } from '@playwright/test'

export type ShoppingStoresFixtureOptions = {
  canConnect?: boolean
  storeName?: string
  linkedProvider?: string | null
}

/** Matches GET /api/stores after DEP-7 (`canConnect` always present on the real API). */
export async function routeShoppingStores(
  page: Page,
  options: ShoppingStoresFixtureOptions = {},
) {
  const canConnect = options.canConnect ?? true
  const storeName = options.storeName ?? 'Kroger'
  const linkedProvider = options.linkedProvider === undefined ? 'kroger' : options.linkedProvider
  const stores = linkedProvider
    ? [{ id: 'st-e2e-1', name: storeName, linkedProvider }]
    : []

  await page.route('**/api/stores', async (route) => {
    if (route.request().method() !== 'GET') {
      await route.continue()
      return
    }
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({
        stores,
        defaultStoreId: stores[0]?.id ?? null,
        lookupsEnabled: false,
        canConnect,
      }),
    })
  })
}