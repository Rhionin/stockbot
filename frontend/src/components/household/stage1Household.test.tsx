import { fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { MantineProvider } from '@mantine/core';
import { Notifications } from '@mantine/notifications';
import { MemoryRouter, Route, Routes } from 'react-router-dom';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import {
  HOUSEHOLD_DELETE_SENTENCE,
  OFFLINE_SENTENCE,
  PAIRING_EXPIRED_SENTENCE,
  PRINT_PAIRING_CODE_LABEL,
  UPDATING_SENTENCE,
  applyHttpResponse,
  formatPairingCode,
  deviceKindLabel,
  serverNoticeText,
  type AuthSession,
  type Device,
} from '@stockbot/shared/contract';
import { getSession, resetStage1Client } from '../../api/client';
import { HouseholdSessionContext } from '../auth/householdSession';
import { ServerNoticeBanner } from '../shell/ServerNoticeBanner';
import { ScanQueuePage } from '../queue/ScanQueuePage';
import { code128Bars } from './code128';
import { DevicesPage } from './DevicesPage';
import { HouseholdPage } from './HouseholdPage';
import App from '../../App';
import { AppMenu } from '../shell/AppMenu';
import { HouseholdsPage } from './HouseholdsPage';
import { extractInviteCode } from './inviteCode';
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

function renderStageRoutes(
  ui: React.ReactNode,
  auth: AuthSession,
  replaceAuth = vi.fn(),
  initialPath = '/',
) {
  return render(
    <MantineProvider env="test">
      <Notifications />
      <MemoryRouter initialEntries={[initialPath]}>
        <HouseholdSessionContext.Provider value={{ required: true, logout: vi.fn(), auth, replaceAuth }}>
          <Routes>
            <Route path="/" element={ui} />
            <Route path="/households" element={<HouseholdsPage />} />
            <Route path="/household" element={<div>Active household page</div>} />
          </Routes>
        </HouseholdSessionContext.Provider>
      </MemoryRouter>
    </MantineProvider>,
  );
}

describe('household management', () => {
  beforeEach(() => {
    FakeEventSource.instances = [];
    vi.stubGlobal('EventSource', FakeEventSource);
    resetStage1Client();
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
      if (url === '/api/household/settings') return Promise.resolve(json({ restockMode: 'auto' }));
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
      if (url === '/api/household/settings') return Promise.resolve(json({ restockMode: 'auto' }));
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

  it('lets an owner rename the household and refresh the session', async () => {
    const replaceAuth = vi.fn();
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url.includes('/members') && init?.method === undefined) {
        return Promise.resolve(json({ members: [{ userId: 'user-1', email: 'ada@example.com', role: 'owner' }] }));
      }
      if (url === '/api/invites') return Promise.resolve(json({ invites: [] }));
      if (url === '/api/household/settings') return Promise.resolve(json({ restockMode: 'auto' }));
      if (url === '/api/households/hh-1' && init?.method === 'PATCH') {
        return Promise.resolve(json({ id: 'hh-1', name: 'Cupboard', role: 'owner', deletedAt: null, purgeAt: null }));
      }
      if (url === '/api/households/hh-1') {
        return Promise.resolve(json({ id: 'hh-1', name: 'Cupboard', role: 'owner', deletedAt: null, purgeAt: null }));
      }
      if (url === '/api/auth/session') {
        return Promise.resolve(json({
          ...session('owner'),
          households: [{ id: 'hh-1', name: 'Cupboard', role: 'owner', deletedAt: null }],
          memberships: [{ householdId: 'hh-1', name: 'Cupboard', role: 'owner', deletedAt: null }],
        }));
      }
      return Promise.reject(new Error(url));
    }));
    renderStage(<HouseholdPage />, session('owner'), replaceAuth);
    const nameInput = await screen.findByRole('textbox', { name: 'Household name' });
    fireEvent.change(nameInput, { target: { value: 'Cupboard' } });
    fireEvent.click(screen.getByRole('button', { name: 'Save name' }));
    expect(await screen.findByText('Household name saved.')).toBeVisible();
    expect(replaceAuth).toHaveBeenCalled();
  });

  it('shows rename errors inline and hides rename controls from members', async () => {
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url.includes('/members') && init?.method === undefined) {
        return Promise.resolve(json({ members: [{ userId: 'user-1', email: 'ada@example.com', role: 'member' }] }));
      }
      if (url === '/api/invites') return Promise.resolve(json({ invites: [] }));
      if (url === '/api/household/settings') return Promise.resolve(json({ restockMode: 'auto' }));
      if (url === '/api/households/hh-1') {
        return Promise.resolve(json({ id: 'hh-1', name: 'Pantry', role: 'member', deletedAt: null, purgeAt: null }));
      }
      return Promise.reject(new Error(url));
    }));
    renderStage(<HouseholdPage />, session('member'));
    expect(await screen.findByRole('heading', { name: 'Pantry' })).toBeVisible();
    expect(screen.queryByRole('button', { name: 'Save name' })).not.toBeInTheDocument();

    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url.includes('/members') && init?.method === undefined) {
        return Promise.resolve(json({ members: [{ userId: 'user-1', email: 'ada@example.com', role: 'owner' }] }));
      }
      if (url === '/api/invites') return Promise.resolve(json({ invites: [] }));
      if (url === '/api/household/settings') return Promise.resolve(json({ restockMode: 'auto' }));
      if (url === '/api/households/hh-1' && init?.method === 'PATCH') {
        return Promise.resolve(json({ error: 'bad', code: 'invalid_input' }, 400));
      }
      if (url === '/api/households/hh-1') {
        return Promise.resolve(json({ id: 'hh-1', name: 'Pantry', role: 'owner', deletedAt: null, purgeAt: null }));
      }
      return Promise.reject(new Error(url));
    }));
    renderStage(<HouseholdPage />, session('owner'));
    const nameInput = await screen.findByRole('textbox', { name: 'Household name' });
    fireEvent.change(nameInput, { target: { value: ' ' } });
    fireEvent.click(screen.getByRole('button', { name: 'Save name' }));
    expect(await screen.findByRole('alert')).toHaveTextContent('Check what you entered and try again.');
  });

  it('loads operator households only for operators', async () => {
    const operatorList = vi.fn();
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL) => {
      const url = String(input);
      if (url === '/api/operator/households') {
        operatorList();
        return Promise.resolve(json({
          households: [{
            id: 'hh-9',
            name: 'Loft',
            isPrimary: false,
            restockMode: 'auto',
            createdAt: '2026-04-01T00:00:00Z',
            members: [{ userId: 'user-9', email: 'dee@example.com', name: 'Dee', role: 'owner', createdAt: '2026-04-01T00:00:00Z' }],
            memberCount: 1,
            productCount: 2,
            itemsOnHand: 3,
            pendingScans: 1,
            lastActivityAt: '2026-04-02T00:00:00Z',
          }],
        }));
      }
      return Promise.reject(new Error(url));
    }));
    const { unmount: unmountOperator } = renderStage(<HouseholdsPage />, { ...session('owner'), operator: true });
    expect(await screen.findByTestId('operator-households-section')).toBeVisible();
    expect(screen.getByText('Loft')).toBeVisible();
    expect(screen.getByText('dee@example.com')).toBeVisible();
    expect(operatorList).toHaveBeenCalled();
    unmountOperator();

    operatorList.mockClear();
    const { unmount } = renderStage(<HouseholdsPage />, session('owner'));
    expect(screen.queryByTestId('operator-households-section')).not.toBeInTheDocument();
    expect(operatorList).not.toHaveBeenCalled();
    unmount();
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

  it('shows one household as a name, not a menu', () => {
    const auth = session('owner');
    auth.households = [{ id: 'hh-1', name: 'Pantry', role: 'owner', deletedAt: null }];
    auth.memberships = [{ householdId: 'hh-1', name: 'Pantry', role: 'owner', deletedAt: null }];
    renderStage(<HouseholdSwitcher />, auth);
    expect(screen.getByRole('button', { name: 'Pantry' })).toHaveClass('household-switch-name');
    expect(screen.queryByRole('combobox', { name: 'Household' })).not.toBeInTheDocument();
    expect(document.querySelector('select')).toBeNull();
  });

  it('navigates to other households from the single-household switcher', async () => {
    const auth = session('owner');
    auth.households = [{ id: 'hh-1', name: 'Pantry', role: 'owner', deletedAt: null }];
    auth.memberships = [{ householdId: 'hh-1', name: 'Pantry', role: 'owner', deletedAt: null }];
    renderStageRoutes(<HouseholdSwitcher />, auth);
    fireEvent.click(screen.getByRole('button', { name: 'Pantry' }));
    expect(await screen.findByRole('heading', { name: 'Your households' })).toBeVisible();
  });

  it('navigates to other households from the multi-household switcher', async () => {
    renderStageRoutes(<HouseholdSwitcher />, session('owner'));
    fireEvent.click(screen.getByRole('combobox', { name: 'Household' }));
    fireEvent.click(await screen.findByRole('option', { name: 'Other households…' }));
    expect(await screen.findByRole('heading', { name: 'Your households' })).toBeVisible();
  });

  it('hides operator-only household controls from non-operators', () => {
    renderStage(<HouseholdsPage />, session('owner'));
    const start = screen.getByRole('form', { name: 'Start a household' });
    expect(within(start).getByRole('textbox', { name: /Invite code/i })).toBeRequired();
    expect(screen.queryByRole('heading', { name: 'Invite someone else to start a household' })).not.toBeInTheDocument();
  });

  it('navigates to the active household page from Manage household', async () => {
    vi.stubGlobal('fetch', vi.fn(() => Promise.reject(new Error('unused'))));
    renderStageRoutes(<HouseholdsPage />, session('owner'), vi.fn(), '/households');
    fireEvent.click(screen.getByRole('button', { name: 'Manage household' }));
    expect(await screen.findByText('Active household page')).toBeVisible();
  });

  it('shows invite errors inline in the Invites section', async () => {
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url.includes('/members') && init?.method === undefined) {
        return Promise.resolve(json({ members: [{ userId: 'user-2', email: 'bea@example.com', role: 'member' }] }));
      }
      if (url === '/api/invites' && init?.method === 'POST') {
        return Promise.resolve(json({ error: 'nope', code: 'forbidden_role' }, 403));
      }
      if (url === '/api/invites') return Promise.resolve(json({ invites: [] }));
      if (url === '/api/household/settings') return Promise.resolve(json({ restockMode: 'auto' }));
      if (url === '/api/households/hh-1') return Promise.resolve(json({ id: 'hh-1', name: 'Pantry', role: 'owner', deletedAt: null, purgeAt: null }));
      return Promise.reject(new Error(url));
    }));
    renderStage(<HouseholdPage />, session('owner'));
    fireEvent.click(await screen.findByRole('button', { name: 'Create invite' }));
    const invites = screen.getByRole('heading', { name: 'Invites' }).closest('section');
    expect(invites).not.toBeNull();
    await waitFor(() => expect(within(invites!).getByRole('alert')).toBeVisible());
  });

  it('opens other households from the app menu', async () => {
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL) => {
      const url = String(input);
      if (url === '/api/providers') return Promise.resolve(json([]));
      if (url === '/api/group-suggestions') return Promise.resolve(json([]));
      if (url === '/api/build') return Promise.resolve(json({}));
      return Promise.reject(new Error(url));
    }));
    render(
      <MantineProvider env="test">
        <MemoryRouter initialEntries={['/']}>
          <HouseholdSessionContext.Provider value={{ required: true, logout: vi.fn(), auth: session('owner'), replaceAuth: vi.fn() }}>
            <Routes>
              <Route path="/" element={<AppMenu onCredentialsChanged={() => undefined} />} />
              <Route path="/households" element={<HouseholdsPage />} />
            </Routes>
          </HouseholdSessionContext.Provider>
        </MemoryRouter>
      </MantineProvider>,
    );
    fireEvent.click(screen.getByRole('button', { name: 'Menu' }));
    fireEvent.click(await screen.findByRole('menuitem', { name: 'Other households' }));
    expect(await screen.findByRole('heading', { name: 'Your households' })).toBeVisible();
  });

  it('renders HouseholdsPage at /households in App', async () => {
    vi.stubGlobal('EventSource', FakeEventSource);
    const auth = session('owner');
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL) => {
      const url = String(input);
      if (url === '/api/auth/session') return Promise.resolve(json(auth));
      if (url === '/api/config') return Promise.resolve(json({ live: 'sse', minClientVersion: { expo: '1.0.0' }, entry: 'public' }));
      if (url === '/api/providers' || url === '/api/group-suggestions') return Promise.resolve(json([]));
      if (url === '/api/build') return Promise.resolve(json({}));
      if (url === '/api/scanner/config') {
        return Promise.resolve(json({ stockInBarcode: 'STOCK_IN', stockOutBarcode: 'STOCK_OUT', currentMode: 'stock_in' }));
      }
      return Promise.resolve(json({}));
    }));
    window.history.pushState({}, '', '/households');
    render(
      <MantineProvider env="test">
        <App />
      </MantineProvider>,
    );
    expect(await screen.findByRole('heading', { name: 'Your households' })).toBeVisible();
    window.history.pushState({}, '', '/');
  });

  it('links to other households instead of starting another household inline', () => {
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL) => {
      const url = String(input);
      if (url.includes('/members')) return Promise.resolve(json({ members: [] }));
      if (url === '/api/invites') return Promise.resolve(json({ invites: [] }));
      if (url === '/api/household/settings') return Promise.resolve(json({ restockMode: 'auto' }));
      if (url === '/api/households/hh-1') return Promise.resolve(json({ id: 'hh-1', name: 'Pantry', role: 'owner', deletedAt: null, purgeAt: null }));
      return Promise.reject(new Error(url));
    }));
    renderStage(<HouseholdPage />, session('owner'));
    expect(screen.queryByRole('form', { name: 'Start a household' })).not.toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Switch or add a household' })).toBeVisible();
    expect(screen.getByRole('heading', { name: 'Other households' })).toBeVisible();
  });

  it('renders reset links inline in Members', async () => {
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url.includes('/members') && init?.method === undefined) {
        return Promise.resolve(json({ members: [{ userId: 'user-2', email: 'bea@example.com', role: 'member' }] }));
      }
      if (url.includes('/reset-link') && init?.method === 'POST') {
        return Promise.resolve(json({ url: 'https://app.example/reset/secret' }));
      }
      if (url === '/api/invites') return Promise.resolve(json({ invites: [] }));
      if (url === '/api/household/settings') return Promise.resolve(json({ restockMode: 'auto' }));
      if (url === '/api/households/hh-1') return Promise.resolve(json({ id: 'hh-1', name: 'Pantry', role: 'owner', deletedAt: null, purgeAt: null }));
      return Promise.reject(new Error(url));
    }));
    renderStage(<HouseholdPage />, session('owner'));
    fireEvent.click(await screen.findByRole('button', { name: 'Reset link' }));
    expect(await screen.findByText(/Reset link for bea@example.com: https:\/\/app.example\/reset\/secret/)).toBeVisible();
  });

  it('surfaces reset link errors inline in Members', async () => {
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url.includes('/members') && init?.method === undefined) {
        return Promise.resolve(json({ members: [{ userId: 'user-2', email: 'bea@example.com', role: 'member' }] }));
      }
      if (url.includes('/reset-link') && init?.method === 'POST') {
        return Promise.resolve(json({ error: 'nope', code: 'forbidden_role' }, 403));
      }
      if (url === '/api/invites') return Promise.resolve(json({ invites: [] }));
      if (url === '/api/household/settings') return Promise.resolve(json({ restockMode: 'auto' }));
      if (url === '/api/households/hh-1') return Promise.resolve(json({ id: 'hh-1', name: 'Pantry', role: 'owner', deletedAt: null, purgeAt: null }));
      return Promise.reject(new Error(url));
    }));
    renderStage(<HouseholdPage />, session('owner'));
    fireEvent.click(await screen.findByRole('button', { name: 'Reset link' }));
    const members = screen.getByRole('heading', { name: 'Members' }).closest('section');
    expect(members).not.toBeNull();
    await waitFor(() => expect(within(members!).getByRole('alert')).toBeVisible());
  });

  it('lists households and switches active household', async () => {
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
    renderStage(<HouseholdsPage />, session('owner'), replaceAuth);
    expect(screen.getByText('Cabin')).toBeVisible();
    fireEvent.click(screen.getByRole('button', { name: 'Switch' }));
    await waitFor(() => expect(replaceAuth).toHaveBeenCalledWith(expect.objectContaining({ activeHouseholdId: 'hh-2' })));
    expect(await screen.findByRole('status')).toHaveTextContent('Switched to Cabin.');
  });

  it('creates a household for operators without an invite code', async () => {
    const replaceAuth = vi.fn();
    const auth = session('owner');
    auth.operator = true;
    const calls: string[] = [];
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      calls.push(`${init?.method ?? 'GET'} ${url}`);
      if (url === '/api/operator/household-invites' && init?.method === 'POST') {
        return Promise.resolve(json({ id: 'op-1', code: 'AUTO-CODE', url: 'https://app.example/invite/AUTO-CODE', email: null, role: 'owner', expiresAt: '2026-06-01T00:00:00Z' }));
      }
      if (url === '/api/households' && init?.method === 'POST') {
        expect(JSON.parse(String(init.body))).toEqual({ name: 'Cabin', restockMode: 'auto', inviteCode: 'AUTO-CODE' });
        return Promise.resolve(json({ id: 'hh-3', name: 'Cabin', role: 'owner' }));
      }
      if (url === '/api/auth/session/household' && init?.method === 'POST') {
        const switched = { ...auth, activeHouseholdId: 'hh-3', households: [...auth.households, { id: 'hh-3', name: 'Cabin', role: 'owner', deletedAt: null }] };
        return Promise.resolve(json(switched));
      }
      return Promise.reject(new Error(url));
    }));
    renderStage(<HouseholdsPage />, auth, replaceAuth);
    const start = screen.getByRole('form', { name: 'Start a household' });
    fireEvent.change(within(start).getByRole('textbox', { name: /Household name/ }), { target: { value: 'Cabin' } });
    fireEvent.click(within(start).getByRole('button', { name: 'Create household' }));
    expect(await screen.findByRole('status')).toHaveTextContent('Cabin was created.');
    expect(calls.some((row) => row.includes('/api/operator/household-invites'))).toBe(true);
    await waitFor(() => expect(replaceAuth).toHaveBeenCalled());
  });

  it('extracts invite codes from full invite URLs when starting a household', async () => {
    expect(extractInviteCode('https://stockbot.rhionin.com/invite/START-9999')).toBe('START-9999');
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url === '/api/households' && init?.method === 'POST') {
        expect(JSON.parse(String(init.body))).toEqual({ name: 'Lake', restockMode: 'auto', inviteCode: 'START-9999' });
        return Promise.resolve(json({ id: 'hh-9', name: 'Lake', role: 'owner' }));
      }
      if (url === '/api/auth/session/household' && init?.method === 'POST') {
        return Promise.resolve(json(session('owner')));
      }
      return Promise.reject(new Error(url));
    }));
    renderStage(<HouseholdsPage />, session('owner'));
    const start = screen.getByRole('form', { name: 'Start a household' });
    fireEvent.change(within(start).getByRole('textbox', { name: /Household name/ }), { target: { value: 'Lake' } });
    fireEvent.change(within(start).getByRole('textbox', { name: /Invite code/ }), { target: { value: 'https://stockbot.rhionin.com/invite/START-9999' } });
    fireEvent.click(within(start).getByRole('button', { name: 'Create household' }));
    expect(await screen.findByRole('status')).toHaveTextContent('Lake was created.');
  });

  it('shows an inline invite-not-found error when creating a household', async () => {
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url === '/api/households' && init?.method === 'POST') {
        return Promise.resolve(json({ error: 'missing', code: 'not_found' }, 404));
      }
      return Promise.reject(new Error(url));
    }));
    renderStage(<HouseholdsPage />, session('owner'));
    const startForm = screen.getByRole('form', { name: 'Start a household' });
    fireEvent.change(within(startForm).getByRole('textbox', { name: /Household name/ }), { target: { value: 'Lake' } });
    fireEvent.change(within(startForm).getByRole('textbox', { name: /Invite code/ }), { target: { value: 'BAD-CODE' } });
    fireEvent.click(within(startForm).getByRole('button', { name: 'Create household' }));
    const start = screen.getByRole('heading', { name: 'Start a household' }).closest('section');
    expect(start).not.toBeNull();
    await waitFor(() => {
      expect(within(start!).getByRole('alert')).toHaveTextContent("That invite code wasn't found. Check the code or ask for a new invite.");
    });
  });

  it('creates operator start-household invites with inline feedback', async () => {
    const auth = session('owner');
    auth.operator = true;
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url === '/api/operator/household-invites' && init?.method === 'POST') {
        return Promise.resolve(json({ id: 'op-2', code: 'OP-CODE', url: 'https://app.example/invite/OP-CODE', email: null, role: 'owner', expiresAt: '2026-06-01T00:00:00Z' }));
      }
      return Promise.reject(new Error(url));
    }));
    renderStage(<HouseholdsPage />, auth);
    fireEvent.click(screen.getByRole('button', { name: 'Create a start-household invite' }));
    expect(await screen.findByText(/Invite link: https:\/\/app.example\/invite\/OP-CODE/)).toBeVisible();
  });

  it('surfaces operator start-household invite errors inline', async () => {
    const auth = session('owner');
    auth.operator = true;
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url === '/api/operator/household-invites' && init?.method === 'POST') {
        return Promise.resolve(json({ error: 'nope', code: 'forbidden_role' }, 403));
      }
      return Promise.reject(new Error(url));
    }));
    renderStage(<HouseholdsPage />, auth);
    fireEvent.click(screen.getByRole('button', { name: 'Create a start-household invite' }));
    const operator = screen.getByRole('heading', { name: 'Invite someone else to start a household' }).closest('section');
    expect(operator).not.toBeNull();
    await waitFor(() => expect(within(operator!).getByRole('alert')).toBeVisible());
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

  it('hides disconnect from a member who can still pair and rename', async () => {
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL) => {
      if (String(input) === '/api/devices') {
        return Promise.resolve(json({ devices: [device({ id: 'hub', label: 'Kitchen hub' })] }));
      }
      return Promise.reject(new Error(String(input)));
    }));
    renderStage(<DevicesPage />, session('member'));
    expect(await screen.findByRole('button', { name: 'Pair a hub' })).toBeVisible();
    expect(screen.getByRole('button', { name: 'Rename' })).toBeVisible();
    expect(screen.queryByRole('button', { name: 'Disconnect' })).not.toBeInTheDocument();
  });

  it('locks the code 128 checksum before the stop pattern', () => {
    const bars = code128Bars('2345-6789');
    expect(bars.length).toBeGreaterThan(100);
    const widths: number[] = [];
    let index = 0;
    while (index < bars.length) {
      const on = bars[index];
      let run = 0;
      while (index < bars.length && bars[index] === on) {
        run += 1;
        index += 1;
      }
      widths.push(run);
    }
    expect(widths.join('')).toBe('2112142232112211322212312132121221322231123121313112223211222113312331112');
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

  it('labels esp32 scanners for display', () => {
    expect(deviceKindLabel('esp32_scanner')).toBe('Scanner');
    expect(deviceKindLabel('pi_hub')).toBe('Hub');
    expect(deviceKindLabel('direct')).toBe('Direct scanner');
  });

  it('renders household scanners without breaking unmocked device fetches', async () => {
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url.includes('/members') && init?.method === undefined) {
        return Promise.resolve(json({ members: [{ userId: 'user-1', email: 'ada@example.com', role: 'member' }] }));
      }
      if (url === '/api/invites') return Promise.resolve(json({ invites: [] }));
      if (url === '/api/household/settings') return Promise.resolve(json({ restockMode: 'auto' }));
      if (url === '/api/households/hh-1') {
        return Promise.resolve(json({ id: 'hh-1', name: 'Pantry', role: 'member', deletedAt: null, purgeAt: null }));
      }
      return Promise.reject(new Error(url));
    }));
    renderStage(<HouseholdPage />, session('member'));
    expect(await screen.findByRole('heading', { name: 'Scanners' })).toBeVisible();
    const scannersSection = screen.getByRole('heading', { name: 'Scanners' }).closest('section');
    expect(scannersSection).not.toBeNull();
    expect(within(scannersSection!).getByText(/No storage-room scanners connected yet/)).toBeVisible();
    expect(within(scannersSection!).queryByRole('alert')).not.toBeInTheDocument();
  });

  it('shows scanner telemetry on the household scanners section', async () => {
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url.includes('/members') && init?.method === undefined) {
        return Promise.resolve(json({ members: [{ userId: 'user-1', email: 'ada@example.com', role: 'owner' }] }));
      }
      if (url === '/api/invites') return Promise.resolve(json({ invites: [] }));
      if (url === '/api/household/settings') return Promise.resolve(json({ restockMode: 'auto' }));
      if (url === '/api/households/hh-1') {
        return Promise.resolve(json({ id: 'hh-1', name: 'Pantry', role: 'owner', deletedAt: null, purgeAt: null }));
      }
      if (url === '/api/devices' && init?.method === undefined) {
        return Promise.resolve(json({
          devices: [
            device({
              id: 'scan-a',
              label: 'Pantry pistol',
              kind: 'esp32_scanner',
              batteryPct: 84,
              charging: true,
              queued: 3,
              online: false,
            }),
            device({ id: 'scan-b', label: 'Low', kind: 'esp32_scanner', batteryPct: 18, charging: false, online: true }),
            device({ id: 'scan-c', label: 'Critical', kind: 'esp32_scanner', batteryPct: 8, charging: false, online: true }),
          ],
        }));
      }
      return Promise.reject(new Error(url));
    }));
    renderStage(<HouseholdPage />, session('owner'));
    const section = await screen.findByRole('heading', { name: 'Scanners' });
    expect(section.closest('section')).toHaveAttribute('aria-labelledby', 'scanners-heading');
    expect(await screen.findByText('84% (Charging)')).toBeVisible();
    expect(screen.getByText('18% (Low battery)')).toBeVisible();
    expect(screen.getByText('8% (Charge now)')).toBeVisible();
    expect(screen.getByText(/3 waiting to send/)).toBeVisible();
    expect(screen.getByText(/Scanner · Offline/)).toBeVisible();
  });

  it('patches scanner settings with inline saving and success feedback', async () => {
    let row = device({
      id: 'scan-1',
      label: 'Pantry pistol',
      kind: 'esp32_scanner',
      commitMode: 'queue',
      defaultDirection: 'stock_out',
      inactivityRevertSeconds: 300,
    });
    const patches: unknown[] = [];
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url === '/api/devices' && init?.method === undefined) return Promise.resolve(json({ devices: [row] }));
      if (url === '/api/devices/scan-1' && init?.method === 'PATCH') {
        const body = JSON.parse(String(init.body));
        patches.push(body);
        row = { ...row, ...body };
        return new Promise((resolve) => {
          window.setTimeout(() => resolve(json(row)), 40);
        });
      }
      return Promise.reject(new Error(url));
    }));
    renderStage(<DevicesPage />, session('member'));
    expect(await screen.findByText('Pantry pistol')).toBeVisible();
    fireEvent.click(screen.getByRole('radio', { name: /Adjust stock immediately/ }));
    expect(await screen.findByText('Saving…')).toBeVisible();
    await waitFor(() => expect(screen.getByRole('status')).toHaveTextContent('Scanner settings saved.'));
    expect(patches[0]).toEqual({ commitMode: 'immediate' });

    fireEvent.click(screen.getByRole('radio', { name: /Stock in \(Scans add to stock\)/ }));
    await waitFor(() => expect(patches.at(-1)).toEqual({ defaultDirection: 'stock_in' }));

    fireEvent.click(screen.getByRole('radio', { name: '1 minute' }));
    await waitFor(() => expect(patches.at(-1)).toEqual({ inactivityRevertSeconds: 60 }));

    fireEvent.click(screen.getByRole('radio', { name: '3 minutes' }));
    await waitFor(() => expect(patches.at(-1)).toEqual({ inactivityRevertSeconds: 180 }));

    fireEvent.click(screen.getByRole('radio', { name: '10 minutes' }));
    await waitFor(() => expect(patches.at(-1)).toEqual({ inactivityRevertSeconds: 600 }));
  });

  it('keeps scanner settings on patch failure and shows an inline alert', async () => {
    const row = device({
      id: 'scan-1',
      label: 'Pantry pistol',
      kind: 'esp32_scanner',
      commitMode: 'queue',
    });
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url === '/api/devices' && init?.method === undefined) return Promise.resolve(json({ devices: [row] }));
      if (url === '/api/devices/scan-1' && init?.method === 'PATCH') {
        return Promise.resolve(json({ error: 'nope', code: 'forbidden_role' }, 403));
      }
      return Promise.reject(new Error(url));
    }));
    renderStage(<DevicesPage />, session('member'));
    expect(await screen.findByText('Pantry pistol')).toBeVisible();
    fireEvent.click(screen.getByRole('radio', { name: /Adjust stock immediately/ }));
    const card = screen.getByText('Pantry pistol').closest('li');
    expect(card).not.toBeNull();
    await waitFor(() => expect(within(card!).getByRole('alert')).toHaveTextContent('Only an owner can do that.'));
    expect(within(card!).getByRole('radio', { name: /Require review in Scan Queue/ })).toBeChecked();
  });

  it('shows pairing wifi help and opens the wall barcode sheet', async () => {
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url === '/api/devices') return Promise.resolve(json({ devices: [] }));
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
    renderStage(<DevicesPage />, session('member'));
    fireEvent.click(await screen.findByRole('button', { name: 'Pair a hub' }));
    const pairingDialog = await screen.findByRole('dialog', { name: 'Pair a hub' });
    expect(within(pairingDialog).getByText('Setting up a brand-new scanner?')).toBeVisible();
    expect(within(pairingDialog).getByText(/Stockbot-Scanner-XXXX/)).toBeVisible();
    expect(within(pairingDialog).getByRole('img', { name: `Code 128 ${PAIRING}` })).toBeVisible();
    fireEvent.click(within(pairingDialog).getByRole('button', { name: 'Close' }));
    fireEvent.click(screen.getByRole('button', { name: 'Print wall barcodes' }));
    const sheet = await screen.findByRole('dialog', { name: 'Storage-room barcode sheet' });
    expect(sheet.querySelector('.storage-barcode-sheet')).not.toBeNull();
    expect(screen.getByRole('img', { name: 'Code 128 STOCK-OUT' })).toBeVisible();
    expect(screen.getByRole('img', { name: 'Code 128 STOCK-IN' })).toBeVisible();
    expect(screen.getByRole('img', { name: 'Code 128 %%SpecCode99' })).toBeVisible();
    fireEvent.click(within(sheet).getByRole('button', { name: 'Print sheet' }));
    expect(print).toHaveBeenCalled();
  });

  it('lets members pair and print wall barcodes but hides disconnect', async () => {
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL) => {
      if (String(input) === '/api/devices') {
        return Promise.resolve(json({ devices: [device({ id: 'hub', label: 'Kitchen hub' })] }));
      }
      return Promise.reject(new Error(String(input)));
    }));
    renderStage(<DevicesPage />, session('member'));
    expect(await screen.findByRole('button', { name: 'Pair a hub' })).toBeVisible();
    expect(screen.getByRole('button', { name: 'Print wall barcodes' })).toBeVisible();
    expect(screen.getByRole('button', { name: 'Rename' })).toBeVisible();
    expect(screen.queryByRole('button', { name: 'Disconnect' })).not.toBeInTheDocument();
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
