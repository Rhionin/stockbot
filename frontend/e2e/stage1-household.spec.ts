import { expect, test, type Page } from '@playwright/test'
import { existsSync, mkdirSync } from 'node:fs'
import { install, PAIRING, resetStage, stage } from './householdStageFixtures'
// GitHub Actions has no writable /cursor/stores/self. STAGE1_SCREENSHOT_DIR
// overrides the directory when a run should keep the images somewhere else.
const shots = process.env.STAGE1_SCREENSHOT_DIR
  || (existsSync('/cursor/stores/self') ? '/cursor/stores/self/383' : '')

const settleDialog = async (page: Page) => {
  await page.waitForFunction(() => {
    const dialog = document.querySelector('[role="dialog"]')
    if (!(dialog instanceof HTMLElement)) return false
    let node: HTMLElement | null = dialog
    while (node) {
      if (Number(getComputedStyle(node).opacity) < 0.99) return false
      node = node.parentElement
    }
    return true
  })
}

const shot = async (page: Page, name: string, width: number, fullPage = true) => {
  if (!shots) return
  if ((await page.getByRole('dialog').count()) > 0) await settleDialog(page)
  await page.evaluate(() => window.scrollTo(0, 0))
  await page.screenshot({ path: `${shots}/${name}-${width}.png`, fullPage })
}

for (const width of [390, 1280]) {
test.describe('stage-1 household screens', () => {
  test.beforeAll(() => {
    if (shots) mkdirSync(shots, { recursive: true })
  })

  test(`sign-in, households, devices, and export at ${width}`, async ({ page }) => {
    resetStage()
    await page.setViewportSize({ width, height: width === 390 ? 844 : 900 })
    await install(page)
    await page.goto('/')
    const setup = page.getByRole('link', { name: 'Set up Stockbot' })
    await expect(setup).toBeVisible()
    await expect(setup).toHaveAttribute('href', '/setup')
    await expect(page.getByText('pi:8080')).toHaveCount(0)
    await setup.click()
    await expect(page.getByRole('heading', { name: 'Set up Stockbot' })).toBeVisible()
    await page.goto('/')
    await shot(page, 'sign-in', width)

    await page.getByLabel('Email').fill('ada@example.com')
    await page.getByRole('textbox', { name: 'Password' }).fill('correct-horse-battery')
    await page.getByRole('button', { name: 'Sign in' }).click()
    await expect(page.getByRole('button', { name: 'Menu' })).toBeVisible()

    await page.getByRole('button', { name: 'Menu' }).click()
    await page.getByRole('menuitem', { name: 'Household', exact: true }).click()
    await expect(page.getByRole('heading', { name: 'Pantry' })).toBeVisible()
    await expect(page.getByRole('combobox', { name: 'Pantry' })).toBeVisible()
    await shot(page, 'switcher', width)
    await expect(page.getByRole('button', { name: 'Make owner' })).toBeVisible()
    await page.getByRole('button', { name: 'Create invite' }).click()
    await expect(page.getByText(/Invite link:/)).toBeVisible()
    await page.getByRole('button', { name: 'Revoke invite' }).first().click()
    await expect(page.getByText('Cancelled')).toBeVisible()
    await shot(page, 'members-invites', width)

    await expect(page.getByRole('heading', { name: 'Scanners', exact: true })).toBeVisible()
    await shot(page, 'household-scanners', width)
    await page.getByRole('button', { name: 'Connect a scanner' }).first().click()
    const householdPairing = page.getByRole('dialog', { name: 'Connect a scanner' })
    await expect(householdPairing).toBeVisible()
    await expect(householdPairing.getByText('Print setup & wall sheet')).toBeVisible()
    await expect(householdPairing.locator('.pairing-screen-only').getByText(/Scan the pairing barcode to connect/)).toBeVisible()
    if (width === 390) {
      const pairingCode = householdPairing.locator('.pairing-spaced')
      await expect(pairingCode).toHaveCSS('white-space', 'nowrap')
      await expect(pairingCode.evaluate((el) => el.scrollHeight <= el.clientHeight + 1)).resolves.toBe(true)
      const barcodeFrame = householdPairing.locator('.pairing-code128-frame')
      await expect(barcodeFrame.evaluate((host) => {
        const svg = host.querySelector('svg')
        if (svg == null) return false
        if (host.scrollWidth > host.clientWidth + 1) return false
        const hostRect = host.getBoundingClientRect()
        const svgRect = svg.getBoundingClientRect()
        const hostCenter = hostRect.left + hostRect.width / 2
        const svgCenter = svgRect.left + svgRect.width / 2
        return Math.abs(hostCenter - svgCenter) < 2
      })).resolves.toBe(true)
    }
    await shot(page, 'household-pairing', width, false)
    await householdPairing.getByRole('button', { name: 'Close' }).click()
    const scannersSection = page.locator('section').filter({ has: page.getByRole('heading', { name: 'Scanners', exact: true }) })
    await expect(scannersSection.getByRole('button', { name: 'Print wall barcodes' })).toHaveCount(1)
    await expect(scannersSection.getByRole('button', { name: 'Connect a scanner' })).toHaveCount(1)

    await page.getByRole('button', { name: 'Menu' }).click()
    await page.getByRole('menuitem', { name: 'Other households' }).click()
    await expect(page.getByRole('heading', { name: 'Households', exact: true })).toBeVisible()
    await expect(page.getByRole('heading', { name: 'Your households' })).toBeVisible()
    await expect(page.getByRole('heading', { name: 'Start a household' })).toBeVisible()
    await expect(page.getByRole('button', { name: 'Switch or add a household' })).toHaveCount(0)
    await shot(page, 'households', width)

    await page.getByRole('button', { name: 'Menu' }).click()
    await page.getByRole('menuitem', { name: 'Household', exact: true }).click()
    await expect(page.getByRole('button', { name: 'Switch or add a household' })).toBeVisible()
    await shot(page, 'household-other-link', width)

    await page.getByRole('button', { name: 'Export household' }).click()
    await expect(page.getByRole('link', { name: 'Download JSON' })).toBeVisible()
    await expect(page.getByText('Your household will be permanently deleted after 30 days.')).toBeVisible()
    await shot(page, 'export-delete', width)

    await page.getByRole('button', { name: 'Menu' }).click()
    await page.getByRole('menuitem', { name: 'Devices' }).click()
    await expect(page.getByText('Kitchen hub')).toBeVisible()
    await expect(page.getByText(/Offline/)).toBeVisible()
    await shot(page, 'devices', width)
    await expect(page.getByRole('button', { name: 'Connect a scanner or hub' })).toBeVisible()
    await page.getByRole('button', { name: 'Print wall barcodes' }).click()
    const wallSheet = page.getByRole('dialog', { name: 'Wall barcodes' })
    await expect(wallSheet).toBeVisible()
    await expect(wallSheet.locator('.kit-print-root')).toBeVisible()
    await expect(wallSheet.getByRole('img', { name: 'Code 128 STOCK-OUT' })).toBeVisible()
    await expect(wallSheet.getByText('Print at Actual size (100%), not Fit to page.')).toHaveCount(1)
    await expect(wallSheet.getByRole('button', { name: 'Using a Symcode handheld?' })).toBeVisible()
    await expect(wallSheet.getByRole('img', { name: 'Code 128 %%SpecCode99' })).toHaveCount(0)
    await shot(page, 'wall-sheet-symcode-collapsed', width, false)
    await wallSheet.locator('.kit-print-root').evaluate((node) => node.setAttribute('data-printing', 'true'))
    await page.emulateMedia({ media: 'print' })
    if (shots) await page.locator('.wall-kit-print-root[data-printing="true"]').screenshot({ path: `${shots}/wall-print-${width}.png` })
    await page.emulateMedia({ media: 'screen' })
    await wallSheet.getByRole('button', { name: 'Close' }).click()
    const kitchen = page.getByRole('listitem').filter({ hasText: 'Kitchen hub' })
    await kitchen.getByRole('button', { name: 'Disconnect' }).click()
    const queuedDialog = page.getByRole('dialog', { name: 'Disconnect hub' })
    await expect(queuedDialog.getByText("Disconnect Kitchen hub? 3 scans it hasn't sent yet will be lost.")).toBeVisible()
    await expect(queuedDialog.getByRole('button', { name: 'Disconnect' })).toBeVisible()
    await expect(queuedDialog.getByRole('button', { name: 'Disconnect' })).toHaveClass(/household-danger/)
    await expect(queuedDialog.getByRole('button', { name: 'Cancel' })).toBeVisible()
    await shot(page, 'disconnect-n3', width, false)
    await queuedDialog.getByRole('button', { name: 'Cancel' }).click()
    await page.getByRole('listitem').filter({ hasText: 'Shed hub' }).getByRole('button', { name: 'Disconnect' }).click()
    const emptyDialog = page.getByRole('dialog', { name: 'Disconnect hub' })
    await expect(emptyDialog.getByText('Disconnect Shed hub?')).toBeVisible()
    await expect(emptyDialog.getByText(/scans it hasn't sent/)).toHaveCount(0)
    await shot(page, 'disconnect-n0', width, false)
    await emptyDialog.getByRole('button', { name: 'Disconnect' }).click()
    await expect(page.getByText('Shed hub was disconnected.')).toBeVisible()

    await page.getByRole('button', { name: 'Connect a scanner or hub' }).click()
    const hubPairing = page.getByRole('dialog', { name: 'Connect a scanner or hub' })
    await expect(hubPairing.locator('.pairing-screen-only').getByRole('img', { name: `Code 128 ${PAIRING}` })).toBeVisible()
    await expect(hubPairing.locator('.pairing-screen-only').getByRole('img', { name: `QR code ${PAIRING}` })).toHaveCount(0)
    await expect(page.getByRole('button', { name: 'Print setup & wall sheet' })).toBeVisible()
    await expect(page.getByText(/No printer\? Plug a USB keyboard/)).toBeVisible()
    await shot(page, 'pairing', width, false)
    await page.locator('.setup-kit-print-root').evaluate((node) => {
      node.removeAttribute('hidden')
      node.setAttribute('data-printing', 'true')
    })
    await page.emulateMedia({ media: 'print' })
    await expect(page.locator('.kit-print-root[data-printing="true"]')).toBeVisible()
    await expect(page.getByRole('img', { name: 'Code 128 STOCK-OUT' })).toBeVisible()
    await expect(page.getByRole('img', { name: 'Code 128 STOCK-IN' })).toBeVisible()
    await expect(page.getByRole('img', { name: 'Code 128 %%SpecCode99' })).toHaveCount(0)
    if (shots) await page.locator('.setup-kit-print-root').screenshot({ path: `${shots}/pairing-print-${width}.png` })
    await page.emulateMedia({ media: 'screen' })
    await page.locator('.pairing-dialog-body').getByRole('button', { name: 'Close' }).click()

    stage.pairingExpired = true
    await page.getByRole('button', { name: 'Connect a scanner or hub' }).click()
    await expect(page.getByText('This code expired. Get a new one.')).toBeVisible()
    await shot(page, 'pairing-expired', width, false)

    stage.role = 'member'
    stage.kind = 'account'
    await page.goto('/household')
    await expect(page.getByText('bea@example.com')).toBeVisible()
    await expect(page.getByRole('button', { name: 'Export household' })).toHaveCount(0)
    await expect(page.getByRole('button', { name: 'Make owner' })).toHaveCount(0)
  })
})
}

test('owner renames household and operator sees admin households list', async ({ page }) => {
  resetStage()
  await page.setViewportSize({ width: 1280, height: 900 })
  await install(page)
  await page.goto('/')
  await page.getByLabel('Email').fill('ada@example.com')
  await page.getByRole('textbox', { name: 'Password' }).fill('correct-horse-battery')
  await page.getByRole('button', { name: 'Sign in' }).click()

  await page.getByRole('button', { name: 'Menu' }).click()
  await page.getByRole('menuitem', { name: 'Household', exact: true }).click()
  await page.getByRole('textbox', { name: 'Household name', exact: true }).fill('Cupboard')
  await page.getByRole('button', { name: 'Save name' }).click()
  await expect(page.getByText('Household name saved.')).toBeVisible()
  await expect(page.getByRole('heading', { name: 'Cupboard' })).toBeVisible()

  await page.getByRole('button', { name: 'Menu' }).click()
  await page.getByRole('menuitem', { name: 'Other households' }).click()
  await expect(page.getByTestId('operator-households-section')).toHaveCount(0)

  stage.operator = true
  await page.reload()
  await expect(page.getByTestId('operator-households-section')).toBeVisible()
  await expect(page.getByRole('heading', { name: 'All households (Admin)' })).toBeVisible()
  await expect(page.getByText('Test Household 2')).toBeVisible()
  await expect(page.getByText(/Scanners: 0 online/)).toBeVisible()
  await expect(page.getByText(/0 scans this week/)).toBeVisible()
  await expect(page.getByText('dee@example.com')).toHaveCount(0)
})

test('shows contract admin-only copy when operator households access is denied', async ({ page }) => {
  resetStage()
  stage.operator = true
  stage.operatorHouseholdsDenied = true
  await page.setViewportSize({ width: 1280, height: 900 })
  await install(page)
  await page.goto('/')
  await page.getByLabel('Email').fill('ada@example.com')
  await page.getByRole('textbox', { name: 'Password' }).fill('correct-horse-battery')
  await page.getByRole('button', { name: 'Sign in' }).click()
  await page.getByRole('button', { name: 'Menu' }).click()
  const denied = page.waitForResponse((r) => new URL(r.url()).pathname === '/api/operator/households' && r.status() === 403)
  await page.getByRole('menuitem', { name: 'Other households' }).click()
  // Pin the fixture body to the contract sentence too; the UI maps by code, so the body is otherwise unobserved.
  expect(await (await denied).json()).toEqual({ error: 'Only an admin can do this.', code: 'forbidden_operator' })
  const section = page.getByTestId('operator-households-section')
  await expect(section).toBeVisible()
  await expect(section.getByRole('alert')).toHaveText('Only an admin can do this.')
  await expect(section).not.toContainText(/operator/i)
})
