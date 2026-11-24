import { AxeBuilder } from '@axe-core/playwright'
import { expect, test, type Locator, type Page } from '@playwright/test'

const email = 'e2e@stockbot.test'
const password = 'correct horse'
const barcode = '0123456789012'

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

async function expectNoViolations(page: Page) {
  const result = await new AxeBuilder({ page }).analyze()
  expect(result.violations.map((v) => `${v.id}: ${v.help} (${v.nodes.length})`)).toEqual([])
}

test('signs in on web with the cookie session and reaches the queue', async ({ page, context }) => {
  const sent: { url: string; headers: Record<string, string> }[] = []
  page.on('request', (request) => {
    if (request.url().includes('/api/')) sent.push({ url: request.url(), headers: request.headers() })
  })

  await page.goto('/')
  await expect(page.getByRole('heading', { name: 'Stockbot' })).toBeVisible()
  await expect(page.getByLabel('Server')).toHaveCount(0)
  await expectNoViolations(page)

  await page.getByLabel('Email').fill(email)
  await page.getByLabel('Password').fill(password)
  await page.getByRole('button', { name: 'Sign in' }).click()

  await expect(page.getByText('Nothing is waiting.')).toBeVisible()
  await expectNoViolations(page)

  const cookies = await context.cookies()
  const session = cookies.find((cookie) => cookie.name.includes('stockbot_session'))
  expect(session?.httpOnly).toBe(true)
  const stored = await page.evaluate(() => JSON.stringify({ ...localStorage }) + JSON.stringify({ ...sessionStorage }))
  expect(stored).not.toContain('sbs_')

  expect(sent.length).toBeGreaterThan(0)
  for (const request of sent) {
    expect(request.headers['authorization']).toBeUndefined()
    expect(request.headers['x-stockbot-client']).toBeUndefined()
  }

  const product = await api(page, 'POST', '/api/products', {
    name: 'Rolled Oats',
    category: 'Pantry',
    unitOfMeasure: 'ea',
    barcode,
  })
  expect(product.status, product.body).toBeLessThan(300)
  const products = JSON.parse((await api(page, 'GET', '/api/products')).body) as { id: string; name: string }[]
  const productId = products.find((candidate) => candidate.name === 'Rolled Oats')?.id
  expect((await api(page, 'POST', '/api/products/overrides', { barcode, productId })).status).toBeLessThan(300)
  const scan = await api(page, 'POST', '/api/scans', { barcode, direction: 'stock_in', userId: 'user-1' })
  expect(scan.status, scan.body).toBe(201)

  await page.reload()
  await expect(page.getByText('Rolled Oats')).toBeVisible()
  const card = await page.locator('[data-testid^="scan-"]').first().boundingBox()
  expect(card?.height ?? Infinity).toBeLessThan(120)
  await expect(page.getByText('Nothing is waiting.')).toHaveCount(0)
  await expectNoViolations(page)
})

async function signIn(page: Page) {
  await page.goto('/')
  await page.getByLabel('Email').fill(email)
  await page.getByLabel('Password').fill(password)
  await page.getByRole('button', { name: 'Sign in' }).click()
  await expect(page.getByRole('heading', { name: 'Scan queue' })).toBeVisible()
}

async function seedScan(page: Page, name: string, code: string) {
  const created = await api(page, 'POST', '/api/products', { name, category: 'Pantry', unitOfMeasure: 'ea', barcode: code })
  expect(created.status, created.body).toBeLessThan(300)
  const products = JSON.parse((await api(page, 'GET', '/api/products')).body) as { id: string; name: string }[]
  const productId = products.find((candidate) => candidate.name === name)?.id
  expect((await api(page, 'POST', '/api/products/overrides', { barcode: code, productId })).status).toBeLessThan(300)
  expect((await api(page, 'POST', '/api/scans', { barcode: code, direction: 'stock_in', userId: 'user-1' })).status).toBe(201)
}

async function box(locator: Locator) {
  const found = await locator.boundingBox()
  expect(found).not.toBeNull()
  return found!
}

function expectCentered(found: { x: number; width: number }, viewportWidth: number) {
  expect(Math.abs(found.x + found.width / 2 - viewportWidth / 2)).toBeLessThanOrEqual(2)
}

const columnMax = 736
const formMax = 480

test.describe('desktop shell at 1280px', () => {
  test.use({ viewport: { width: 1280, height: 800 } })

  test('centers the sign-in form without phone wording', async ({ page }) => {
    await page.goto('/')
    await expect(page.getByRole('heading', { name: 'Stockbot' })).toBeVisible()
    await expect(page.getByText('Sign in with your email and password.')).toBeVisible()
    await expect(page.getByText('from the phone')).toHaveCount(0)
    const field = await box(page.getByLabel('Email'))
    expect(field.width).toBeLessThanOrEqual(formMax)
    expectCentered(field, 1280)
    await expectNoViolations(page)
  })

  test('lands on the queue under a top navigation in a bounded, centered column', async ({ page }) => {
    await page.goto('/')
    await page.getByLabel('Email').fill(email)
    await page.getByLabel('Password').fill(password)
    await page.getByRole('button', { name: 'Sign in' }).click()
    await expect(page.getByRole('heading', { name: 'Scan queue' })).toBeVisible()
    await seedScan(page, 'Whole Milk', '0123456789029')
    await page.reload()

    expect(new URL(page.url()).pathname).toBe('/')
    await expect(page.getByRole('tab', { name: 'Queue' })).toHaveAttribute('aria-selected', 'true')
    expect(await page.getByRole('navigation').getByRole('tab').allInnerTexts()).toEqual(['Queue', 'Scan', 'Household'])

    const nav = await box(page.getByRole('navigation', { name: 'Sections' }))
    expect(nav.y).toBe(0)
    expect(nav.width).toBe(1280)
    expect(nav.height).toBeLessThan(100)

    const card = await box(page.locator('[data-testid^="scan-"]').first())
    expect(card.width).toBeLessThanOrEqual(columnMax)
    expectCentered(card, 1280)
    const heading = await box(page.getByRole('heading', { name: 'Scan queue' }))
    expect(heading.y).toBeGreaterThan(nav.height)
    await expectNoViolations(page)
  })

  test('bounds the scan stage and centers it', async ({ page }) => {
    await signIn(page)
    await page.getByRole('tab', { name: 'Scan' }).click()
    const stage = page.getByTestId('scan-stage')
    await expect(stage).toBeVisible()
    expect(new URL(page.url()).pathname).toBe('/scan')

    const found = await box(stage)
    expect(found.width).toBeLessThanOrEqual(columnMax)
    expect(found.height).toBeLessThanOrEqual(480)
    expect(found.y + found.height).toBeLessThan(800)
    expectCentered(found, 1280)
    await expectNoViolations(page)
  })

  test('hides the server row and build stamp lines on household', async ({ page }) => {
    await signIn(page)
    await page.getByRole('tab', { name: 'Household' }).click()
    const heading = page.getByRole('heading', { name: 'Household' })
    await expect(heading).toBeVisible()
    await expect(page.getByText('Server', { exact: true })).toHaveCount(0)
    await expect(page.getByText(/Update embedded|Runtime unavailable/)).toHaveCount(0)
    await expect(page.getByText(/^Version \d/)).toBeVisible()

    const signOut = await box(page.getByRole('button', { name: 'Sign out' }))
    expect(signOut.x).toBeGreaterThanOrEqual((1280 - columnMax) / 2)
    expect(signOut.x + signOut.width).toBeLessThanOrEqual((1280 + columnMax) / 2)
    await expectNoViolations(page)
  })
})

test.describe('phone shell at 390px', () => {
  test.use({ viewport: { width: 390, height: 844 } })

  test('keeps the bottom tab bar and fills the width', async ({ page }) => {
    await signIn(page)
    await seedScan(page, 'Brown Rice', '0123456789036')
    await page.reload()
    await expect(page.getByText('Brown Rice')).toBeVisible()
    await expect(page.getByRole('tab', { name: 'Queue' })).toHaveAttribute('aria-selected', 'true')
    expect(await page.getByRole('navigation').getByRole('tab').allInnerTexts()).toEqual(['Queue', 'Scan', 'Household'])

    const tabs = await box(page.getByRole('navigation').getByRole('tablist'))
    expect(tabs.y).toBeGreaterThan(844 / 2)
    expect(tabs.y + tabs.height).toBeLessThanOrEqual(844)
    expect(tabs.width).toBe(390)
    await expect(page.getByText('Stockbot', { exact: true })).toHaveCount(0)

    const card = await box(page.locator('[data-testid^="scan-"]').first())
    expect(card.width).toBeGreaterThan(390 - 60)
    await expectNoViolations(page)

    await page.getByRole('tab', { name: 'Scan' }).click()
    const stage = await box(page.getByTestId('scan-stage'))
    expect(stage.width).toBe(390)
    expect(stage.height).toBeGreaterThan(300)
    await expectNoViolations(page)

    await page.getByRole('tab', { name: 'Household' }).click()
    await expect(page.getByRole('heading', { name: 'Household' })).toBeVisible()
    await expectNoViolations(page)
  })
})
