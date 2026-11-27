import AxeBuilder from '@axe-core/playwright'
import { expect, test, type APIRequestContext, type Locator, type Page } from '@playwright/test'
import { resetSharedState } from './helpers'

const artifactDir = '/opt/cursor/artifacts'

async function seedShelf(request: APIRequestContext) {
  const created: { name: string; itemId: string; productId: string }[] = []
  for (let index = 1; index <= 40; index += 1) {
    const name = index === 7
      ? 'Longnamewithoutspacesforthesheetwrapcheckatthreehundredtwentypixels'
      : `Synth detail ${String(index).padStart(2, '0')}`
    const barcode = String(230000000000 + index)
    const response = await request.post('/api/products', {
      data: { name, category: 'Pantry', unitOfMeasure: 'unit', barcode },
    })
    expect(response.ok()).toBe(true)
    const listed = await request.get('/api/products')
    const products = await listed.json() as { id: string; name: string }[]
    const product = products.find((entry) => entry.name === name)
    if (!product) throw new Error(`missing ${name}`)
    expect((await request.post('/api/products/overrides', { data: { barcode, productId: product.id } })).ok()).toBe(true)
    const ensured = await request.post(`/api/products/${product.id}/item`)
    expect(ensured.ok()).toBe(true)
    const item = await ensured.json() as { itemId: string }
    expect((await request.post(`/api/inventory/${item.itemId}/instances`, { data: {} })).ok()).toBe(true)
    created.push({ name, itemId: item.itemId, productId: product.id })
  }
  const loose = await request.post('/api/products', {
    data: { name: 'Unstocked group member', category: 'Pantry', unitOfMeasure: 'unit', barcode: '230000000099' },
  })
  expect(loose.ok()).toBe(true)
  const products = await (await request.get('/api/products')).json() as { id: string; name: string }[]
  const unstocked = products.find((entry) => entry.name === 'Unstocked group member')
  const stocked = created[0]
  if (!unstocked) throw new Error('unstocked product missing')
  const group = await request.post('/api/groups', {
    data: { name: 'Synth beans', productIds: [stocked.productId, unstocked.id] },
  })
  expect(group.ok()).toBe(true)
  return created
}

async function scroller(page: Page) {
  return page.locator('.app-shell > .mantine-AppShell-main')
}

async function scrollTop(page: Page) {
  return (await scroller(page)).evaluate((element) => element.scrollTop)
}

async function setScheme(page: Page, scheme: 'light' | 'dark') {
  await page.evaluate((value) => {
    window.localStorage.setItem('mantine-color-scheme-value', value)
    document.documentElement.setAttribute('data-mantine-color-scheme', value)
  }, scheme)
  await page.reload()
}

test.describe.serial('item detail sheet and list view', () => {
  let items: { name: string; itemId: string; productId: string }[] = []

  test.beforeAll(async ({ request }) => {
    await resetSharedState(request)
    items = await seedShelf(request)
  })

  test.afterAll(async ({ request }) => {
    await resetSharedState(request)
  })

  test('spec overlay: child restock modal and group drawer targets and header clearance at 390', async ({ page }) => {
    const minTarget = 43.99
    const assertDialogTargets = async (dialog: Locator, includeNumberSteppers = false) => {
      await expect(dialog).toBeVisible()
      const buttonSelector = includeNumberSteppers
        ? 'button'
        : 'button:not(.mantine-NumberInput-control)'
      const boxes = await dialog.evaluate((root, { min, buttonSelector }) => {
        const controls = root.querySelectorAll(
          `${buttonSelector}, input:not([type="hidden"]), .mantine-Modal-close, .mantine-Drawer-close`,
        )
        return [...controls]
          .map((element) => {
            const box = element.getBoundingClientRect()
            const style = getComputedStyle(element)
            const label =
              element.getAttribute('aria-label')
              || element.getAttribute('name')
              || element.textContent?.trim()
              || element.tagName
            const input = element instanceof HTMLInputElement
            return {
              label,
              height: box.height,
              width: box.width,
              input,
              visible:
                style.display !== 'none'
                && style.visibility !== 'hidden'
                && box.height > 0
                && box.width > 0,
            }
          })
          .filter((box) => box.visible)
          .map(({ label, height, width, input }) => ({ label, height, width, input }))
      }, { min: minTarget, buttonSelector })
      expect(boxes.length).toBeGreaterThan(0)
      for (const box of boxes) {
        expect(box.height, `${box.label} height`).toBeGreaterThanOrEqual(minTarget)
        if (!box.input) {
          expect(box.width, `${box.label} width`).toBeGreaterThanOrEqual(minTarget)
        }
      }
    }
    const assertModalBelowHeader = async () => {
      const cleared = await page.evaluate(() => {
        const header = document.querySelector('.app-shell > .mantine-AppShell-header')
        const content = document.querySelector('.restock-target-editor .mantine-Modal-content')
        if (!(header instanceof HTMLElement) || !(content instanceof HTMLElement)) return false
        return content.getBoundingClientRect().top >= header.getBoundingClientRect().bottom - 1
      })
      expect(cleared).toBe(true)
    }

    await page.setViewportSize({ width: 390, height: 844 })
    await page.goto(`/inventory?item=${items[7].itemId}`)
    const sheet = page.getByRole('dialog', { name: 'Synth detail 08' })
    await expect(sheet).toBeVisible()
    await sheet.getByRole('button', { name: /Change|Set for this item/ }).click()
    await expect(page.getByRole('dialog', { name: /Restock target/ })).toBeVisible()
    await assertModalBelowHeader()
    const editorDialog = page.getByRole('dialog', { name: /Restock target/ })
    await assertDialogTargets(editorDialog, true)
    await page.keyboard.press('Escape')
    await expect(editorDialog).toHaveCount(0)

    await sheet.getByRole('button', { name: 'Add to a group…' }).click()
    const groupDialog = page.locator('.shelf-sheet.inventory-child-overlay[role="dialog"]')
    await assertDialogTargets(groupDialog)
  })

  test('spec 26/z-index: Inventory nav tab resets list at 390 while sheet is open', async ({ page }) => {
    await page.setViewportSize({ width: 390, height: 844 })
    await page.goto('/inventory')
    await page.getByLabel('Search products or groups').fill('detail')
    await page.getByRole('button', { name: 'Details for Synth detail 03' }).click()
    await expect(page.getByRole('dialog', { name: 'Synth detail 03' })).toBeVisible()
    const tab = page.getByRole('navigation', { name: 'Sections' }).getByRole('link', { name: 'Inventory' })
    await expect(tab).toBeVisible()
    await tab.click()
    await expect(page.getByRole('dialog')).toHaveCount(0)
    await expect(page.getByLabel('Search products or groups')).toHaveValue('')
    expect(await scrollTop(page)).toBe(0)
  })

  test('spec R16: deep link Back stays on inventory and closes the sheet', async ({ page }) => {
    await page.setViewportSize({ width: 1280, height: 800 })
    await page.goto(`/inventory?filter=low&item=${items[2].itemId}`)
    await expect(page.getByRole('dialog', { name: 'Synth detail 03' })).toBeVisible()
    await page.goBack()
    await expect(page).toHaveURL('/inventory?filter=low')
    await expect(page.getByRole('dialog')).toHaveCount(0)
  })

  test('spec R16: deep link with restock editor closes child then sheet on successive Back', async ({ page }) => {
    await page.setViewportSize({ width: 1280, height: 800 })
    await page.goto(`/inventory?item=${items[7].itemId}`)
    const sheet = page.getByRole('dialog', { name: 'Synth detail 08' })
    await expect(sheet).toBeVisible()
    await sheet.getByRole('button', { name: /Change|Set for this item/ }).click()
    await expect(page.getByRole('dialog', { name: /Restock target/ })).toBeVisible()
    await page.goBack()
    await expect(page.getByRole('dialog', { name: /Restock target/ })).toHaveCount(0)
    await expect(sheet).toBeVisible()
    await page.goBack()
    await expect(page).toHaveURL('/inventory')
    await expect(page.getByRole('dialog')).toHaveCount(0)
  })

  test('spec R16: deep link with Add to a group closes child then sheet on successive Back', async ({ page }) => {
    await page.setViewportSize({ width: 1280, height: 800 })
    await page.goto(`/inventory?item=${items[7].itemId}`)
    const sheet = page.getByRole('dialog', { name: 'Synth detail 08' })
    await expect(sheet).toBeVisible()
    await sheet.getByRole('button', { name: 'Add to a group…' }).click()
    await expect(page.getByRole('dialog', { name: /Add Synth detail 08 to/ })).toBeVisible()
    await page.goBack()
    await expect(page.getByRole('dialog', { name: /Add Synth detail 08 to/ })).toHaveCount(0)
    await expect(sheet).toBeVisible()
    await page.goBack()
    await expect(page).toHaveURL('/inventory')
    await expect(page.getByRole('dialog')).toHaveCount(0)
  })

  test('spec 06: a touch scroll that starts on a row does not open the sheet', async ({ page }) => {
    await page.setViewportSize({ width: 390, height: 844 })
    await page.goto('/inventory')
    const row = page.locator('.shelf-row').first()
    await expect(row).toBeVisible()
    const box = await row.boundingBox()
    if (!box) throw new Error('row has no box')
    const client = await page.context().newCDPSession(page)
    const x = Math.round(box.x + box.width / 2)
    const startY = Math.round(box.y + box.height / 2)
    await client.send('Input.dispatchTouchEvent', { type: 'touchStart', touchPoints: [{ x, y: startY }] })
    for (let step = 1; step <= 8; step += 1) {
      await client.send('Input.dispatchTouchEvent', {
        type: 'touchMove',
        touchPoints: [{ x, y: startY - step * 20 }],
      })
    }
    await client.send('Input.dispatchTouchEvent', { type: 'touchEnd', touchPoints: [] })
    await client.detach()
    await expect(page.getByRole('dialog')).toHaveCount(0)
  })

  test('spec 19 and spec 20: leaving for history and returning keeps the row, search and filter', async ({ page }) => {
    await page.setViewportSize({ width: 1280, height: 800 })
    await page.goto('/inventory')
    await expect(page.getByRole('heading', { name: 'Synth detail 40' })).toBeVisible()
    await page.getByLabel('Search products or groups').fill('detail')
    const target = page.getByRole('heading', { name: 'Synth detail 25' })
    await target.scrollIntoViewIfNeeded()
    const before = await scrollTop(page)
    expect(before).toBeGreaterThan(100)
    const row = page.getByRole('article').filter({ has: target })
    await row.getByRole('button', { name: 'History' }).click()
    await page.locator('a.history-back', { hasText: 'Inventory' }).click()
    await expect(page.getByRole('heading', { name: 'Synth detail 25' })).toBeVisible()
    await expect(page.getByLabel('Search products or groups')).toHaveValue('detail')
    expect(Math.abs(await scrollTop(page) - before)).toBeLessThanOrEqual(1)
  })

  test('spec 21: browser Back and the header tab return to the saved list', async ({ page }) => {
    await page.setViewportSize({ width: 1280, height: 800 })
    await page.goto('/inventory')
    await page.getByLabel('Search products or groups').fill('detail')
    const target = page.getByRole('heading', { name: 'Synth detail 25' })
    await target.scrollIntoViewIfNeeded()
    const before = await scrollTop(page)
    await page.getByRole('article').filter({ has: target }).getByRole('button', { name: 'History' }).click()
    await page.goBack()
    await expect(page.getByLabel('Search products or groups')).toHaveValue('detail')
    expect(Math.abs(await scrollTop(page) - before)).toBeLessThanOrEqual(1)
    await page.goForward()
    await page.waitForURL(/history/)
    await page.goBack()
    await expect(page.getByLabel('Search products or groups')).toHaveValue('detail')
    expect(Math.abs(await scrollTop(page) - before)).toBeLessThanOrEqual(1)
    await page.getByRole('navigation', { name: 'Sections' }).getByRole('link', { name: 'History' }).click()
    await page.getByRole('navigation', { name: 'Sections' }).getByRole('link', { name: 'Inventory' }).click()
    await expect(page.getByLabel('Search products or groups')).toHaveValue('detail')
    expect(Math.abs(await scrollTop(page) - before)).toBeLessThanOrEqual(1)
  })

  test('spec 22: a delayed return holds scroll on the placeholder', async ({ page }) => {
    await page.setViewportSize({ width: 1280, height: 800 })
    await page.goto('/inventory')
    await page.getByLabel('Search products or groups').fill('detail')
    const target = page.getByRole('heading', { name: 'Synth detail 25' })
    await target.scrollIntoViewIfNeeded()
    const before = await scrollTop(page)
    await page.route('**/api/inventory', async (route) => {
      if (route.request().method() === 'GET') await new Promise((resolve) => setTimeout(resolve, 500))
      await route.continue()
    })
    await page.getByRole('article').filter({ has: target }).getByRole('button', { name: 'History' }).click()
    await page.locator('a.history-back', { hasText: 'Inventory' }).click()
    const placeholder = page.locator('.shelf-placeholder')
    await expect(placeholder).toBeVisible()
    const during = await scrollTop(page)
    expect(during).toBeGreaterThan(100)
    expect(Math.abs(during - before)).toBeLessThanOrEqual(1)
    await expect(page.getByRole('status')).toContainText('Loading inventory')
    await expect(placeholder).toHaveAttribute('aria-hidden', 'true')
    await expect(page.locator('.shelf-list')).toHaveAttribute('aria-busy', 'true')
    await expect(page.getByRole('heading', { name: 'Synth detail 25' })).toBeVisible()
    await expect(page.getByRole('status')).not.toContainText('Loading inventory')
    await expect(page.locator('.shelf-list')).toHaveAttribute('aria-busy', 'false')
    expect(Math.abs(await scrollTop(page) - before)).toBeLessThanOrEqual(1)
    await expect(page.getByRole('status').filter({ hasText: 'Loading inventory' })).toHaveCount(0)
  })

  test('spec 26: the Inventory tab resets the list when it is already open', async ({ page }) => {
    await page.setViewportSize({ width: 1280, height: 800 })
    await page.goto('/inventory')
    await page.getByLabel('Search products or groups').fill('detail')
    await page.getByRole('button', { name: 'Details for Synth detail 03' }).click()
    await expect(page.getByRole('dialog', { name: 'Synth detail 03' })).toBeVisible()
    await page.getByRole('navigation', { name: 'Sections' }).getByRole('link', { name: 'Inventory' }).click()
    await expect(page.getByRole('dialog')).toHaveCount(0)
    await expect(page.getByLabel('Search products or groups')).toHaveValue('')
    expect(await scrollTop(page)).toBe(0)
  })

  test('spec 27: saving a target in the row menu leaves scroll in place', async ({ page }) => {
    await page.setViewportSize({ width: 1280, height: 800 })
    await page.goto('/inventory')
    const target = page.getByRole('heading', { name: 'Synth detail 25' })
    await target.scrollIntoViewIfNeeded()
    const before = await scrollTop(page)
    const row = page.getByRole('article').filter({ has: target })
    await row.getByRole('button', { name: 'Actions for Synth detail 25' }).click()
    await page.getByRole('menuitem', { name: 'Restock target…' }).click()
    const dialog = page.getByRole('dialog', { name: 'Restock target for Synth detail 25' })
    await dialog.getByRole('button', { name: 'Save' }).click()
    await expect(dialog).toHaveCount(0)
    expect(Math.abs(await scrollTop(page) - before)).toBeLessThanOrEqual(1)
  })

  test('spec 28: select mode is off after returning', async ({ page }) => {
    await page.setViewportSize({ width: 1280, height: 800 })
    await page.goto('/inventory')
    await page.getByRole('button', { name: 'Select' }).click()
    await expect(page.getByRole('button', { name: 'Done' })).toBeVisible()
    await page.getByRole('article').filter({ has: page.getByRole('heading', { name: 'Synth detail 03' }) }).getByRole('button', { name: 'History' }).click()
    await page.locator('a.history-back', { hasText: 'Inventory' }).click()
    await expect(page.getByRole('button', { name: 'Select' })).toBeVisible()
    await expect(page.getByRole('checkbox')).toHaveCount(0)
  })

  test('spec 29: Back from History opened in the sheet reopens the sheet', async ({ page }) => {
    await page.setViewportSize({ width: 1280, height: 800 })
    await page.goto('/inventory')
    await page.getByRole('button', { name: 'Details for Synth detail 04' }).click()
    const dialog = page.getByRole('dialog', { name: 'Synth detail 04' })
    await dialog.getByRole('link', { name: 'History' }).click()
    await page.goBack()
    await expect(page.getByRole('dialog', { name: 'Synth detail 04' })).toBeVisible()
    await expect(page).toHaveURL(/item=/)
  })

  test('spec 11 and spec 14: a long name wraps at 320 and the edit block does not jump', async ({ page }) => {
    await page.setViewportSize({ width: 320, height: 700 })
    await page.goto('/inventory')
    await page.getByRole('button', { name: /Details for Longname/ }).click()
    const dialog = page.getByRole('dialog')
    const title = dialog.getByRole('heading').first()
    await expect(title).toBeVisible()
    const overflow = await title.evaluate((element) => element.scrollWidth - element.clientWidth)
    expect(overflow).toBeLessThanOrEqual(1)
    const edit = dialog.getByRole('button', { name: 'Edit product' })
    const before = await edit.boundingBox()
    await expect(dialog.getByText(/Barcode:/)).toBeVisible()
    const after = await edit.boundingBox()
    expect(before && after && Math.abs(before.y - after.y)).toBeLessThanOrEqual(1)
  })

  test('spec 33 spec 34 and spec 35: Esc and Back close only the top overlay', async ({ page }) => {
    await page.setViewportSize({ width: 1280, height: 800 })
    await page.goto('/inventory')
    await page.getByRole('button', { name: 'Details for Synth detail 08' }).click()
    const sheet = page.getByRole('dialog', { name: 'Synth detail 08' })
    const before = await scrollTop(page)
    await sheet.getByRole('button', { name: /Change|Set for this item/ }).click()
    const editor = page.getByRole('dialog', { name: /Restock target/ })
    await expect(editor).toBeVisible()
    await page.keyboard.press('Escape')
    await expect(editor).toHaveCount(0)
    await expect(sheet).toBeVisible()
    await expect(page).toHaveURL(/item=/)
    await expect(sheet.getByRole('button', { name: /Change|Set for this item/ })).toBeFocused()
    expect(Math.abs(await scrollTop(page) - before)).toBeLessThanOrEqual(1)
    await expect(page.locator('.shelf-row.is-return')).toHaveCount(0)
    await page.keyboard.press('Escape')
    await expect(page.getByRole('dialog')).toHaveCount(0)
    await expect(page.getByRole('button', { name: 'Details for Synth detail 08' })).toBeFocused()

    await page.getByRole('button', { name: 'Details for Synth detail 08' }).click()
    await sheet.getByRole('button', { name: 'Add to a group…' }).click()
    await expect(page.getByRole('dialog', { name: /Add Synth detail 08 to/ })).toBeVisible()
    await page.goBack()
    await expect(page.getByRole('dialog', { name: 'Synth detail 08' })).toBeVisible()
    await expect(page).toHaveURL(/item=/)
    await page.goBack()
    await expect(page.getByRole('dialog')).toHaveCount(0)

    await page.reload()
    await page.goto(`/inventory?item=${items[7].itemId}`)
    await expect(page.getByRole('dialog', { name: 'Synth detail 08' })).toBeVisible()
    await expect(page.getByRole('dialog')).toHaveCount(1)
  })

  test('spec 32: a group member with no item row cannot open a sheet', async ({ page }) => {
    await page.setViewportSize({ width: 1280, height: 800 })
    await page.goto('/inventory?filter=groups')
    await page.getByRole('button', { name: 'Synth beans', exact: true }).click()
    const member = page.locator('.shelf-member').filter({ hasText: 'Unstocked group member' })
    await expect(member).toBeVisible()
    const cursor = await member.evaluate((element) => getComputedStyle(element).cursor)
    expect(cursor).not.toBe('pointer')
    await member.click()
    await expect(page.getByRole('dialog')).toHaveCount(0)
    await member.getByRole('button', { name: 'Actions for Unstocked group member' }).click()
    await expect(page.getByRole('menuitem', { name: 'Move to another group' })).toBeVisible()
  })

  test('spec 30 and spec 31: contrast, touch size and screenshots', async ({ page }) => {
    test.setTimeout(120_000)
    const shot = async (name: string) => {
      await page.screenshot({ path: `${artifactDir}/${name}.png`, fullPage: false })
    }
    const assertTargets = async () => {
      const boxes = await page.locator('.item-detail').getByRole('button').evaluateAll((elements) =>
        elements.map((element) => {
          const box = element.getBoundingClientRect()
          return {
            name: element.getAttribute('aria-label') || element.textContent || '',
            width: box.width,
            height: box.height,
          }
        }),
      )
      expect(boxes.length).toBeGreaterThan(0)
      for (const box of boxes) {
        expect(box.width, box.name).toBeGreaterThanOrEqual(44)
        expect(box.height, box.name).toBeGreaterThanOrEqual(44)
      }
    }
    const axeSheet = async () => {
      const result = await new AxeBuilder({ page }).include('.item-detail').analyze()
      expect(result.violations.map((violation) => violation.id)).toEqual([])
    }
    const detail = page.getByRole('dialog', { name: 'Synth detail 02' })

    await page.setViewportSize({ width: 1280, height: 800 })
    await page.goto('/inventory')
    await page.getByRole('button', { name: 'Details for Synth detail 02' }).click()
    await expect(detail).toBeVisible()
    await expect(detail.getByText('No photo')).toBeVisible()
    await assertTargets()
    await axeSheet()
    await shot('item-detail-1280-light-ideal')
    await shot('item-detail-1280-light-no-photo')

    await setScheme(page, 'dark')
    await expect(detail).toBeVisible()
    await axeSheet()
    await shot('item-detail-1280-dark-ideal')
    await shot('item-detail-1280-dark-no-photo')

    await page.setViewportSize({ width: 390, height: 844 })
    await setScheme(page, 'light')
    await expect(detail).toBeVisible()
    const phone = await page.locator('.item-detail .mantine-Drawer-content').boundingBox()
    expect(phone && phone.height).toBeGreaterThan(700)
    await assertTargets()
    await axeSheet()
    await shot('item-detail-390-light-ideal')
    await setScheme(page, 'dark')
    await expect(detail).toBeVisible()
    await axeSheet()
    await shot('item-detail-390-dark-ideal')

    await page.setViewportSize({ width: 320, height: 700 })
    await page.goto('/inventory')
    await page.getByRole('button', { name: /Details for Longname/ }).click()
    await expect(page.getByRole('dialog', { name: /Longname/ })).toBeVisible()
    await assertTargets()
    await axeSheet()
    await shot('item-detail-320-light-long-name')
    await setScheme(page, 'dark')
    await expect(page.getByRole('dialog', { name: /Longname/ })).toBeVisible()
    await shot('item-detail-320-dark-long-name')

    await page.setViewportSize({ width: 640, height: 400 })
    await setScheme(page, 'light')
    const zoomed = await page.locator('.item-detail .mantine-Drawer-content').boundingBox()
    expect(zoomed && zoomed.width).toBeGreaterThan(600)
    expect(zoomed && zoomed.height).toBeGreaterThan(350)
    await axeSheet()
    await shot('item-detail-200-zoom-light')

    await page.setViewportSize({ width: 1280, height: 800 })
    await page.goto('/inventory?item=missing-item')
    await expect(page.getByText('This product is no longer in your inventory.')).toBeVisible()
    await shot('item-detail-1280-light-deleted')
    await page.setViewportSize({ width: 320, height: 700 })
    await shot('item-detail-320-light-deleted')

    await page.setViewportSize({ width: 1280, height: 800 })
    await page.goto('/inventory')
    await page.getByRole('button', { name: 'Details for Synth detail 02' }).click()
    await detail.getByRole('button', { name: /Change|Set for this item/ }).click()
    await expect(page.getByRole('dialog', { name: /Restock target/ })).toBeVisible()
    await shot('item-detail-1280-light-editor')
    await page.keyboard.press('Escape')

    await page.context().setOffline(true)
    await expect(detail.getByText(/You're offline\. This is what we knew at/)).toBeVisible()
    await shot('item-detail-1280-light-offline')
    await page.context().setOffline(false)

    const delayList = async (route: import('@playwright/test').Route) => {
      const path = new URL(route.request().url()).pathname
      if (route.request().method() === 'GET' && path === '/api/inventory') {
        await new Promise((resolve) => setTimeout(resolve, 800))
      }
      await route.continue().catch(() => {})
    }
    await page.route('**/api/inventory', delayList)
    await page.goto(`/inventory?item=${items[1].itemId}`)
    await expect(page.locator('.item-detail-title-skeleton')).toBeVisible()
    await shot('item-detail-1280-light-loading')
    await page.unroute('**/api/inventory')

    await page.goto('/inventory')
    const held = page.getByRole('heading', { name: 'Synth detail 25' })
    await held.scrollIntoViewIfNeeded()
    await page.route('**/api/inventory', delayList)
    await page.getByRole('article').filter({ has: held }).getByRole('button', { name: 'History' }).click()
    await page.locator('a.history-back', { hasText: 'Inventory' }).click()
    const placeholder = page.locator('.shelf-placeholder')
    await expect(placeholder).toBeVisible()
    expect(await scrollTop(page)).toBeGreaterThan(100)
    await shot('item-detail-list-loading-held-scroll')
    await page.unroute('**/api/inventory')

    await page.goto('/inventory?filter=groups')
    await page.getByRole('button', { name: 'Synth beans', exact: true }).click()
    await expect(page.locator('.shelf-member').filter({ hasText: 'Unstocked group member' })).toBeVisible()
    await shot('item-detail-group-member-no-item')
  })

  test('spec 40: reduced motion removes the placeholder pulse', async ({ page }) => {
    await page.emulateMedia({ reducedMotion: 'reduce' })
    await page.goto('/inventory')
    const row = page.getByRole('article').filter({ has: page.getByRole('heading', { name: 'Synth detail 25' }) })
    await row.getByRole('button', { name: 'History' }).click()
    await expect(page.locator('a.history-back', { hasText: 'Inventory' })).toBeVisible()
    await page.route('**/api/inventory', async (route) => {
      if (route.request().method() === 'GET' && new URL(route.request().url()).pathname === '/api/inventory') {
        await new Promise((resolve) => setTimeout(resolve, 400))
      }
      await route.continue().catch(() => {})
    })
    const loading = page.locator('a.history-back', { hasText: 'Inventory' }).click()
    const placeholder = page.locator('.shelf-placeholder')
    await expect(placeholder).toBeVisible()
    const animation = await placeholder.evaluate((element) => getComputedStyle(element).animationName)
    expect(animation).toBe('none')
    await loading
  })

  test('spec 23: a reload starts at the top', async ({ page }) => {
    await page.setViewportSize({ width: 1280, height: 800 })
    await page.goto('/inventory')
    await page.getByRole('heading', { name: 'Synth detail 25' }).scrollIntoViewIfNeeded()
    expect(await scrollTop(page)).toBeGreaterThan(100)
    await page.reload()
    await expect(page.locator('.shelf-row').first()).toBeVisible()
    expect(await scrollTop(page)).toBe(0)
  })

  test('spec 24: the last opened row is highlighted on return and the highlight clears', async ({ page }) => {
    await page.setViewportSize({ width: 1280, height: 800 })
    await page.goto('/inventory')
    const row = page.locator('.shelf-row').filter({ has: page.getByRole('heading', { name: 'Synth detail 25' }) })
    const before = await row.boundingBox()
    await row.getByRole('button', { name: 'Details for Synth detail 25' }).click()
    await page.getByRole('dialog', { name: 'Synth detail 25' }).getByRole('link', { name: 'History' }).click()
    await page.locator('a.history-back', { hasText: 'Inventory' }).click()
    await expect(row).toBeVisible()
    await expect(row).toHaveClass(/is-return/)
    const after = await row.boundingBox()
    expect(after?.height).toBe(before?.height)
    await expect(row).not.toHaveClass(/is-return/, { timeout: 3000 })
    await page.emulateMedia({ reducedMotion: 'reduce' })
    await row.getByRole('button', { name: 'Details for Synth detail 25' }).click()
    await page.getByRole('dialog', { name: 'Synth detail 25' }).getByRole('link', { name: 'History' }).click()
    await page.locator('a.history-back', { hasText: 'Inventory' }).click()
    await expect(row).toHaveClass(/is-return/)
    const transition = await row.evaluate((element) => getComputedStyle(element).transitionProperty)
    expect(transition).not.toBe('background-color')
  })
})
