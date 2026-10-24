import { expect, test, type Page } from '@playwright/test'

const sentence = "Can't reach Stockbot. Check your connection and try again."

// 900 sits between the 48em shell inset and the 64em scan column.
// Moving either breakpoint across it changes the banner box.
const viewports = [
  { width: 390, height: 844, shell: 20 },
  { width: 900, height: 800, shell: 15 },
  { width: 1280, height: 800, shell: 15 },
]

const shellSurfaces = [
  { name: 'queue', link: 'Scan Queue', heading: 'Scan queue' },
  { name: 'inventory', link: 'Inventory', heading: 'Inventory' },
  { name: 'history', link: 'History', heading: 'History' },
]

async function bannerBox(page: Page) {
  const banner = page.getByTestId('server-notice-banner')
  await expect(banner).toBeVisible()
  const box = await banner.boundingBox()
  expect(box).not.toBeNull()
  return box!
}

function expectSides(box: { x: number; y: number; width: number }, side: number, viewport: number) {
  expect(box.x).toBe(side)
  expect(box.width).toBe(viewport - side * 2)
  expect(box.y).toBeGreaterThan(0)
}

function expectGate(box: { x: number; y: number; width: number }, viewport: number) {
  expect(box.x).toBe(20)
  expect(box.width).toBe(viewport - 40)
  expect(box.y).toBe(24)
}

for (const viewport of viewports) {
  test(`shell banner box at ${viewport.width}`, async ({ page }) => {
    await page.setViewportSize({ width: viewport.width, height: viewport.height })
    await page.goto('/')
    await expect(page.getByRole('heading', { name: 'Scan queue' })).toBeVisible()
    await page.context().setOffline(true)

    for (const surface of shellSurfaces) {
      await page.getByRole('link', { name: surface.link, exact: true }).click()
      await expect(page.getByRole('heading', { name: surface.heading })).toBeVisible()
      await expect(page.getByText(sentence)).toBeVisible()
      const box = await bannerBox(page)
      if (surface.name === 'queue' && viewport.width === 1280) {
        const column = await page.locator('.scan-page').boundingBox()
        expect(column).not.toBeNull()
        expect(box.x).toBe(column!.x)
        expect(box.width).toBe(column!.width)
        expect(box.y).toBeGreaterThan(0)
      } else {
        expectSides(box, viewport.shell, viewport.width)
      }
    }
  })

  test(`startup banner box at ${viewport.width}`, async ({ page }) => {
    await page.setViewportSize({ width: viewport.width, height: viewport.height })
    await page.route('**/api/session', (route) => route.abort('internetdisconnected'))
    await page.goto('/')
    await expect(page.getByText(sentence)).toBeVisible()
    expectGate(await bannerBox(page), viewport.width)
  })

  test(`sign-in banner box at ${viewport.width}`, async ({ page }) => {
    await page.setViewportSize({ width: viewport.width, height: viewport.height })
    await page.route('**/api/session', (route) => route.fulfill({
      status: 401,
      contentType: 'application/json',
      body: JSON.stringify({ error: 'Sign in required.' }),
    }))
    await page.route('**/api/login', (route) => route.abort('internetdisconnected'))
    await page.goto('/')
    await page.getByRole('textbox', { name: 'Username' }).fill('pantry')
    await page.getByRole('textbox', { name: 'Password' }).fill('correct-horse')
    await page.getByRole('button', { name: 'Sign in' }).click()
    await expect(page.getByText(sentence)).toBeVisible()
    expectGate(await bannerBox(page), viewport.width)
  })
}
