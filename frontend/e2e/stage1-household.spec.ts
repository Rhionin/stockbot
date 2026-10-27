import { expect, test, type Page, type Route } from '@playwright/test'
import { existsSync, mkdirSync } from 'node:fs'
import { formatPairingCode } from '../../shared/contract'

const PAIRING = formatPairingCode('23456789')
// GitHub Actions has no writable /cursor/stores/self. STAGE1_SCREENSHOT_DIR
// overrides the directory when a run should keep the images somewhere else.
const shots = process.env.STAGE1_SCREENSHOT_DIR
  || (existsSync('/cursor/stores/self') ? '/cursor/stores/self/383' : '')

interface Stage {
  kind: 'signed_out' | 'account'
  role: 'owner' | 'member'
  operator: boolean
  householdName: string
  olderAppDevices: number
  invites: { id: string; status: 'open' | 'revoked'; role: 'member' | 'owner' }[]
  deletedAt: string | null
  pairingExpired: boolean
}

const stage: Stage = {
  kind: 'signed_out',
  role: 'owner',
  operator: false,
  householdName: 'Pantry',
  olderAppDevices: 3,
  invites: [{ id: 'inv-1', status: 'open', role: 'member' }],
  deletedAt: null,
  pairingExpired: false,
}

const sessionBody = () => ({
  user: stage.kind === 'account' ? { id: 'user-1', email: 'ada@example.com', name: 'Ada' } : null,
  kind: stage.kind,
  memberships: [
    { householdId: 'hh-1', name: stage.householdName, role: stage.role, deletedAt: stage.deletedAt },
    { householdId: 'hh-2', name: 'Cabin', role: 'member', deletedAt: null },
  ],
  households: [
    { id: 'hh-1', name: stage.householdName, role: stage.role, deletedAt: stage.deletedAt },
    { id: 'hh-2', name: 'Cabin', role: 'member', deletedAt: null },
  ],
  activeHouseholdId: stage.kind === 'signed_out' ? null : 'hh-1',
  operator: stage.operator,
})

const devices = () => ([
  { id: 'hub', label: 'Kitchen hub', createdAt: '2026-04-01T00:00:00Z', lastSeenAt: '2026-04-02T15:00:00Z', revokedAt: null, kind: 'pi_hub', online: true, queued: 3 },
  { id: 'shed', label: 'Shed hub', createdAt: '2026-04-01T00:00:00Z', lastSeenAt: '2026-03-01T00:00:00Z', revokedAt: null, kind: 'box_hub', online: false, queued: 0 },
  { id: 'direct', label: 'Counter', createdAt: '2026-04-01T00:00:00Z', lastSeenAt: '2026-04-02T15:00:00Z', revokedAt: null, kind: 'direct', online: true },
])

const fulfillJson = (route: Route, body: unknown, status = 200) => route.fulfill({
  status,
  contentType: 'application/json',
  body: JSON.stringify(body),
})

const install = async (page: Page) => {
  await page.route('**/*', async (route) => {
    const url = new URL(route.request().url())
    const path = url.pathname
    const method = route.request().method()
    if (path === '/setup' && method !== 'POST') return route.continue()
    if (!path.startsWith('/api') && path !== '/setup') return route.continue()
    if (path === '/api/auth/session' && method === 'GET') return fulfillJson(route, sessionBody())
    if (path === '/api/auth/session/household' && method === 'POST') {
      const body = route.request().postDataJSON() as { householdId: string }
      return fulfillJson(route, { ...sessionBody(), activeHouseholdId: body.householdId })
    }
    if (path === '/api/auth/login' && method === 'POST') {
      stage.kind = 'account'
      stage.role = 'owner'
      return fulfillJson(route, sessionBody())
    }
    if (path === '/api/config') {
      return fulfillJson(route, {
        live: 'sse',
        minClientVersion: { expo: '1.0.0' },
        entry: 'public',
      })
    }
    if (path === '/api/providers' || path === '/api/group-suggestions' || path === '/api/scans' || path === '/api/inventory') {
      return fulfillJson(route, [])
    }
    if (path === '/api/build') return fulfillJson(route, {})
    if (path === '/api/scanner/config') {
      return fulfillJson(route, { stockInBarcode: 'STOCK_IN', stockOutBarcode: 'STOCK_OUT', currentMode: 'stock_in' })
    }
    if (path === '/api/events') {
      return route.fulfill({
        status: 200,
        contentType: 'text/event-stream',
        body: 'event: hint\ndata: {"type":"devices"}\n\n',
      })
    }
    if (path === '/api/household/settings' && method === 'GET') {
      return fulfillJson(route, { restockMode: 'auto' })
    }
    if (path === '/api/telemetry') return fulfillJson(route, { olderAppDevices: stage.olderAppDevices })
    if (path === '/api/households/hh-1/members') return fulfillJson(route, { members: [{ userId: 'user-2', email: 'bea@example.com', role: 'member' }] })
    if (path === '/api/invites' && method === 'GET') {
      return fulfillJson(route, {
        invites: stage.invites.map((invite) => ({
          id: invite.id,
          email: null,
          role: invite.role,
          createdBy: 'user-1',
          expiresAt: '2026-06-01T00:00:00Z',
          status: invite.status,
        })),
      })
    }
    if (path === '/api/invites' && method === 'POST') {
      stage.invites.push({ id: 'inv-2', status: 'open', role: 'member' })
      return fulfillJson(route, { id: 'inv-2', code: 'new-code', url: 'https://app.example/invite/new-code', email: null, role: 'member', expiresAt: '2026-06-01T00:00:00Z' })
    }
    if (path.endsWith('/revoke')) {
      stage.invites = stage.invites.map((invite) => invite.id === 'inv-1' ? { ...invite, status: 'revoked' } : invite)
      return fulfillJson(route, {})
    }
    if (path === '/api/operator/households' && method === 'GET') {
      if (!stage.operator) return fulfillJson(route, { error: 'Operator access required.', code: 'forbidden_operator' }, 403)
      return fulfillJson(route, {
        households: [
          {
            id: 'hh-1',
            name: stage.householdName,
            isPrimary: true,
            restockMode: 'auto',
            createdAt: '2026-04-01T00:00:00Z',
            members: [{ userId: 'user-1', email: 'ada@example.com', name: 'Ada', role: 'owner', createdAt: '2026-04-01T00:00:00Z' }],
            memberCount: 1,
            productCount: 4,
            itemsOnHand: 2,
            pendingScans: 0,
            lastActivityAt: '2026-04-02T00:00:00Z',
          },
          {
            id: 'hh-9',
            name: 'Loft',
            isPrimary: false,
            restockMode: 'auto',
            createdAt: '2026-04-01T00:00:00Z',
            members: [{ userId: 'user-9', email: 'dee@example.com', name: 'Dee', role: 'owner', createdAt: '2026-04-01T00:00:00Z' }],
            memberCount: 1,
            productCount: 1,
            itemsOnHand: 1,
            pendingScans: 1,
            lastActivityAt: null,
          },
        ],
      })
    }
    if (path === '/api/households/hh-1' && method === 'PATCH') {
      const body = route.request().postDataJSON() as { name?: string }
      stage.householdName = (body.name ?? stage.householdName).trim()
      return fulfillJson(route, { id: 'hh-1', name: stage.householdName, role: stage.role, deletedAt: stage.deletedAt, purgeAt: null })
    }
    if (path === '/api/households/hh-1' && method === 'GET') {
      return fulfillJson(route, { id: 'hh-1', name: stage.householdName, role: stage.role, deletedAt: stage.deletedAt, purgeAt: null })
    }
    if (path === '/api/households/hh-1' && method === 'DELETE') {
      stage.deletedAt = '2026-04-02T00:00:00Z'
      return fulfillJson(route, {
        id: 'hh-1',
        name: 'Pantry',
        role: 'owner',
        deletedAt: stage.deletedAt,
        purgeAt: '2026-05-02T00:00:00Z',
        message: 'Your household will be permanently deleted after 30 days. Backup copies are cleared within two weeks after that.',
      })
    }
    if (path === '/api/households/hh-1/restore') {
      stage.deletedAt = null
      return fulfillJson(route, { id: 'hh-1', name: 'Pantry', role: 'owner', deletedAt: null, purgeAt: null })
    }
    if (path === '/api/export' && method === 'POST') return fulfillJson(route, { jobId: 'job-1', status: 'ready' })
    if (path === '/api/export/job-1') {
      return fulfillJson(route, { jobId: 'job-1', status: 'ready', jsonUrl: '/files/pantry.json', csvUrl: '/files/pantry.csv', expiresAt: null })
    }
    if (path === '/api/me' && method === 'DELETE') return fulfillJson(route, { error: 'blocked', code: 'last_owner' }, 409)
    if (path === '/api/devices' && method === 'GET') return fulfillJson(route, { devices: devices() })
    if (path === '/api/devices/pair' && method === 'POST') {
      return fulfillJson(route, {
        code: PAIRING,
        qrPayload: PAIRING,
        expiresAt: stage.pairingExpired ? '2000-01-01T00:00:00Z' : '2099-01-01T00:00:00Z',
        pairingId: 'pair-1',
        barcodeValue: PAIRING,
        qrValue: PAIRING,
      })
    }
    if (path === '/api/devices/pair/pair-1') {
      return fulfillJson(route, { pairingId: 'pair-1', status: stage.pairingExpired ? 'expired' : 'pending', expiresAt: '2099-01-01T00:00:00Z' })
    }
    if (path === '/api/devices/shed' && method === 'DELETE') return fulfillJson(route, {})
    if (path === '/api/devices/hub' && method === 'PATCH') return fulfillJson(route, devices()[0])
    return fulfillJson(route, {})
  })
}

const resetStage = () => {
  stage.kind = 'signed_out'
  stage.role = 'owner'
  stage.operator = false
  stage.householdName = 'Pantry'
  stage.olderAppDevices = 3
  stage.invites = [{ id: 'inv-1', status: 'open', role: 'member' }]
  stage.deletedAt = null
  stage.pairingExpired = false
}

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
    await expect(page.getByRole('combobox', { name: 'Household' })).toBeVisible()
    await shot(page, 'switcher', width)
    await expect(page.getByRole('button', { name: 'Make owner' })).toBeVisible()
    await page.getByRole('button', { name: 'Create invite' }).click()
    await expect(page.getByText(/Invite link:/)).toBeVisible()
    await page.getByRole('button', { name: 'Revoke invite' }).first().click()
    await expect(page.getByText('Cancelled')).toBeVisible()
    await shot(page, 'members-invites', width)

    await page.getByRole('button', { name: 'Menu' }).click()
    await page.getByRole('menuitem', { name: 'Other households' }).click()
    await expect(page.getByRole('heading', { name: 'Households' })).toBeVisible()
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

    await page.getByRole('button', { name: 'Pair a hub' }).click()
    await expect(page.getByRole('img', { name: `Code 128 ${PAIRING}` })).toBeVisible()
    await expect(page.getByRole('img', { name: `QR code ${PAIRING}` })).toBeVisible()
    await expect(page.getByRole('button', { name: 'Print pairing code' })).toBeVisible()
    await expect(page.getByText(/No printer\? Plug a USB keyboard/)).toBeVisible()
    await shot(page, 'pairing', width, false)
    await page.emulateMedia({ media: 'print' })
    await expect(page.locator('.pairing-sheet')).toBeVisible()
    if (shots) await page.locator('.pairing-sheet').screenshot({ path: `${shots}/pairing-print-${width}.png` })
    await page.emulateMedia({ media: 'screen' })
    await page.locator('.pairing-sheet').getByRole('button', { name: 'Close' }).click()

    stage.pairingExpired = true
    await page.getByRole('button', { name: 'Pair a hub' }).click()
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
  await expect(page.getByText('Loft')).toBeVisible()
  await expect(page.getByText('dee@example.com')).toBeVisible()
})
