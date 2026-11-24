import AxeBuilder from '@axe-core/playwright'
import { expect, test, type Page } from '@playwright/test'

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

  await page.getByRole('tab', { name: 'Queue' }).click()
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
  await page.getByRole('tab', { name: 'Queue' }).click()
  await expect(page.getByText('Rolled Oats')).toBeVisible()
  await expect(page.getByText('Nothing is waiting.')).toHaveCount(0)
  await expectNoViolations(page)
})
