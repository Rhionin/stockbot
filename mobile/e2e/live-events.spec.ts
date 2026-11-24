import { expect, test, type Page } from '@playwright/test'

const email = 'e2e@stockbot.test'
const password = 'correct horse'

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

const seeded: string[] = []

test.afterEach(async ({ page }) => {
  for (const id of seeded.splice(0)) await api(page, 'PATCH', `/api/scans/${id}`, { status: 'cancelled' })
})

async function signIn(page: Page) {
  await page.goto('/')
  await page.getByLabel('Email').fill(email)
  await page.getByLabel('Password').fill(password)
  await page.getByRole('button', { name: 'Sign in' }).click()
  await expect(page.getByRole('heading', { name: 'To confirm' })).toBeVisible()
}

async function scan(page: Page, name: string, code: string) {
  const created = await api(page, 'POST', '/api/products', { name, category: 'Pantry', unitOfMeasure: 'ea', barcode: code })
  expect(created.status, created.body).toBeLessThan(300)
  const products = JSON.parse((await api(page, 'GET', '/api/products')).body) as { id: string; name: string }[]
  const productId = products.find((candidate) => candidate.name === name)?.id
  expect((await api(page, 'POST', '/api/products/overrides', { barcode: code, productId })).status).toBeLessThan(300)
  const posted = await api(page, 'POST', '/api/scans', { barcode: code, direction: 'stock_in', userId: 'user-1' })
  expect(posted.status, posted.body).toBe(201)
  const id = (JSON.parse(posted.body) as { id: string }).id
  seeded.push(id)
  return id
}

test('a scan made elsewhere appears in the open queue without a reload', async ({ page }) => {
  await signIn(page)
  let navigations = 0
  page.on('framenavigated', () => {
    navigations += 1
  })
  const before = navigations

  await scan(page, 'Live Lentils', '0123456789043')

  await expect(page.getByText('Live Lentils')).toBeVisible({ timeout: 5000 })
  expect(navigations).toBe(before)
})

test('a scan confirmed elsewhere leaves the open queue without a reload', async ({ page }) => {
  await signIn(page)
  const id = await scan(page, 'Live Barley', '0123456789050')
  await expect(page.getByText('Live Barley')).toBeVisible({ timeout: 5000 })

  const cancelled = await api(page, 'PATCH', `/api/scans/${id}`, { status: 'cancelled' })
  expect(cancelled.status, cancelled.body).toBeLessThan(300)

  await expect(page.getByText('Live Barley')).toHaveCount(0, { timeout: 5000 })
})

test('the scan screen follows a scanner mode change made through the API', async ({ page }) => {
  await signIn(page)
  await page.getByRole('tab', { name: 'Scan' }).click()
  await expect(page.getByRole('button', { name: 'Switch to using up' })).toBeVisible()

  expect((await api(page, 'POST', '/api/scanner/mode', { mode: 'stock_out' })).status).toBeLessThan(300)
  await expect(page.getByRole('button', { name: 'Switch to putting away' })).toBeVisible({ timeout: 5000 })

  expect((await api(page, 'POST', '/api/scanner/mode', { mode: 'stock_in' })).status).toBeLessThan(300)
  await expect(page.getByRole('button', { name: 'Switch to using up' })).toBeVisible({ timeout: 5000 })
})

test('opens one event stream and keeps it, with no poll beside it', async ({ page }) => {
  const streams: string[] = []
  const lists: string[] = []
  page.on('request', (request) => {
    const url = new URL(request.url())
    if (url.pathname === '/api/events') streams.push(request.url())
    if (url.pathname === '/api/scans') lists.push(request.url())
  })
  await signIn(page)
  await page.waitForTimeout(500)
  const listsAfterLoad = lists.length

  await page.waitForTimeout(5000)

  expect(streams).toHaveLength(1)
  expect(lists.length).toBe(listsAfterLoad)
})
