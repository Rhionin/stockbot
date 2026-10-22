import AxeBuilder from '@axe-core/playwright'
import { expect, test, type APIRequestContext, type Page } from '@playwright/test'
import { commitSelectedScan, resetToStockIn, scanBarcode } from './helpers'

async function createProduct(
  request: APIRequestContext,
  input: { name: string; category: string; unitOfMeasure: string; netAmount?: number; netUnit?: string },
) {
  const response = await request.post('/api/products', { data: input })
  if (!response.ok()) throw new Error(`create ${input.name}: ${response.status()} ${await response.text()}`)
  return response.json() as Promise<{ id: string; name: string }>
}

async function setScheme(page: Page, scheme: 'light' | 'dark') {
  await page.evaluate((value) => {
    window.localStorage.setItem('mantine-color-scheme-value', value)
    document.documentElement.setAttribute('data-mantine-color-scheme', value)
  }, scheme)
  await page.reload()
  await expect.poll(() => page.evaluate(() => document.documentElement.getAttribute('data-mantine-color-scheme'))).toBe(scheme)
}

function violationText(violations: { id: string; help: string; nodes: { target: string[] }[] }[]) {
  return violations.map((violation) => (
    `${violation.id}: ${violation.help} (${violation.nodes.map((node) => node.target.join(' ')).join('; ')})`
  )).join('\n')
}

async function expectClean(page: Page, extra: string[] = []) {
  let builder = new AxeBuilder({ page }).include('.bin-page')
  for (const selector of extra) builder = builder.include(selector)
  const results = await builder.analyze()
  expect(results.violations, violationText(results.violations)).toEqual([])
}

function channels(color: string): [number, number, number] {
  const match = color.match(/rgba?\((\d+),\s*(\d+),\s*(\d+)/)
  if (!match) throw new Error(`Unparsed color: ${color}`)
  return [Number(match[1]), Number(match[2]), Number(match[3])]
}

function contrast(foreground: string, background: string) {
  const lin = (channel: number) => {
    const value = channel / 255
    return value <= 0.04045 ? value / 12.92 : ((value + 0.055) / 1.055) ** 2.4
  }
  const lum = (rgb: [number, number, number]) => 0.2126 * lin(rgb[0]) + 0.7152 * lin(rgb[1]) + 0.0722 * lin(rgb[2])
  const hi = Math.max(lum(channels(foreground)), lum(channels(background)))
  const lo = Math.min(lum(channels(foreground)), lum(channels(background)))
  return (hi + 0.05) / (lo + 0.05)
}

async function expectListColors(page: Page, scheme: 'light' | 'dark') {
  const row = page.locator('.shelf-row').first()
  const sample = await row.evaluate((el) => {
    const name = el.querySelector('.shelf-name')
    const meta = el.querySelector('.shelf-meta')
    const meter = el.querySelector('.shelf-meter')
    const input = document.querySelector('.bin-page .mantine-Input-input')
    const walk = (start: Element | null) => {
      let node = start
      while (node) {
        const bg = getComputedStyle(node).backgroundColor
        const alpha = bg.match(/rgba\(\d+,\s*\d+,\s*\d+,\s*([\d.]+)\)/)
        if (bg !== 'transparent' && (!alpha || Number(alpha[1]) > 0.05)) return bg
        node = node.parentElement
      }
      return getComputedStyle(document.body).backgroundColor
    }
    const background = walk(el)
    return {
      color: name ? getComputedStyle(name).color : '',
      background,
      summary: meta ? getComputedStyle(meta).color : '',
      well: meter ? getComputedStyle(meter).backgroundColor : '',
      placeholder: input ? getComputedStyle(input, '::placeholder').color : '',
      field: input ? getComputedStyle(input).backgroundColor : '',
      border: input ? getComputedStyle(input).borderTopColor : '',
    }
  })
  expect(contrast(sample.color, sample.background), JSON.stringify(sample)).toBeGreaterThanOrEqual(4.5)
  expect(contrast(sample.summary, sample.background), JSON.stringify(sample)).toBeGreaterThanOrEqual(4.5)
  expect(contrast(sample.placeholder, sample.field), JSON.stringify(sample)).toBeGreaterThanOrEqual(4.5)
  expect(contrast(sample.border, sample.field), JSON.stringify(sample)).toBeGreaterThanOrEqual(3)
  const well = channels(sample.well)
  if (scheme === 'light') {
    expect(well[0], JSON.stringify(sample)).toBeGreaterThan(220)
  } else {
    expect(well[0], JSON.stringify(sample)).toBeGreaterThan(30)
    expect(well[0], JSON.stringify(sample)).toBeLessThan(70)
  }
  await expectClean(page)
}

async function tabTo(page: Page, name: string) {
  const target = page.getByRole('button', { name })
  await page.getByRole('navigation', { name: 'Sections' }).getByRole('link', { name: 'Inventory' }).focus()
  for (let step = 0; step < 24; step += 1) {
    if (await target.evaluate((el) => document.activeElement === el)) return target
    await page.keyboard.press('Tab')
  }
  throw new Error(`Tab never reached ${name}`)
}

test('group page meets axe in light and dark', async ({ page, request }) => {
  test.setTimeout(90_000)
  const lemon = await createProduct(request, {
    name: 'Contrast lemon', category: 'Drinks', unitOfMeasure: 'canister', netAmount: 18.3, netUnit: 'oz',
  })
  const powder = await createProduct(request, {
    name: 'Contrast unsized', category: 'Drinks', unitOfMeasure: 'canister',
  })
  const lemonOverride = await request.post('/api/products/overrides', {
    data: { barcode: '052000449901', productId: lemon.id },
  })
  expect(lemonOverride.ok()).toBe(true)
  const powderOverride = await request.post('/api/products/overrides', {
    data: { barcode: '052000449902', productId: powder.id },
  })
  expect(powderOverride.ok()).toBe(true)

  await page.goto('/')
  await resetToStockIn(page)
  await commitSelectedScan(page, await scanBarcode(page, '052000449901'))
  await commitSelectedScan(page, await scanBarcode(page, '052000449902'))

  const targeted = await request.post('/api/groups', {
    data: { name: 'Contrast powder', productIds: [lemon.id], target: { quantity: 48, dimension: 'mass' } },
  })
  if (!targeted.ok()) throw new Error(await targeted.text())
  const targetedGroup = await targeted.json() as { id: string }

  const empty = await request.post('/api/groups', {
    data: { name: 'Loose contrast powder', productIds: [powder.id] },
  })
  if (!empty.ok()) throw new Error(await empty.text())
  const emptyGroup = await empty.json() as { id: string }

  await page.setViewportSize({ width: 390, height: 844 })
  await page.goto(`/groups/${targetedGroup.id}`)
  await expect(page.getByRole('heading', { name: 'Contrast powder' })).toBeVisible()
  await expect(page.getByRole('meter', { name: 'Stock on hand' })).toHaveAttribute('aria-valuenow', '18.3')
  await setScheme(page, 'light')
  await expect(page.getByRole('heading', { name: 'Contrast powder' })).toBeVisible()
  await expectClean(page)

  const amount = await tabTo(page, '48 oz, Keep on hand amount, pinned, 48 ounces')
  const ring = await amount.evaluate((el) => {
    const style = getComputedStyle(el)
    return { outlineStyle: style.outlineStyle, outlineWidth: style.outlineWidth }
  })
  expect(ring.outlineStyle).not.toBe('none')
  expect(parseFloat(ring.outlineWidth)).toBeGreaterThanOrEqual(2)
  await page.keyboard.press('Enter')
  await expect(page.getByLabel('Ounces')).toBeFocused()
  await page.keyboard.press('Escape')
  await expect(page.getByRole('button', { name: '48 oz, Keep on hand amount, pinned, 48 ounces' })).toBeVisible()

  const time = await tabTo(page, 'Set time, Keep on hand time, not set')
  await time.press('Space')
  await expect(page.getByLabel('Months')).toBeFocused()
  await page.getByRole('button', { name: 'Cancel' }).click()

  await setScheme(page, 'dark')
  await expect(page.getByRole('heading', { name: 'Contrast powder' })).toBeVisible()
  await expectClean(page)

  await page.goto(`/groups/${emptyGroup.id}`)
  await expect(page.getByRole('heading', { name: 'Loose contrast powder' })).toBeVisible()
  await expect(page.getByRole('button', { name: '3 mo default, Keep on hand time, pinned, 3 months, household default' })).toBeVisible()
  await expect(page.getByRole('button', { name: 'Set amount, Keep on hand amount, not set' })).toBeVisible()
  await expect(page.getByText('Set an amount or time')).toBeVisible()
  await expect(page.getByRole('meter', { name: 'Stock on hand' })).toHaveAttribute('aria-valuetext', 'No sizes listed')
  await expectClean(page)

  await setScheme(page, 'light')
  await expect(page.getByRole('heading', { name: 'Loose contrast powder' })).toBeVisible()
  await expectClean(page)

  const unsetAmount = await tabTo(page, 'Set amount, Keep on hand amount, not set')
  await unsetAmount.press('Enter')
  await expect(page.getByLabel('Ounces')).toBeVisible()
  await page.getByRole('button', { name: 'Cancel' }).click()
  const unsetTime = await tabTo(page, '3 mo default, Keep on hand time, pinned, 3 months, household default')
  await unsetTime.press('Enter')
  await expect(page.getByLabel('Months')).toBeVisible()

  expect((await request.post('/api/onboarding/complete')).ok()).toBe(true)
  await page.goto('/groups')
  await setScheme(page, 'light')
  await expect(page).toHaveURL(/\/inventory\?filter=groups/)
  await expect(page.getByRole('heading', { name: 'Inventory' })).toBeVisible()
  await expect(page.getByRole('button', { name: 'Groups', exact: true })).toHaveAttribute('aria-pressed', 'true')
  await expect(page.getByRole('textbox', { name: 'Search products or groups' })).toBeVisible()
  await expectClean(page)
  await expectListColors(page, 'light')

  await setScheme(page, 'dark')
  await expect(page.getByRole('heading', { name: 'Inventory' })).toBeVisible()
  await expectClean(page)
  await expectListColors(page, 'dark')

  const bran = await createProduct(request, {
    name: 'Axe bran muffin', category: 'Baking', unitOfMeasure: 'box', netAmount: 16, netUnit: 'oz',
  })
  const berry = await createProduct(request, {
    name: 'Axe blueberry muffin', category: 'Baking', unitOfMeasure: 'box', netAmount: 16, netUnit: 'oz',
  })
  const fancy = await createProduct(request, {
    name: 'Axe lehi roller', category: 'Baking', unitOfMeasure: 'box', netAmount: 16, netUnit: 'oz',
  })
  const variety = await request.post('/api/groups', {
    data: {
      name: 'Axe muffin mix',
      productIds: [bran.id, berry.id, fancy.id],
      target: { quantity: 32, dimension: 'mass' },
    },
  })
  if (!variety.ok()) throw new Error(await variety.text())
  const varietyGroup = await variety.json() as { id: string }
  expect((await request.put(`/api/groups/${varietyGroup.id}/rule`, {
    data: { rule: 'favor_variety', confirm: true },
  })).ok()).toBe(true)
  expect((await request.put(`/api/groups/${varietyGroup.id}/members/${fancy.id}/restock`, {
    data: { noRestock: true },
  })).ok()).toBe(true)

  await page.setViewportSize({ width: 390, height: 844 })
  await page.goto(`/groups/${varietyGroup.id}`)
  await setScheme(page, 'light')
  await expect(page.getByRole('heading', { name: 'Axe muffin mix' })).toBeVisible()
  await expect(page.getByRole('heading', { name: 'In rotation' })).toBeVisible()
  await expect(page.getByRole('heading', { name: 'Not restocked' })).toBeVisible()
  await expect(page.getByText('⊘ no restock')).toBeVisible()
  await expect(page.getByText(/Next up: .* · rotates through 2/)).toBeVisible()
  const asideInk = await page.locator('.bin-member.is-aside .bin-member-name').evaluate((el) => {
    const color = getComputedStyle(el).color
    let background = getComputedStyle(document.body).backgroundColor
    let node = el.parentElement
    while (node) {
      const bg = getComputedStyle(node).backgroundColor
      const alpha = bg.match(/rgba\(\d+,\s*\d+,\s*\d+,\s*([\d.]+)\)/)
      if (!alpha || Number(alpha[1]) > 0.05) {
        background = bg
        break
      }
      node = node.parentElement
    }
    return { color, background }
  })
  expect(contrast(asideInk.color, asideInk.background), JSON.stringify(asideInk)).toBeGreaterThanOrEqual(4.5)
  await expectClean(page)

  await page.getByRole('button', { name: 'Remove or move Axe bran muffin' }).click()
  await expect(page.getByRole('menuitem', { name: /Don't restock/ })).toBeVisible()
  await expectClean(page, ['[role="menu"]'])
  await page.keyboard.press('Escape')

  await page.getByRole('button', { name: 'Change rule, Favor variety' }).click()
  const varietyDialog = page.getByRole('dialog', { name: 'What to buy next' })
  await expect(varietyDialog.getByText('Products marked "Don\'t restock" are skipped.')).toBeVisible()
  await expectClean(page, ['[role="dialog"]'])
  await page.keyboard.press('Escape')

  await setScheme(page, 'dark')
  await expect(page.getByRole('heading', { name: 'Not restocked' })).toBeVisible()
  await expectClean(page)
  expect((await request.delete(`/api/groups/${varietyGroup.id}`)).ok()).toBe(true)

  await page.goto('/groups/suggestions')
  await expect(page.getByRole('heading', { name: 'Suggestions' })).toBeVisible()
  await expectClean(page)
  await setScheme(page, 'light')
  await expect(page.getByRole('heading', { name: 'Suggestions' })).toBeVisible()
  await expectClean(page)

  // Later specs share this database and match the household-default list line.
  expect((await request.delete(`/api/groups/${targetedGroup.id}`)).ok()).toBe(true)
  expect((await request.delete(`/api/groups/${emptyGroup.id}`)).ok()).toBe(true)
})

test.afterEach(async ({ request }) => {
  const listed = await request.get('/api/groups')
  if (!listed.ok()) return
  const groups = await listed.json() as { id: string; name: string }[]
  for (const group of groups) {
    if (group.name.startsWith('Contrast') || group.name.startsWith('Loose contrast') || group.name.startsWith('Axe ')) {
      await request.delete(`/api/groups/${group.id}`)
    }
  }
})
