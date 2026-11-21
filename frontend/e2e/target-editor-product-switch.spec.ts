import { expect, test, type Page } from '@playwright/test'

type SynthTarget = { kind: 'months'; value: number }

const household = { kind: 'months' as const, value: 3 }

const restockItems = (targets: Record<string, SynthTarget>) => ([
  {
    itemId: 'item-a',
    productId: 'product-a',
    name: 'Synth item 27',
    unit: 'unit',
    target: targets['item-a'],
    level: 'item',
    sourceLabel: 'Set for this item',
    valueLabel: `${targets['item-a'].value} months`,
    rowSource: `${targets['item-a'].value} months`,
    chain: [],
    categories: [],
    setLabel: 'Change',
  },
  {
    itemId: 'item-b',
    productId: 'product-b',
    name: 'Synth item 28',
    unit: 'unit',
    target: targets['item-b'],
    level: 'item',
    sourceLabel: 'Set for this item',
    valueLabel: `${targets['item-b'].value} months`,
    rowSource: `${targets['item-b'].value} months`,
    chain: [],
    categories: [],
    setLabel: 'Change',
  },
])

async function installSyntheticInventory(page: Page) {
  const targets: Record<string, SynthTarget> = {
    'item-a': { kind: 'months', value: 7 },
    'item-b': { kind: 'months', value: 2 },
  }
  await page.route('**/api/inventory', async (route) => {
    if (route.request().method() !== 'GET') {
      await route.continue()
      return
    }
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify([
        {
          item: {
            id: 'item-a',
            userId: 'user-1',
            productId: 'product-a',
            product: { id: 'product-a', name: 'Synth item 27', category: 'Pantry', unitOfMeasure: 'unit' },
            targetQuantity: null,
            createdAt: '2026-01-01T00:00:00Z',
          },
          instanceCount: 1,
          nearExpiryCount: 0,
          expiredCount: 0,
          needsAttention: false,
        },
        {
          item: {
            id: 'item-b',
            userId: 'user-1',
            productId: 'product-b',
            product: { id: 'product-b', name: 'Synth item 28', category: 'Pantry', unitOfMeasure: 'unit' },
            targetQuantity: null,
            createdAt: '2026-01-01T00:00:00Z',
          },
          instanceCount: 1,
          nearExpiryCount: 0,
          expiredCount: 0,
          needsAttention: false,
        },
      ]),
    })
  })
  await page.route('**/api/restock/items/**', async (route) => {
    if (route.request().method() === 'PUT') {
      const itemId = route.request().url().split('/').pop() ?? ''
      targets[itemId] = route.request().postDataJSON() as SynthTarget
      await route.fulfill({ status: 200, contentType: 'application/json', body: '{}' })
      return
    }
    await route.continue()
  })
  await page.route('**/api/restock', async (route) => {
    if (route.request().method() !== 'GET') {
      await route.continue()
      return
    }
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({
        household,
        householdLabel: '3 months',
        appliesTo: 2,
        itemCount: 2,
        items: restockItems(targets),
        categories: [],
        notRestocked: 0,
        suggestionCount: 0,
      }),
    })
  })
  await page.route('**/api/settings/supply', (route) => route.fulfill({
    status: 200,
    contentType: 'application/json',
    body: JSON.stringify({ months: 3, opening: false, wipePhrase: 'WIPE INVENTORY' }),
  }))
  await page.route('**/api/groups', (route) => route.fulfill({ status: 200, contentType: 'application/json', body: '[]' }))
  await page.route('**/api/products', (route) => route.fulfill({ status: 200, contentType: 'application/json', body: '[]' }))
  await page.route('**/api/group-suggestions', (route) => route.fulfill({ status: 200, contentType: 'application/json', body: '[]' }))
}

async function monthsInDialog(page: Page, title: string) {
  const dialog = page.getByRole('dialog', { name: title })
  await expect(dialog).toBeVisible()
  return dialog.getByRole('textbox', { name: 'Months' })
}

test('restock editor shows each product\'s saved target after switching items', async ({ page }) => {
  test.setTimeout(60_000)
  await installSyntheticInventory(page)
  await page.goto('/inventory')
  await expect(page.getByRole('heading', { name: 'Inventory' })).toBeVisible()

  await page.getByRole('button', { name: 'Actions for Synth item 27' }).click()
  await page.getByRole('menuitem', { name: 'Restock target…' }).click()
  const firstMonths = await monthsInDialog(page, 'Restock target for Synth item 27')
  await expect(firstMonths).toHaveValue('7')
  await page.getByRole('dialog', { name: 'Restock target for Synth item 27' }).getByRole('button', { name: 'Save' }).click()
  await expect(page.getByRole('dialog')).toHaveCount(0)

  await page.getByRole('button', { name: 'Actions for Synth item 28' }).click()
  await page.getByRole('menuitem', { name: 'Restock target…' }).click()
  const secondMonths = await monthsInDialog(page, 'Restock target for Synth item 28')
  await expect(secondMonths).toHaveValue('2')
})

for (const width of [390, 1280]) {
  test(`phone and desktop widths keep per-product editor values (${width}px)`, async ({ page }) => {
    await page.setViewportSize({ width, height: 844 })
    await installSyntheticInventory(page)
    await page.goto('/inventory')
    await page.getByRole('button', { name: 'Actions for Synth item 27' }).click()
    await page.getByRole('menuitem', { name: 'Restock target…' }).click()
    const firstMonths = await monthsInDialog(page, 'Restock target for Synth item 27')
    await expect(firstMonths).toHaveValue('7')
    await page.getByRole('dialog', { name: 'Restock target for Synth item 27' }).getByRole('button', { name: 'Save' }).click()
    await page.getByRole('button', { name: 'Actions for Synth item 28' }).click()
    await page.getByRole('menuitem', { name: 'Restock target…' }).click()
    await expect(await monthsInDialog(page, 'Restock target for Synth item 28')).toHaveValue('2')
  })
}