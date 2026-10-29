import { fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { MantineProvider } from '@mantine/core';
import { Notifications } from '@mantine/notifications';
import { MemoryRouter } from 'react-router-dom';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import {
  CONNECT_SCANNER_OR_HUB_LABEL,
  PRINT_ACTUAL_SIZE_INSTRUCTION,
  PRINT_PAIRING_CODE_LABEL,
  SYMCODE_DISCLOSURE_LABEL,
  formatPairingCode,
  hostedErrorSentence,
  pairingScannerScreenFallback,
  type AuthSession,
  type Device,
} from '@stockbot/shared/contract';
import { resetStage1Client } from '../../api/client';
import { HouseholdSessionContext } from '../auth/householdSession';
import { SetupPage } from '../auth/SetupPage';
import { DevicesPage } from './DevicesPage';
import { HouseholdPage } from './HouseholdPage';
import { HouseholdsPage } from './HouseholdsPage';
import { WallBarcodeSheetModal } from './WallBarcodeSheetModal';

const PAIRING = formatPairingCode('23456789');

const json = (body: unknown, status = 200) =>
  new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } });

function requestUrl(input: RequestInfo | URL): string {
  if (typeof input === 'string') return input;
  if (input instanceof URL) return input.href;
  return input.url;
}

function session(role: 'owner' | 'member', operator = false): AuthSession {
  return {
    user: { id: 'user-1', email: 'ada@example.com', name: 'Ada' },
    kind: 'account',
    memberships: [{ householdId: 'hh-1', name: 'Test Household', role, deletedAt: null }],
    households: [{ id: 'hh-1', name: 'Test Household', role, deletedAt: null }],
    activeHouseholdId: 'hh-1',
    operator,
  };
}

const scannerDevice = (overrides: Partial<Device> = {}): Device => ({
  id: 'scan-1',
  label: '',
  createdAt: '2026-04-01T00:00:00Z',
  lastSeenAt: '2026-04-02T00:00:00Z',
  revokedAt: null,
  kind: 'esp32_scanner',
  online: true,
  inactivityRevertSeconds: 180,
  defaultDirection: 'stock_out',
  ...overrides,
});

function renderStage(ui: React.ReactNode, auth: AuthSession) {
  return render(
    <MantineProvider env="test">
      <Notifications />
      <MemoryRouter>
        <HouseholdSessionContext.Provider value={{ required: true, logout: vi.fn(), auth, replaceAuth: vi.fn() }}>
          {ui}
        </HouseholdSessionContext.Provider>
      </MemoryRouter>
    </MantineProvider>,
  );
}

function assertNoOperatorInVisibleCopy() {
  expect(document.body.textContent?.toLowerCase()).not.toMatch(/\boperator\b/);
}

describe('issue #456 kit copy (table B)', () => {
  beforeEach(() => {
    vi.restoreAllMocks();
    resetStage1Client();
  });

  it('B12: SetupPage lede says admin account', () => {
    render(
      <MantineProvider env="test">
        <MemoryRouter>
          <SetupPage />
        </MemoryRouter>
      </MantineProvider>,
    );
    expect(screen.getByText('Enter the setup code from the server, then create the admin account.')).toBeVisible();
    assertNoOperatorInVisibleCopy();
  });

  it('B13: admin invite-code description on HouseholdsPage', async () => {
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL) => {
      const url = requestUrl(input);
      if (url.includes('/api/operator/households')) return Promise.resolve(json({ households: [] }));
      return Promise.reject(new Error(url));
    }));
    renderStage(<HouseholdsPage />, session('owner', true));
    expect(await screen.findByText(
      'Optional for admins. Leave it blank to create a household directly, or paste an invite code or link.',
    )).toBeVisible();
    assertNoOperatorInVisibleCopy();
  });

  it('B14: forbidden_operator hosted sentence', () => {
    expect(hostedErrorSentence('forbidden_operator')).toBe('Only an admin can do this.');
  });

  it('B1/B3/B5/B16–B18: household pairing dialog and combined kit print root', async () => {
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const url = requestUrl(input);
      if (url.includes('/members')) return Promise.resolve(json({ members: [] }));
      if (url === '/api/invites') return Promise.resolve(json({ invites: [] }));
      if (url === '/api/household/settings') return Promise.resolve(json({ restockMode: 'auto' }));
      if (url === '/api/households/hh-1') {
        return Promise.resolve(json({ id: 'hh-1', name: 'Test Household', role: 'owner', deletedAt: null, purgeAt: null }));
      }
      if (url.includes('/api/devices') && init?.method !== 'POST' && init?.method !== 'PATCH' && init?.method !== 'DELETE') {
        return Promise.resolve(json({ devices: [] }));
      }
      if (url.includes('/api/devices/pair/pair-1')) {
        return Promise.resolve(json({ pairingId: 'pair-1', status: 'pending', expiresAt: '2099-06-15T14:05:00.000Z' }));
      }
      if (url.includes('/api/devices/pair')) {
        return Promise.resolve(json({
          code: PAIRING,
          qrPayload: PAIRING,
          expiresAt: '2099-06-15T14:05:00.000Z',
          pairingId: 'pair-1',
          barcodeValue: PAIRING,
          qrValue: PAIRING,
        }));
      }
      return Promise.reject(new Error(url));
    }));
    renderStage(<HouseholdPage />, session('owner'));
    const scannersHeading = await screen.findByRole('heading', { name: 'Scanners' });
    const scannersSection = scannersHeading.closest('section')!;
    fireEvent.click(within(scannersSection).getAllByRole('button', { name: 'Connect a scanner' })[0]);
    const dialog = await screen.findByRole('dialog', { name: 'Connect a scanner' });
    await waitFor(() => {
      const screenOnly = dialog.querySelector('.pairing-screen-only');
      expect(screenOnly).not.toBeNull();
      expect(within(screenOnly as HTMLElement).getByText(/Scan the pairing barcode from Stockbot/)).toBeVisible();
    });
    const screenOnly = dialog.querySelector('.pairing-screen-only') as HTMLElement;
    expect(within(screenOnly).getByText(pairingScannerScreenFallback())).toBeVisible();
    expect(within(dialog).queryByText(/USB keyboard/)).not.toBeInTheDocument();
    expect(within(dialog).getByRole('button', { name: PRINT_PAIRING_CODE_LABEL })).toBeVisible();
    const printRoot = dialog.querySelector('.kit-print-root')!;
    expect(printRoot).not.toBeNull();
    expect(within(printRoot as HTMLElement).getByText('Connect your scanner')).toBeVisible();
    expect(within(printRoot as HTMLElement).getByRole('img', { name: `Code 128 ${PAIRING}` })).toBeVisible();
    expect(within(printRoot as HTMLElement).getByRole('img', { name: 'Code 128 STOCK-OUT' })).toBeVisible();
    expect(within(printRoot as HTMLElement).getByRole('img', { name: 'Code 128 STOCK-IN' })).toBeVisible();
    expect(printRoot.textContent).not.toMatch(/%%SpecCode99|Setup B/);
    expect(within(dialog).getAllByText(PRINT_ACTUAL_SIZE_INSTRUCTION)).toHaveLength(1);
    assertNoOperatorInVisibleCopy();
  });

  it('B6: devices header uses Connect a scanner or hub', async () => {
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL) => {
      if (requestUrl(input).includes('/api/devices')) return Promise.resolve(json({ devices: [] }));
      return Promise.reject(new Error(requestUrl(input)));
    }));
    renderStage(<DevicesPage />, session('owner'));
    expect(await screen.findByRole('button', { name: CONNECT_SCANNER_OR_HUB_LABEL })).toBeVisible();
    assertNoOperatorInVisibleCopy();
  });

  it('B8/B9: esp32 scanner rename and disconnect modals avoid hub wording', async () => {
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL) => {
      if (requestUrl(input).includes('/api/devices')) {
        return Promise.resolve(json({ devices: [scannerDevice({ label: 'Pantry pistol', queued: 2 })] }));
      }
      return Promise.reject(new Error(requestUrl(input)));
    }));
    renderStage(<DevicesPage />, session('owner'));
    expect(await screen.findByText('Pantry pistol')).toBeVisible();
    fireEvent.click(screen.getByRole('button', { name: 'Rename' }));
    expect(await screen.findByRole('dialog', { name: 'Rename scanner' })).toBeVisible();
    fireEvent.click(screen.getByRole('button', { name: 'Cancel' }));
    fireEvent.click(screen.getByRole('button', { name: 'Disconnect' }));
    const dialog = await screen.findByRole('dialog', { name: 'Disconnect scanner' });
    expect(within(dialog).getByText("Disconnect Pantry pistol? 2 scans it hasn't sent yet will be lost.")).toBeVisible();
    expect(dialog.textContent?.match(/\b[Hh]ub\b/)).toBeNull();
    assertNoOperatorInVisibleCopy();
  });

  it('B10/B19: wall sheet uses scanner settings and Wall barcodes / Print labels', async () => {
    render(
      <MantineProvider env="test">
        <WallBarcodeSheetModal
          opened
          onClose={() => undefined}
          devices={[scannerDevice()]}
        />
      </MantineProvider>,
    );
    expect(await screen.findByRole('dialog', { name: 'Wall barcodes' })).toBeVisible();
    expect(screen.getByRole('button', { name: 'Print' })).toBeVisible();
    const printRoot = screen.getByRole('dialog').querySelector('.kit-print-root')!;
    expect(within(printRoot as HTMLElement).getByText(/Goes back to Stock out 3 minutes/)).toBeVisible();
    assertNoOperatorInVisibleCopy();
  });

  it('B11: Symcode barcode stays behind disclosure until expanded', async () => {
    render(
      <MantineProvider env="test">
        <WallBarcodeSheetModal opened onClose={() => undefined} devices={[]} />
      </MantineProvider>,
    );
    expect(screen.queryByRole('img', { name: 'Code 128 %%SpecCode99' })).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: SYMCODE_DISCLOSURE_LABEL }));
    expect(await screen.findByRole('img', { name: 'Code 128 %%SpecCode99' })).toBeVisible();
    assertNoOperatorInVisibleCopy();
  });

  it('B20: scanners empty state copy and inline Connect button', async () => {
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL) => {
      const url = requestUrl(input);
      if (url.includes('/members')) return Promise.resolve(json({ members: [] }));
      if (url === '/api/invites') return Promise.resolve(json({ invites: [] }));
      if (url === '/api/household/settings') return Promise.resolve(json({ restockMode: 'auto' }));
      if (url === '/api/households/hh-1') {
        return Promise.resolve(json({ id: 'hh-1', name: 'Test Household', role: 'owner', deletedAt: null, purgeAt: null }));
      }
      if (url.includes('/api/devices')) return Promise.resolve(json({ devices: [] }));
      return Promise.reject(new Error(url));
    }));
    renderStage(<HouseholdPage />, session('owner'));
    expect(await screen.findByText('No scanners yet. Connect one to scan items in and out right at the shelf.')).toBeVisible();
    const emptyConnect = screen.getAllByRole('button', { name: 'Connect a scanner' });
    expect(emptyConnect.length).toBeGreaterThanOrEqual(2);
    assertNoOperatorInVisibleCopy();
  });

  it('household scanners section avoids operator in visible copy', async () => {
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL) => {
      const url = requestUrl(input);
      if (url.includes('/members')) return Promise.resolve(json({ members: [] }));
      if (url === '/api/invites') return Promise.resolve(json({ invites: [] }));
      if (url === '/api/household/settings') return Promise.resolve(json({ restockMode: 'auto' }));
      if (url === '/api/households/hh-1') {
        return Promise.resolve(json({ id: 'hh-1', name: 'Test Household', role: 'owner', deletedAt: null, purgeAt: null }));
      }
      if (url.includes('/api/devices')) return Promise.resolve(json({ devices: [scannerDevice({ label: 'A' })] }));
      if (url.includes('/api/devices/pair/pair-1')) {
        return Promise.resolve(json({ pairingId: 'pair-1', status: 'pending', expiresAt: '2099-01-01T00:00:00Z' }));
      }
      if (url.includes('/api/devices/pair')) {
        return Promise.resolve(json({
          code: PAIRING,
          qrPayload: PAIRING,
          expiresAt: '2099-01-01T00:00:00Z',
          pairingId: 'pair-1',
          barcodeValue: PAIRING,
          qrValue: PAIRING,
        }));
      }
      return Promise.reject(new Error(url));
    }));
    renderStage(<HouseholdPage />, session('owner'));
    expect(await screen.findByRole('heading', { name: 'Scanners' })).toBeVisible();
    assertNoOperatorInVisibleCopy();
    const scannersSection = screen.getByRole('heading', { name: 'Scanners' }).closest('section')!;
    fireEvent.click(within(scannersSection).getAllByRole('button', { name: 'Connect a scanner' })[0]);
    const pairingDialog = await screen.findByRole('dialog', { name: 'Connect a scanner' });
    await waitFor(() => {
      const screenOnly = pairingDialog.querySelector('.pairing-screen-only');
      expect(screenOnly).not.toBeNull();
      expect(within(screenOnly as HTMLElement).getByText(/Scan the pairing barcode from Stockbot/)).toBeVisible();
    });
    assertNoOperatorInVisibleCopy();
    fireEvent.click(within(pairingDialog).getByRole('button', { name: 'Close' }));
    fireEvent.click(within(scannersSection).getByRole('button', { name: 'Print wall barcodes' }));
    await waitFor(() => expect(screen.getByRole('dialog', { name: 'Wall barcodes' })).toBeVisible());
    assertNoOperatorInVisibleCopy();
  });
});
