import { fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { MantineProvider } from '@mantine/core';
import { Notifications } from '@mantine/notifications';
import { MemoryRouter, useLocation } from 'react-router-dom';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import {
  CHANGEOVER_BANNER,
  HOUSEHOLD_DELETE_SENTENCE,
  MAKE_MY_ACCOUNT,
  OFFLINE_SENTENCE,
  PAIRING_EXPIRED_SENTENCE,
  PRINT_PAIRING_CODE_LABEL,
  RESTORE_HOUSEHOLD_PASSWORD,
  RETIRE_HOUSEHOLD_PASSWORD,
  UPDATING_SENTENCE,
  applyHttpResponse,
  formatPairingCode,
  serverNoticeText,
  type AuthSession,
  type Device,
} from '@stockbot/shared/contract';
import { getSession, resetStage1Client } from '../../api/client';
import { HouseholdSessionContext } from '../auth/householdSession';
import { ServerNoticeBanner } from '../shell/ServerNoticeBanner';
import { AppMenu } from '../shell/AppMenu';
import { ScanQueuePage } from '../queue/ScanQueuePage';
import { ChangeoverBanner } from './ChangeoverBanner';
import { code128Bars } from './code128';
import { DevicesPage } from './DevicesPage';
import { HouseholdPage } from './HouseholdPage';
import { HouseholdSwitcher } from './HouseholdSwitcher';
import { useLiveHint } from './liveHints';

const PAIRING = formatPairingCode('23456789');

const json = (body: unknown, status = 200) =>
  new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } });

class FakeEventSource {
  static instances: FakeEventSource[] = [];
  onerror: ((event: Event) => void) | null = null;
  closed = false;
  readonly url: string;
  private readonly listeners = new Map<string, Array<(event: MessageEvent) => void>>();

  constructor(url: string) {
    this.url = url;
    FakeEventSource.instances.push(this);
  }

  addEventListener(type: string, listener: (event: MessageEvent) => void) {
    const current = this.listeners.get(type) ?? [];
    current.push(listener);
    this.listeners.set(type, current);
  }

  close() {
    this.closed = true;
  }

  dispatch(type: string, payload: unknown) {
    for (const listener of this.listeners.get(type) ?? []) {
      listener({ data: JSON.stringify(payload) } as MessageEvent);
    }
  }
}

function session(role: 'owner' | 'member', kind: AuthSession['kind'] = 'account'): AuthSession {
  return {
    user: kind === 'account' ? { id: 'user-1', email: 'ada@example.com', name: 'Ada' } : null,
    kind,
    memberships: [
      { householdId: 'hh-1', name: 'Pantry', role, deletedAt: null },
      { householdId: 'hh-2', name: 'Cabin', role: 'member', deletedAt: null },
    ],
    households: [
      { id: 'hh-1', name: 'Pantry', role, deletedAt: null },
      { id: 'hh-2', name: 'Cabin', role: 'member', deletedAt: null },
    ],
    activeHouseholdId: 'hh-1',
    operator: false,
  };
}

const device = (overrides: Partial<Device>): Device => ({
  id: 'dev-1',
  label: 'Kitchen hub',
  createdAt: '2026-04-01T00:00:00Z',
  lastSeenAt: '2026-04-02T00:00:00Z',
  revokedAt: null,
  kind: 'pi_hub',
  online: true,
  ...overrides,
});

function renderStage(ui: React.ReactNode, auth: AuthSession, replaceAuth = vi.fn()) {
  return render(
    <MantineProvider env="test">
      <Notifications />
      <MemoryRouter>
        <HouseholdSessionContext.Provider value={{ required: true, logout: vi.fn(), auth, replaceAuth }}>
          {ui}
        </HouseholdSessionContext.Provider>
      </MemoryRouter>
    </MantineProvider>,
  );
}

describe('changeover and household management', () => {
  beforeEach(() => {
    FakeEventSource.instances = [];
    vi.stubGlobal('EventSource', FakeEventSource);
    resetStage1Client();
  });

  it('shows the changeover banner and opens make-my-account from an invite code', async () => {
    const Location = () => {
      const { pathname } = useLocation();
      return <p>path {pathname}</p>;
    };
    renderStage(<><ChangeoverBanner /><Location /></>, session('member', 'household_password'));
    expect(screen.getByRole('status')).toHaveTextContent(CHANGEOVER_BANNER);
    fireEvent.click(screen.getByRole('button', { name: MAKE_MY_ACCOUNT }));
    fireEvent.change(screen.getByLabelText('Invite code'), { target: { value: 'invite-code' } });
    fireEvent.click(screen.getByRole('button', { name: 'Continue' }));
    expect(await screen.findByText('path /invite/invite-code')).toBeVisible();
  });

  it('warns about older apps only when some opened Stockbot in the last 7 days', async () => {
    const retireBodies: string[] = [];
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url === '/api/providers' || url === '/api/group-suggestions') return Promise.resolve(json([]));
      if (url === '/api/build') return Promise.resolve(json({}));
      if (url === '/api/household/settings') {
        if (init?.method === 'POST' && url.endsWith('/retire')) {
          retireBodies.push('retire');
        }
        return Promise.resolve(json({ restockMode: 'auto', householdPasswordEnabled: true }));
      }
      if (url.endsWith('/household-password/retire')) {
        retireBodies.push(url);
        return Promise.resolve(json({
          restockMode: 'auto',
          householdPasswordEnabled: false,
          householdPasswordRestoreUntil: '2026-05-01T00:00:00Z',
        }));
      }
      if (url.includes('/members')) {
        return Promise.resolve(json({ members: [{ userId: 'user-2', email: 'bea@example.com', role: 'member' }] }));
      }
      if (url === '/api/telemetry') return Promise.resolve(json({ olderAppDevices: 2 }));
      return Promise.reject(new Error(url));
    }));
    const first = renderStage(<AppMenu onCredentialsChanged={vi.fn()} />, session('owner'));
    fireEvent.click(screen.getByRole('button', { name: 'Menu' }));
    await screen.findByRole('menuitem', { name: RETIRE_HOUSEHOLD_PASSWORD });
    fireEvent.click(screen.getByRole('menuitem', { name: RETIRE_HOUSEHOLD_PASSWORD }));
    expect(await screen.findByText(/2 devices opened Stockbot with an older app version/)).toBeVisible();
    expect(screen.getByText('bea@example.com')).toBeVisible();
    fireEvent.click(screen.getByRole('button', { name: 'Cancel' }));
    first.unmount();

    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL) => {
      const url = String(input);
      if (url === '/api/providers' || url === '/api/group-suggestions') return Promise.resolve(json([]));
      if (url === '/api/build') return Promise.resolve(json({}));
      if (url === '/api/household/settings') return Promise.resolve(json({ restockMode: 'auto', householdPasswordEnabled: true }));
      if (url.includes('/members')) return Promise.resolve(json({ members: [] }));
      if (url === '/api/telemetry') return Promise.resolve(json({ olderAppDevices: 0 }));
      if (url.endsWith('/household-password/retire')) {
        return Promise.resolve(json({ restockMode: 'auto', householdPasswordEnabled: false }));
      }
      return Promise.reject(new Error(url));
    }));
    renderStage(<AppMenu onCredentialsChanged={vi.fn()} />, session('owner'));
    fireEvent.click(screen.getByRole('button', { name: 'Menu' }));
    await screen.findByRole('menuitem', { name: RETIRE_HOUSEHOLD_PASSWORD });
    fireEvent.click(screen.getByRole('menuitem', { name: RETIRE_HOUSEHOLD_PASSWORD }));
    expect(screen.queryByText(/older app version/)).not.toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Turn off' })).toHaveClass('household-danger');
    fireEvent.click(screen.getByRole('button', { name: 'Turn off' }));
    await waitFor(() => expect(screen.queryByRole('button', { name: 'Turn off' })).not.toBeInTheDocument());
  });

  it('restores the household password from the menu', async () => {
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url === '/api/providers' || url === '/api/group-suggestions') return Promise.resolve(json([]));
      if (url === '/api/build') return Promise.resolve(json({}));
      if (url.endsWith('/household-password/restore') && init?.method === 'POST') {
        return Promise.resolve(json({ restockMode: 'auto', householdPasswordEnabled: true }));
      }
      if (url === '/api/household/settings') {
        return Promise.resolve(json({
          restockMode: 'ask_restock',
          householdPasswordEnabled: false,
          householdPasswordRestoreUntil: '2026-05-01T12:00:00Z',
        }));
      }
      if (url.includes('/members')) return Promise.resolve(json({ members: [] }));
      if (url === '/api/telemetry') return Promise.resolve(json({ olderAppDevices: 0 }));
      return Promise.reject(new Error(url));
    }));
    renderStage(<AppMenu onCredentialsChanged={vi.fn()} />, session('owner'));
    fireEvent.click(screen.getByRole('button', { name: 'Menu' }));
    await screen.findByRole('menuitem', { name: RESTORE_HOUSEHOLD_PASSWORD });
    fireEvent.click(screen.getByRole('menuitem', { name: RESTORE_HOUSEHOLD_PASSWORD }));
    expect(screen.getByText(/You can turn it back on until/)).toBeVisible();
    fireEvent.click(screen.getByRole('button', { name: 'Turn back on' }));
    await waitFor(() => expect(screen.queryByRole('button', { name: 'Turn back on' })).not.toBeInTheDocument());
  });

  it('hides owner controls from a member and lets an owner manage people and invites', async () => {
    const calls: string[] = [];
    let invites = [
      { id: 'inv-1', email: null, role: 'member', createdBy: 'user-1', expiresAt: '2026-05-01T00:00:00Z', status: 'open' },
    ];
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      calls.push(`${init?.method ?? 'GET'} ${url}`);
      if (url.includes('/members') && (init?.method === undefined)) {
        return Promise.resolve(json({ members: [
          { userId: 'user-1', email: 'ada@example.com', role: 'owner' },
          { userId: 'user-2', email: 'bea@example.com', role: 'member' },
        ] }));
      }
      if (url === '/api/invites' && init?.method === 'POST') {
        invites = [...invites, { id: 'inv-2', email: null, role: 'member', createdBy: 'user-1', expiresAt: '2026-06-01T00:00:00Z', status: 'open' }];
        return Promise.resolve(json({ id: 'inv-2', code: 'code', url: 'https://app.example/invite/code', email: null, role: 'member', expiresAt: '2026-06-01T00:00:00Z' }));
      }
      if (url.includes('/revoke')) {
        invites = invites.map((invite) => invite.id === 'inv-1' ? { ...invite, status: 'revoked' } : invite);
        return Promise.resolve(json({}));
      }
      if (url === '/api/invites') return Promise.resolve(json({ invites }));
      if (url === '/api/household/settings') return Promise.resolve(json({ restockMode: 'auto', householdPasswordEnabled: false }));
      if (url === '/api/households/hh-1') return Promise.resolve(json({ id: 'hh-1', name: 'Pantry', role: 'member', deletedAt: null, purgeAt: null }));
      return Promise.reject(new Error(url));
    }));
    const { unmount } = renderStage(<HouseholdPage />, session('member'));
    expect(await screen.findByText('bea@example.com')).toBeVisible();
    expect(screen.queryByRole('button', { name: 'Make owner' })).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Export household' })).not.toBeInTheDocument();
    expect(screen.queryByText(HOUSEHOLD_DELETE_SENTENCE)).not.toBeInTheDocument();
    expect(screen.queryByRole('radio', { name: 'Owner' })).not.toBeInTheDocument();
    unmount();

    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url.includes('/members') && init?.method === undefined) {
        return Promise.resolve(json({ members: [{ userId: 'user-2', email: 'bea@example.com', role: 'member' }] }));
      }
      if (url.includes('/members/') && init?.method === 'PATCH') return Promise.resolve(json({ userId: 'user-2', email: 'bea@example.com', role: 'owner' }));
      if (url === '/api/invites' && init?.method === 'POST') {
        return Promise.resolve(json({ id: 'inv-2', code: 'code', url: 'https://app.example/invite/code', email: null, role: 'owner', expiresAt: '2026-06-01T00:00:00Z' }));
      }
      if (url.includes('/revoke')) {
        invites = [{ id: 'inv-1', email: null, role: 'member', createdBy: 'user-1', expiresAt: '2026-05-01T00:00:00Z', status: 'revoked' }];
        return Promise.resolve(json({}));
      }
      if (url === '/api/invites') return Promise.resolve(json({ invites }));
      if (url === '/api/household/settings') return Promise.resolve(json({ restockMode: 'auto', householdPasswordEnabled: false }));
      if (url === '/api/households/hh-1' && init?.method === 'DELETE') {
        return Promise.resolve(json({
          id: 'hh-1',
          name: 'Pantry',
          role: 'owner',
          deletedAt: '2026-04-02T00:00:00Z',
          purgeAt: '2026-05-02T00:00:00Z',
          message: HOUSEHOLD_DELETE_SENTENCE,
        }));
      }
      if (url === '/api/households/hh-1') return Promise.resolve(json({ id: 'hh-1', name: 'Pantry', role: 'owner', deletedAt: null, purgeAt: null }));
      if (url === '/api/me' && init?.method === 'DELETE') {
        return Promise.resolve(json({ error: 'blocked', code: 'last_owner' }, 409));
      }
      if (url === '/api/export' && init?.method === 'POST') return Promise.resolve(json({ jobId: 'job-1', status: 'pending' }));
      if (url === '/api/export/job-1') {
        return Promise.resolve(json({ jobId: 'job-1', status: 'ready', jsonUrl: '/files/pantry.json', csvUrl: '/files/pantry.csv', expiresAt: null }));
      }
      return Promise.reject(new Error(url));
    }));
    renderStage(<HouseholdPage />, session('owner'));
    expect(await screen.findByRole('button', { name: 'Make owner' })).toBeVisible();
    expect(screen.getByText(HOUSEHOLD_DELETE_SENTENCE)).toBeVisible();
    fireEvent.click(screen.getByRole('button', { name: 'Create invite' }));
    expect(await screen.findByText(/Invite link: https:\/\/app.example\/invite\/code/)).toBeVisible();
    fireEvent.click(screen.getByRole('button', { name: 'Revoke invite' }));
    expect(await screen.findByText('Cancelled')).toBeVisible();
    fireEvent.click(screen.getByRole('button', { name: 'Export household' }));
    expect(await screen.findByRole('link', { name: 'Download JSON' })).toHaveAttribute('href', '/files/pantry.json');
    expect(screen.getByRole('link', { name: 'Download CSV' })).toHaveAttribute('href', '/files/pantry.csv');

    fireEvent.change(screen.getByLabelText('Type Pantry to delete'), { target: { value: 'Pantry' } });
    fireEvent.click(screen.getByRole('button', { name: 'Delete household' }));
    expect(await screen.findByText(HOUSEHOLD_DELETE_SENTENCE)).toBeVisible();
    expect(await screen.findByRole('button', { name: 'Restore household' })).toBeVisible();

    fireEvent.change(screen.getByLabelText('Type ada@example.com to delete the account'), { target: { value: 'ada@example.com' } });
    fireEvent.click(screen.getByRole('button', { name: 'Delete account' }));
    expect(await screen.findByRole('alert')).toHaveTextContent("You're the only owner. Make someone else an owner first, or delete the household.");
    expect(calls.length).toBeGreaterThan(0);
  });

  it('switches the active household', async () => {
    const replaceAuth = vi.fn();
    const next = session('owner');
    next.activeHouseholdId = 'hh-2';
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url === '/api/auth/session/household' && init?.method === 'POST') {
        expect(JSON.parse(String(init.body))).toEqual({ householdId: 'hh-2' });
        return Promise.resolve(json(next));
      }
      return Promise.reject(new Error(url));
    }));
    renderStage(<HouseholdSwitcher />, session('owner'), replaceAuth);
    fireEvent.click(screen.getByRole('combobox', { name: 'Household' }));
    fireEvent.click(await screen.findByRole('option', { name: 'Cabin' }));
    await waitFor(() => expect(replaceAuth).toHaveBeenCalledWith(expect.objectContaining({ activeHouseholdId: 'hh-2' })));
  });
});

describe('devices, pairing, and live hints', () => {
  beforeEach(() => {
    FakeEventSource.instances = [];
    vi.stubGlobal('EventSource', FakeEventSource);
    resetStage1Client();
  });

  it('lists presence, renames a hub, and confirms disconnect with and without queued scans', async () => {
    let devices = [
      device({ id: 'hub', label: 'Kitchen hub', online: true, queued: 3 }),
      device({ id: 'shed', label: 'Shed hub', online: false, lastSeenAt: '2026-04-01T00:00:00Z', queued: 0 }),
      device({ id: 'direct', label: 'Counter', kind: 'direct', online: true }),
    ];
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url === '/api/devices' && init?.method === undefined) return Promise.resolve(json({ devices }));
      if (url === '/api/devices/hub' && init?.method === 'PATCH') {
        devices = devices.map((row) => row.id === 'hub' ? { ...row, label: 'Porch hub' } : row);
        return Promise.resolve(json(devices[0]));
      }
      if (url === '/api/devices/shed' && init?.method === 'DELETE') {
        devices = devices.filter((row) => row.id !== 'shed');
        return Promise.resolve(json({}));
      }
      if (url === '/api/devices/pair' && init?.method === 'POST') {
        return Promise.resolve(json({
          code: PAIRING,
          qrPayload: PAIRING,
          expiresAt: '2099-01-01T00:00:00Z',
          pairingId: 'pair-1',
          barcodeValue: PAIRING,
          qrValue: PAIRING,
        }));
      }
      if (url === '/api/devices/pair/pair-1') {
        return Promise.resolve(json({ pairingId: 'pair-1', status: 'pending', expiresAt: '2099-01-01T00:00:00Z' }));
      }
      return Promise.reject(new Error(url));
    }));
    const print = vi.fn();
    vi.stubGlobal('print', print);
    renderStage(<DevicesPage />, session('owner'));
    expect(await screen.findByText('Kitchen hub')).toBeVisible();
    expect(screen.getAllByText(/Hub/).length).toBeGreaterThan(0);
    expect(screen.getByText(/Direct scanner/)).toBeVisible();
    expect(screen.getAllByText(/Online/).length).toBeGreaterThan(0);
    expect(screen.getByText(/Offline/)).toBeVisible();
    const directRow = screen.getByText('Counter').closest('li');
    expect(directRow && within(directRow).queryByRole('button', { name: 'Rename' })).toBeNull();

    fireEvent.click(screen.getAllByRole('button', { name: 'Rename' })[0]);
    fireEvent.change(screen.getByLabelText('Name'), { target: { value: 'Porch hub' } });
    fireEvent.click(screen.getByRole('button', { name: 'Save' }));
    expect(await screen.findByText('Porch hub')).toBeVisible();

    const kitchen = screen.getByText('Porch hub').closest('li');
    if (kitchen === null) throw new Error('missing hub row');
    fireEvent.click(within(kitchen).getByRole('button', { name: 'Disconnect' }));
    const queuedDialog = await screen.findByRole('dialog', { name: 'Disconnect hub' });
    expect(within(queuedDialog).getByText("Disconnect Porch hub? 3 scans it hasn't sent yet will be lost.")).toBeVisible();
    const disconnect = within(queuedDialog).getByRole('button', { name: 'Disconnect' });
    expect(disconnect).toHaveClass('household-danger');
    fireEvent.click(within(queuedDialog).getByRole('button', { name: 'Cancel' }));

    const shed = screen.getByText('Shed hub').closest('li');
    if (shed === null) throw new Error('missing shed row');
    fireEvent.click(within(shed).getByRole('button', { name: 'Disconnect' }));
    const emptyDialog = await screen.findByRole('dialog', { name: 'Disconnect hub' });
    expect(within(emptyDialog).getByText('Disconnect Shed hub?')).toBeVisible();
    expect(within(emptyDialog).queryByText(/scans it hasn't sent/)).not.toBeInTheDocument();
    fireEvent.click(within(emptyDialog).getByRole('button', { name: 'Disconnect' }));
    expect(await screen.findByText('Shed hub was disconnected.')).toBeVisible();

    fireEvent.click(screen.getByRole('button', { name: 'Pair a hub' }));
    expect(await screen.findByRole('img', { name: `Code 128 ${PAIRING}` })).toBeVisible();
    expect(await screen.findByRole('img', { name: `QR code ${PAIRING}` })).toBeVisible();
    expect(screen.getByLabelText('Pairing code')).toHaveTextContent(PAIRING.split('').join(' '));
    expect(screen.getByText(/Scan this code with your scanner to connect your hub to Pantry/)).toBeVisible();
    expect(screen.getByText(new RegExp(`No printer\\? Plug a USB keyboard into the hub, type this code, and press Enter: ${PAIRING}`))).toBeVisible();
    fireEvent.click(screen.getByRole('button', { name: PRINT_PAIRING_CODE_LABEL }));
    expect(print).toHaveBeenCalled();
    expect(code128Bars(PAIRING).some(Boolean)).toBe(true);
  });

  it('says when a pairing code has expired', async () => {
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url === '/api/devices') return Promise.resolve(json({ devices: [] }));
      if (url === '/api/devices/pair' && init?.method === 'POST') {
        return Promise.resolve(json({
          code: PAIRING,
          qrPayload: PAIRING,
          expiresAt: '2000-01-01T00:00:00Z',
          pairingId: 'pair-1',
          barcodeValue: PAIRING,
          qrValue: PAIRING,
        }));
      }
      return Promise.reject(new Error(url));
    }));
    renderStage(<DevicesPage />, session('owner'));
    fireEvent.click(await screen.findByRole('button', { name: 'Pair a hub' }));
    expect(await screen.findByText(PAIRING_EXPIRED_SENTENCE)).toBeVisible();
    expect(screen.getByRole('button', { name: 'Get a new one' })).toBeVisible();
  });

  it('refetches devices when the stream hints, and polls after the stream drops', async () => {
    let loads = 0;
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL) => {
      if (String(input) === '/api/devices') {
        loads += 1;
        return Promise.resolve(json({ devices: [device({})] }));
      }
      return Promise.reject(new Error(String(input)));
    }));
    renderStage(<DevicesPage />, session('owner'));
    expect(await screen.findByText('Kitchen hub')).toBeVisible();
    const initial = loads;
    FakeEventSource.instances[0]?.dispatch('hint', { type: 'devices' });
    await waitFor(() => expect(loads).toBeGreaterThan(initial));

    let polls = 0;
    const Probe = () => {
      useLiveHint(['devices'], () => { polls += 1; }, 10_000);
      return null;
    };
    vi.useFakeTimers();
    render(<Probe />);
    FakeEventSource.instances.at(-1)?.onerror?.(new Event('error'));
    await vi.advanceTimersByTimeAsync(10_000);
    expect(polls).toBeGreaterThan(0);
    vi.useRealTimers();
  });

  it('refetches the scan queue on a scans hint', async () => {
    const auth = session('owner');
    let pendingLoads = 0;
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL) => {
      const url = String(input);
      if (url === '/api/auth/session') return Promise.resolve(json(auth));
      if (url.includes('/api/scans') && url.includes('pending')) {
        pendingLoads += 1;
        return Promise.resolve(json([]));
      }
      if (url.includes('/api/scans') || url.startsWith('/api/inventory') || url.endsWith('/api/scanner/config')) {
        return Promise.resolve(json(url.endsWith('/api/scanner/config')
          ? { stockInBarcode: 'STOCK_IN', stockOutBarcode: 'STOCK_OUT', currentMode: 'stock_in' }
          : []));
      }
      return Promise.reject(new Error(url));
    }));
    await getSession();
    render(<MantineProvider env="test"><ScanQueuePage /></MantineProvider>);
    await waitFor(() => expect(pendingLoads).toBeGreaterThan(0));
    const initial = pendingLoads;
    FakeEventSource.instances[0]?.dispatch('hint', { type: 'scans', cursor: '1' });
    await waitFor(() => expect(pendingLoads).toBeGreaterThan(initial));
  });
});

describe('offline banner sentences', () => {
  it('shows the unreachable sentence and the updating sentence', () => {
    applyHttpResponse(504, '');
    const { unmount } = render(<MantineProvider><ServerNoticeBanner /></MantineProvider>);
    expect(screen.getByText(OFFLINE_SENTENCE)).toBeVisible();
    expect(serverNoticeText()).toBe("Can't reach Stockbot. Check your connection and try again.");
    unmount();
    applyHttpResponse(503, 'updating');
    render(<MantineProvider><ServerNoticeBanner /></MantineProvider>);
    expect(screen.getByText(UPDATING_SENTENCE)).toBeVisible();
  });
});
