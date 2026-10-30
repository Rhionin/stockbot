import { type Page, type Route } from '@playwright/test'
import { formatPairingCode, hostedErrorSentence } from '../../shared/contract'
import { PAIRING_ON_SCREEN_TOO_SMALL_HINT } from '../src/components/household/kitCopy456'

export const PAIRING = formatPairingCode('23456789')
export { PAIRING_ON_SCREEN_TOO_SMALL_HINT }

export function countPdfPages(buffer: Buffer): number {
  const matches = buffer.toString('latin1').match(/\/Type\s*\/Page\b/g)
  return matches?.length ?? 0
}

/** Extract visible text from a PDF buffer (Chrome embeds literal strings). */
export function pdfExtractText(buffer: Buffer): string {
  const raw = buffer.toString('latin1')
  const chunks: string[] = []
  const re = /\(([^)\\]*(?:\\.[^)\\]*)*)\)/g
  let match = re.exec(raw)
  while (match !== null) {
    chunks.push(match[1].replace(/\\([\\()])/g, '$1'))
    match = re.exec(raw)
  }
  return chunks.join(' ')
}

export function pdfContainsText(buffer: Buffer, needle: string): boolean {
  if (buffer.toString('latin1').includes(needle)) return true
  return pdfExtractText(buffer).includes(needle)
}

export interface Stage {
  kind: 'signed_out' | 'account'
  role: 'owner' | 'member'
  operator: boolean
  operatorHouseholdsDenied: boolean
  householdName: string
  olderAppDevices: number
  invites: { id: string; status: 'open' | 'revoked'; role: 'member' | 'owner' }[]
  deletedAt: string | null
  pairingExpired: boolean
}

export const stage: Stage = {
  kind: 'signed_out',
  role: 'owner',
  operator: false,
  operatorHouseholdsDenied: false,
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
  {
    id: 'scan-storage',
    label: 'Storage room',
    createdAt: '2026-04-01T00:00:00Z',
    lastSeenAt: '2026-04-02T15:00:00Z',
    revokedAt: null,
    kind: 'esp32_scanner',
    online: true,
    inactivityRevertSeconds: 300,
    defaultDirection: 'stock_out',
  },
])

const fulfillJson = (route: Route, body: unknown, status = 200) => route.fulfill({
  status,
  contentType: 'application/json',
  body: JSON.stringify(body),
})

export const resetStage = () => {
  stage.kind = 'signed_out'
  stage.role = 'owner'
  stage.operator = false
  stage.operatorHouseholdsDenied = false
  stage.householdName = 'Pantry'
  stage.olderAppDevices = 3
  stage.invites = [{ id: 'inv-1', status: 'open', role: 'member' }]
  stage.deletedAt = null
  stage.pairingExpired = false
}

export const install = async (page: Page) => {
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
      if (!stage.operator || stage.operatorHouseholdsDenied) {
        return fulfillJson(route, {
          error: hostedErrorSentence('forbidden_operator'),
          code: 'forbidden_operator',
        }, 403)
      }
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

export async function signInAndOpenHouseholdScannerPairing(page: Page) {
  await page.goto('/')
  await page.getByLabel('Email').fill('ada@example.com')
  await page.getByRole('textbox', { name: 'Password' }).fill('correct-horse-battery')
  await page.getByRole('button', { name: 'Sign in' }).click()
  await page.getByRole('button', { name: 'Menu' }).click()
  await page.getByRole('menuitem', { name: 'Household', exact: true }).click()
  await page.getByRole('button', { name: 'Connect a scanner' }).first().click()
}

export async function stubWindowPrint(page: Page) {
  await page.evaluate(() => {
    window.print = () => undefined
  })
}
