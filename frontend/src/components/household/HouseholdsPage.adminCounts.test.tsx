import { StrictMode } from 'react';
import { fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { MantineProvider } from '@mantine/core';
import { MemoryRouter, Route, Routes } from 'react-router-dom';
import { beforeEach, afterEach, describe, expect, it, vi } from 'vitest';
import { SERVER_ERROR_FALLBACK } from '@stockbot/shared/contract';
import type { AuthSession, OperatorHouseholdView } from '@stockbot/shared/contract';
import { resetStage1Client } from '../../api/client';
import { HouseholdSessionContext } from '../auth/householdSession';
import { HouseholdPage } from './HouseholdPage';
import { HouseholdsPage } from './HouseholdsPage';

const json = (body: unknown, status = 200) =>
  new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } });

function session(operator: boolean): AuthSession {
  return {
    user: { id: 'user-1', email: 'admin@example.com', name: 'Admin User' },
    kind: 'account',
    memberships: [{ householdId: 'hh-1', name: 'Test Household', role: 'owner', deletedAt: null }],
    households: [{ id: 'hh-1', name: 'Test Household', role: 'owner', deletedAt: null }],
    activeHouseholdId: 'hh-1',
    operator,
  };
}

function renderManagePage(auth: AuthSession) {
  return render(
    <MantineProvider env="test">
      <MemoryRouter initialEntries={['/household']}>
        <Routes>
          <Route
            path="/household"
            element={(
              <HouseholdSessionContext.Provider value={{ required: true, logout: vi.fn(), auth, replaceAuth: vi.fn() }}>
                <HouseholdPage />
              </HouseholdSessionContext.Provider>
            )}
          />
        </Routes>
      </MemoryRouter>
    </MantineProvider>,
  );
}

function renderAdminPage(auth: AuthSession) {
  return render(
    <MantineProvider env="test">
      <MemoryRouter>
        <HouseholdSessionContext.Provider value={{ required: true, logout: vi.fn(), auth, replaceAuth: vi.fn() }}>
          <HouseholdsPage />
        </HouseholdSessionContext.Provider>
      </MemoryRouter>
    </MantineProvider>,
  );
}

/** Member email/name/role must not appear anywhere: text, attributes, or as role words. */
function expectNoMemberPii(section: HTMLElement, secrets: string[]) {
  for (const secret of secrets) {
    expect(document.body.textContent).not.toContain(secret);
    expect(document.body.innerHTML).not.toContain(secret);
  }
  expect(within(section).queryAllByText(/^\s*(Owner|Member|Admin)\s*$/)).toHaveLength(0);
}

const testHouseholdRow: OperatorHouseholdView = {
  id: 'hh-test',
  name: 'Test Household',
  isPrimary: true,
  restockMode: 'auto',
  createdAt: '2026-04-01T00:00:00Z',
  memberCount: 1,
  productCount: 2,
  itemsOnHand: 3,
  pendingScans: 0,
  scannersOnline: 1,
  scannersOffline: 0,
  scansThisWeek: 1,
  lastActivityAt: '2026-04-02T12:00:00Z',
};

/** Simulates an old or buggy server still sending a deprecated members array. */
function operatorHouseholdsWithLegacyMembers(households: OperatorHouseholdView[], legacyMembers: unknown[]): unknown {
  return {
    households: households.map((row) => ({ ...row, members: legacyMembers }) as unknown),
  };
}

describe('HouseholdsPage admin server list', () => {
  beforeEach(() => {
    resetStage1Client();
    vi.restoreAllMocks();
    vi.stubEnv('TZ', 'UTC');
  });

  afterEach(() => {
    vi.unstubAllEnvs();
  });

  it('renders grouped count stats with singular pluralization and no member PII', async () => {
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL) => {
      const url = String(input);
      if (url === '/api/operator/households') {
        return Promise.resolve(json(operatorHouseholdsWithLegacyMembers([testHouseholdRow], [{
          userId: 'hidden-1',
          email: 'secret@example.com',
          name: 'Secret Person',
          role: 'owner',
          createdAt: '2026-04-01T00:00:00Z',
        }])));
      }
      return Promise.reject(new Error(url));
    }));
    renderAdminPage(session(true));
    const section = await screen.findByTestId('operator-households-section');
    expect(section).toBeVisible();
    expect(screen.getByText('Test Household', { selector: '.admin-household-name' })).toBeVisible();
    const stats = screen.getByTestId('operator-household-stats');
    const statLines = stats.querySelectorAll('.admin-household-stat-line');
    expect(statLines).toHaveLength(4);
    expect(statLines[0]).toHaveTextContent(/^1 member · 2 products · 3 items on hand$/);
    expect(statLines[1]).toHaveTextContent(/^Scanners: 1 online · 0 offline$/);
    expect(statLines[2]).toHaveTextContent(/^1 scan this week · 0 pending scans$/);
    expect(statLines[3]).toHaveTextContent(/^Last activity Apr 2, 12:00 PM$/);
    expect(within(section).getByText('New items restock automatically', { exact: true })).toBeVisible();
    expect(within(section).getByText('Created Apr 1', { exact: true })).toBeVisible();
    expectNoMemberPii(section, ['secret@example.com', 'Secret Person']);
    expect(document.body.textContent).not.toMatch(/\boperator\b/i);
  });

  it('shows empty and loading states in the admin section', async () => {
    let resolveList: (value: Response) => void = () => undefined;
    const listPromise = new Promise<Response>((resolve) => {
      resolveList = resolve;
    });
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL) => {
      const url = String(input);
      if (url === '/api/operator/households') return listPromise;
      return Promise.reject(new Error(url));
    }));
    renderAdminPage(session(true));
    expect(await screen.findByText('Loading households…')).toBeVisible();
    expect(screen.queryByText('No households on this server yet.')).not.toBeInTheDocument();
    resolveList(json({ households: [] }));
    await waitFor(() => expect(screen.getByText('No households on this server yet.')).toBeVisible());
    expect(screen.queryByText('Loading households…')).not.toBeInTheDocument();
    expect(document.body.textContent).not.toMatch(/\boperator\b/i);
  });

  it('shows load errors inline in the admin section', async () => {
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL) => {
      const url = String(input);
      if (url === '/api/operator/households') {
        return Promise.resolve(json({ error: 'nope', code: 'forbidden_operator' }, 403));
      }
      return Promise.reject(new Error(url));
    }));
    renderAdminPage(session(true));
    const section = await screen.findByTestId('operator-households-section');
    expect(await within(section).findByRole('alert')).toHaveTextContent(/^Only an admin can do this\.$/);
    expect(within(section).queryByText('No households on this server yet.')).not.toBeInTheDocument();
    expect(within(section).queryByRole('button', { name: 'Try again' })).not.toBeInTheDocument();
    expect(document.body.textContent).not.toMatch(/\boperator\b/i);
  });

  it('keeps the alert and shows Try again loading while the retry is in flight', async () => {
    let calls = 0;
    let resolveRetry: (value: Response) => void = () => undefined;
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL) => {
      const url = String(input);
      if (url === '/api/operator/households') {
        calls += 1;
        if (calls === 1) return Promise.resolve(json({ error: 'boom', code: 'internal' }, 500));
        return new Promise<Response>((resolve) => { resolveRetry = resolve; });
      }
      return Promise.reject(new Error(url));
    }));
    renderAdminPage(session(true));
    const section = await screen.findByTestId('operator-households-section');
    await within(section).findByRole('alert');
    const tryIdle = within(section).getByRole('button', { name: 'Try again' });
    expect(tryIdle).toHaveAccessibleName('Try again');
    const idleLabels = tryIdle.querySelectorAll('.admin-household-retry-label-stack span');
    expect(idleLabels[0]).not.toHaveAttribute('aria-hidden', 'true');
    expect(idleLabels[1]).toHaveAttribute('aria-hidden', 'true');
    expect(tryIdle.querySelector('.admin-household-retry-label-busy')).not.toBeNull();
    fireEvent.click(tryIdle);
    await waitFor(() => expect(calls).toBe(2));
    const retry = within(section).getByRole('button', { name: 'Trying again…' });
    expect(retry).toHaveAccessibleName('Trying again…');
    await waitFor(() => expect(retry).toHaveAttribute('data-loading', 'true'));
    expect(retry).toHaveAttribute('aria-busy', 'true');
    expect(retry).toBeDisabled();
    const busyLabels = retry.querySelectorAll('.admin-household-retry-label-stack span');
    expect(busyLabels[0]).toHaveAttribute('aria-hidden', 'true');
    expect(busyLabels[1]).not.toHaveAttribute('aria-hidden', 'true');
    const activeBusyLabel = retry.querySelector('.admin-household-retry-label-busy');
    expect(activeBusyLabel).not.toBeNull();
    expect(activeBusyLabel!.querySelector('[aria-hidden="true"]')).not.toBeNull();
    expect(getComputedStyle(activeBusyLabel!)).toMatchObject({ opacity: '1', visibility: 'visible' });
    await waitFor(() => {
      const spinner = activeBusyLabel!.firstElementChild;
      expect(spinner).not.toBeNull();
      expect(String(spinner!.className)).toMatch(/Loader/i);
      expect(spinner).toHaveAttribute('aria-hidden', 'true');
    });
    expect(within(section).getByRole('alert')).toHaveTextContent(SERVER_ERROR_FALLBACK);
    expect(within(section).queryByText('No households on this server yet.')).not.toBeInTheDocument();
    resolveRetry(json({ households: [testHouseholdRow] }));
    await waitFor(() => expect(within(section).queryByRole('alert')).not.toBeInTheDocument());
    expect(within(section).queryByRole('button', { name: 'Try again' })).not.toBeInTheDocument();
  });

  it('renders stat lines with nbsp separators in fixed positions', async () => {
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL) => {
      const url = String(input);
      if (url === '/api/operator/households') return Promise.resolve(json({ households: [testHouseholdRow] }));
      return Promise.reject(new Error(url));
    }));
    renderAdminPage(session(true));
    const stats = await screen.findByTestId('operator-household-stats');
    const statLines = stats.querySelectorAll('.admin-household-stat-line');
    expect(statLines[0].textContent).toBe('1 member\u00a0· 2 products\u00a0· 3 items on hand');
    expect(statLines[1].textContent).toBe('Scanners: 1 online\u00a0· 0 offline');
    expect(statLines[2].textContent).toBe('1 scan this week\u00a0· 0 pending scans');
    expect(statLines[3].textContent).toBe('Last activity Apr 2, 12:00 PM');
  });

  it('formats Created using the viewer local calendar date near UTC midnight', async () => {
    vi.unstubAllEnvs();
    vi.stubEnv('TZ', 'America/Denver');
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL) => {
      const url = String(input);
      if (url === '/api/operator/households') {
        return Promise.resolve(json({ households: [{ ...testHouseholdRow, createdAt: '2026-04-01T03:00:00.000Z' }] }));
      }
      return Promise.reject(new Error(url));
    }));
    renderAdminPage(session(true));
    const section = await screen.findByTestId('operator-households-section');
    expect(within(section).getByText('Created Mar 31', { exact: true })).toBeVisible();
  });

  it('retries after a server error with Try again', async () => {
    let calls = 0;
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL) => {
      const url = String(input);
      if (url === '/api/operator/households') {
        calls += 1;
        if (calls === 1) {
          return Promise.resolve(json({ error: 'boom', code: 'internal' }, 500));
        }
        return Promise.resolve(json({ households: [testHouseholdRow] }));
      }
      return Promise.reject(new Error(url));
    }));
    renderAdminPage(session(true));
    const section = await screen.findByTestId('operator-households-section');
    expect(await within(section).findByRole('alert')).toHaveTextContent(SERVER_ERROR_FALLBACK);
    const retry = within(section).getByRole('button', { name: 'Try again' });
    fireEvent.click(retry);
    await waitFor(() => expect(screen.getByText('Test Household', { selector: '.admin-household-name' })).toBeVisible());
    expect(calls).toBe(2);
    expect(within(section).queryByRole('alert')).not.toBeInTheDocument();
  });

  it('shows the latest error after Try again fails with a different message', async () => {
    let calls = 0;
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL) => {
      const url = String(input);
      if (url === '/api/operator/households') {
        calls += 1;
        if (calls === 1) {
          return Promise.resolve(json({ error: 'boom', code: 'internal' }, 500));
        }
        return Promise.resolve(json({ error: 'nope', code: 'forbidden_operator' }, 403));
      }
      return Promise.reject(new Error(url));
    }));
    renderAdminPage(session(true));
    const section = await screen.findByTestId('operator-households-section');
    expect(await within(section).findByRole('alert')).toHaveTextContent(SERVER_ERROR_FALLBACK);
    fireEvent.click(within(section).getByRole('button', { name: 'Try again' }));
    await waitFor(() => expect(within(section).getByRole('alert')).toHaveTextContent(/^Only an admin can do this\.$/));
    expect(within(section).queryByText(SERVER_ERROR_FALLBACK)).not.toBeInTheDocument();
    expect(within(section).queryByRole('button', { name: 'Try again' })).not.toBeInTheDocument();
    expect(screen.queryByText('Loading households…')).not.toBeInTheDocument();
    expect(calls).toBe(2);
  });

  it('same-instance reload failure clears rows and keeps alert plus Try again', async () => {
    let fail = false;
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL) => {
      const url = String(input);
      if (url === '/api/operator/households') {
        return Promise.resolve(fail
          ? json({ error: 'boom', code: 'internal' }, 500)
          : json({ households: [testHouseholdRow] }));
      }
      return Promise.reject(new Error(url));
    }));
    const view = (auth: AuthSession) => (
      <MantineProvider env="test">
        <MemoryRouter>
          <HouseholdSessionContext.Provider value={{ required: true, logout: vi.fn(), auth, replaceAuth: vi.fn() }}>
            <HouseholdsPage />
          </HouseholdSessionContext.Provider>
        </MemoryRouter>
      </MantineProvider>
    );
    const { rerender } = render(view(session(true)));
    await screen.findByText('Test Household', { selector: '.admin-household-name' });
    rerender(view(session(false)));
    fail = true;
    rerender(view(session(true)));
    const section = await screen.findByTestId('operator-households-section');
    await waitFor(() => expect(within(section).getByRole('alert')).toHaveTextContent(SERVER_ERROR_FALLBACK));
    expect(within(section).getByRole('button', { name: 'Try again' })).toBeEnabled();
    expect(within(section).queryByText('Test Household', { selector: '.admin-household-name' })).not.toBeInTheDocument();
    expect(within(section).queryByTestId('operator-household-stats')).not.toBeInTheDocument();
  });

  it('ignores a stale error after a newer load succeeds', async () => {
    let calls = 0;
    let resolveFirst: (value: Response) => void = () => undefined;
    let resolveSecond: (value: Response) => void = () => undefined;
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL) => {
      const url = String(input);
      if (url !== '/api/operator/households') return Promise.reject(new Error(url));
      calls += 1;
      if (calls === 1) return new Promise<Response>((resolve) => { resolveFirst = resolve; });
      return new Promise<Response>((resolve) => { resolveSecond = resolve; });
    }));
    render(
      <StrictMode>
        <MantineProvider env="test">
          <MemoryRouter>
            <HouseholdSessionContext.Provider value={{ required: true, logout: vi.fn(), auth: session(true), replaceAuth: vi.fn() }}>
              <HouseholdsPage />
            </HouseholdSessionContext.Provider>
          </MemoryRouter>
        </MantineProvider>
      </StrictMode>,
    );
    await waitFor(() => expect(calls).toBeGreaterThanOrEqual(2));
    resolveSecond(json({ households: [testHouseholdRow] }));
    const section = await screen.findByTestId('operator-households-section');
    await screen.findByText('Test Household', { selector: '.admin-household-name' });
    resolveFirst(json({ error: 'boom', code: 'internal' }, 500));
    await waitFor(() => expect(within(section).queryByRole('alert')).not.toBeInTheDocument());
    expect(within(section).getByText('Test Household', { selector: '.admin-household-name' })).toBeVisible();
  });

  it('keeps loading visible until a newer in-flight load settles when an older request finishes first', async () => {
    let calls = 0;
    let resolveFirst: (value: Response) => void = () => undefined;
    let resolveSecond: (value: Response) => void = () => undefined;
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL) => {
      const url = String(input);
      if (url !== '/api/operator/households') return Promise.reject(new Error(url));
      calls += 1;
      if (calls === 1) return new Promise<Response>((resolve) => { resolveFirst = resolve; });
      return new Promise<Response>((resolve) => { resolveSecond = resolve; });
    }));
    render(
      <StrictMode>
        <MantineProvider env="test">
          <MemoryRouter>
            <HouseholdSessionContext.Provider value={{ required: true, logout: vi.fn(), auth: session(true), replaceAuth: vi.fn() }}>
              <HouseholdsPage />
            </HouseholdSessionContext.Provider>
          </MemoryRouter>
        </MantineProvider>
      </StrictMode>,
    );
    await waitFor(() => expect(calls).toBeGreaterThanOrEqual(2));
    expect(screen.getByText('Loading households…')).toBeVisible();
    resolveFirst(json({ households: [{ ...testHouseholdRow, name: 'Stale Household' }] }));
    await waitFor(() => {
      expect(screen.queryByText('Stale Household', { selector: '.admin-household-name' })).not.toBeInTheDocument();
    });
    expect(screen.getByText('Loading households…')).toBeVisible();
    resolveSecond(json({ households: [testHouseholdRow] }));
    await waitFor(() => expect(screen.queryByText('Loading households…')).not.toBeInTheDocument());
    expect(screen.getByText('Test Household', { selector: '.admin-household-name' })).toBeVisible();
  });

  it('does not reset a newer successful load when an older request settles late', async () => {
    let calls = 0;
    let resolveFirst: (value: Response) => void = () => undefined;
    let resolveSecond: (value: Response) => void = () => undefined;
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL) => {
      const url = String(input);
      if (url !== '/api/operator/households') return Promise.reject(new Error(url));
      calls += 1;
      if (calls === 1) return new Promise<Response>((resolve) => { resolveFirst = resolve; });
      return new Promise<Response>((resolve) => { resolveSecond = resolve; });
    }));
    render(
      <StrictMode>
        <MantineProvider env="test">
          <MemoryRouter>
            <HouseholdSessionContext.Provider value={{ required: true, logout: vi.fn(), auth: session(true), replaceAuth: vi.fn() }}>
              <HouseholdsPage />
            </HouseholdSessionContext.Provider>
          </MemoryRouter>
        </MantineProvider>
      </StrictMode>,
    );
    await waitFor(() => expect(calls).toBeGreaterThanOrEqual(2));
    resolveSecond(json({ households: [testHouseholdRow] }));
    const section = await screen.findByTestId('operator-households-section');
    await screen.findByText('Test Household', { selector: '.admin-household-name' });
    expect(screen.queryByText('Loading households…')).not.toBeInTheDocument();
    resolveFirst(json({ error: 'boom', code: 'internal' }, 500));
    await waitFor(() => expect(within(section).queryByRole('alert')).not.toBeInTheDocument());
    expect(within(section).getByText('Test Household', { selector: '.admin-household-name' })).toBeVisible();
    expect(screen.queryByText('Loading households…')).not.toBeInTheDocument();
  });

  it('ignores a stale success after a newer load failed', async () => {
    let calls = 0;
    let resolveFirst: (value: Response) => void = () => undefined;
    let resolveSecond: (value: Response) => void = () => undefined;
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL) => {
      const url = String(input);
      if (url !== '/api/operator/households') return Promise.reject(new Error(url));
      calls += 1;
      if (calls === 1) return new Promise<Response>((resolve) => { resolveFirst = resolve; });
      return new Promise<Response>((resolve) => { resolveSecond = resolve; });
    }));
    render(
      <StrictMode>
        <MantineProvider env="test">
          <MemoryRouter>
            <HouseholdSessionContext.Provider value={{ required: true, logout: vi.fn(), auth: session(true), replaceAuth: vi.fn() }}>
              <HouseholdsPage />
            </HouseholdSessionContext.Provider>
          </MemoryRouter>
        </MantineProvider>
      </StrictMode>,
    );
    await waitFor(() => expect(calls).toBeGreaterThanOrEqual(2));
    resolveSecond(json({ error: 'boom', code: 'internal' }, 500));
    const section = await screen.findByTestId('operator-households-section');
    await waitFor(() => expect(within(section).getByRole('alert')).toHaveTextContent(SERVER_ERROR_FALLBACK));
    expect(within(section).queryByText('Test Household', { selector: '.admin-household-name' })).not.toBeInTheDocument();
    resolveFirst(json({ households: [testHouseholdRow] }));
    await waitFor(() => expect(within(section).queryByText('Test Household', { selector: '.admin-household-name' })).not.toBeInTheDocument());
    expect(within(section).getByRole('alert')).toHaveTextContent(SERVER_ERROR_FALLBACK);
  });

  it('wraps a very long household name without leaking member rows', async () => {
    const longName = 'Test Household '.repeat(12).trim();
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL) => {
      const url = String(input);
      if (url === '/api/operator/households') {
        return Promise.resolve(json(operatorHouseholdsWithLegacyMembers([{
          ...testHouseholdRow,
          name: longName,
          scannersOnline: 0,
          scannersOffline: 0,
          scansThisWeek: 0,
        }], [{
          userId: 'x',
          email: 'leak@example.com',
          name: 'Leak Person',
          role: 'member',
          createdAt: '2026-04-01T00:00:00Z',
        }])));
      }
      return Promise.reject(new Error(url));
    }));
    renderAdminPage(session(true));
    const section = await screen.findByTestId('operator-households-section');
    const title = await screen.findByText(longName);
    expect(title).toHaveClass('admin-household-name');
    expect(screen.getByTestId('operator-household-stats')).toHaveTextContent('Scanners: 0 online · 0 offline');
    expectNoMemberPii(section, ['leak@example.com', 'Leak Person']);
  });
});

describe('households admin touch target scoping', () => {
  beforeEach(() => {
    resetStage1Client();
    vi.restoreAllMocks();
  });

  it('does not mark the manage household page with households-admin-page', async () => {
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL) => {
      const url = String(input);
      if (url.includes('/members')) {
        return Promise.resolve(json({
          members: [
            { userId: 'user-1', email: 'owner@test.com', name: 'Owner', role: 'owner', createdAt: '2026-04-01T00:00:00Z' },
            { userId: 'user-2', email: 'member@test.com', name: 'Member', role: 'member', createdAt: '2026-04-01T00:00:00Z' },
          ],
        }));
      }
      if (url === '/api/invites') return Promise.resolve(json({ invites: [] }));
      if (url === '/api/household/settings') return Promise.resolve(json({ restockMode: 'auto' }));
      if (url === '/api/households/hh-1') {
        return Promise.resolve(json({ id: 'hh-1', name: 'Test Household', role: 'owner', deletedAt: null, purgeAt: null }));
      }
      if (url.includes('/api/devices')) return Promise.resolve(json({ devices: [] }));
      return Promise.reject(new Error(url));
    }));
    renderManagePage(session(false));
    expect(document.querySelector('.households-admin-page')).not.toBeInTheDocument();
    const [remove] = await screen.findAllByRole('button', { name: 'Remove' });
    expect(remove.closest('.households-admin-page')).toBeNull();
  });
});
