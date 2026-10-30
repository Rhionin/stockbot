import { StrictMode } from 'react';
import { fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { MantineProvider } from '@mantine/core';
import { MemoryRouter } from 'react-router-dom';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { SERVER_ERROR_FALLBACK } from '@stockbot/shared/contract';
import type { AuthSession, OperatorHouseholdView } from '@stockbot/shared/contract';
import { resetStage1Client } from '../../api/client';
import { HouseholdSessionContext } from '../auth/householdSession';
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

const denver = process.env.TZ === 'America/Denver';
const expectedLastActivity = denver ? 'Last activity Apr 2, 6:00 AM' : 'Last activity Apr 2, 12:00 PM';
const expectedCreated = denver ? 'Created Mar 31' : 'Created Apr 1';

describe('HouseholdsPage admin server list', () => {
  beforeEach(() => {
    resetStage1Client();
    vi.restoreAllMocks();
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
    expect(stats).toHaveTextContent('1 member · 2 products · 3 items on hand');
    expect(stats).toHaveTextContent('Scanners: 1 online · 0 offline');
    expect(stats).toHaveTextContent('1 scan this week · 0 pending scans');
    expect(stats).toHaveTextContent(expectedLastActivity);
    expect(section).toHaveTextContent('New items restock automatically');
    expect(section).toHaveTextContent(expectedCreated);
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
    fireEvent.click(within(section).getByRole('button', { name: 'Try again' }));
    await waitFor(() => expect(calls).toBe(2));
    const retry = within(section).getByRole('button', { name: 'Try again' });
    await waitFor(() => expect(retry).toHaveAttribute('data-loading', 'true'));
    expect(retry).toBeDisabled();
    expect(within(section).getByRole('alert')).toHaveTextContent(SERVER_ERROR_FALLBACK);
    expect(within(section).queryByText('No households on this server yet.')).not.toBeInTheDocument();
    resolveRetry(json({ households: [testHouseholdRow] }));
    await waitFor(() => expect(within(section).queryByRole('alert')).not.toBeInTheDocument());
    expect(within(section).queryByRole('button', { name: 'Try again' })).not.toBeInTheDocument();
  });

  it('keeps each stat separator on the end of the previous part', async () => {
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL) => {
      const url = String(input);
      if (url === '/api/operator/households') return Promise.resolve(json({ households: [testHouseholdRow] }));
      return Promise.reject(new Error(url));
    }));
    renderAdminPage(session(true));
    const stats = await screen.findByTestId('operator-household-stats');
    const parts = Array.from(stats.querySelectorAll('.admin-household-stat-part')).map((el) => el.textContent ?? '');
    expect(parts.filter((part) => part.startsWith('·') || part.startsWith(' '))).toEqual([]);
    expect(parts.some((part) => part.endsWith('\u00a0·'))).toBe(true);
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

  it('clears stale households when a reload fails', async () => {
    let shouldFail = false;
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL) => {
      const url = String(input);
      if (url === '/api/operator/households') {
        if (shouldFail) {
          return Promise.resolve(json({ error: 'boom', code: 'internal' }, 500));
        }
        return Promise.resolve(json({ households: [testHouseholdRow] }));
      }
      return Promise.reject(new Error(url));
    }));
    const auth = session(true);
    const sessionValue = { required: true as const, logout: vi.fn(), auth, replaceAuth: vi.fn() };
    const view = (reloadKey: string) => (
      <MantineProvider env="test">
        <MemoryRouter>
          <HouseholdSessionContext.Provider value={sessionValue}>
            <HouseholdsPage key={reloadKey} />
          </HouseholdSessionContext.Provider>
        </MemoryRouter>
      </MantineProvider>
    );
    const { rerender } = render(view('initial'));
    await screen.findByTestId('operator-households-section');
    await screen.findByText('Test Household', { selector: '.admin-household-name' });
    shouldFail = true;
    rerender(view('reload-fail'));
    const sectionAfterReload = await screen.findByTestId('operator-households-section');
    await waitFor(() => expect(within(sectionAfterReload).getByRole('alert')).toHaveTextContent(SERVER_ERROR_FALLBACK));
    expect(within(sectionAfterReload).queryByText('Test Household', { selector: '.admin-household-name' })).not.toBeInTheDocument();
  });

  it('clears stale households when a later in-flight load fails under StrictMode', async () => {
    let calls = 0;
    let resolveSlow: (value: Response) => void = () => undefined;
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL) => {
      const url = String(input);
      if (url !== '/api/operator/households') return Promise.reject(new Error(url));
      calls += 1;
      if (calls === 1) {
        return new Promise<Response>((resolve) => { resolveSlow = resolve; });
      }
      return Promise.resolve(json({ error: 'boom', code: 'internal' }, 500));
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
    const section = await screen.findByTestId('operator-households-section');
    await waitFor(() => expect(calls).toBeGreaterThanOrEqual(2));
    await waitFor(() => expect(within(section).getByRole('alert')).toHaveTextContent(SERVER_ERROR_FALLBACK));
    expect(within(section).queryByText('Test Household', { selector: '.admin-household-name' })).not.toBeInTheDocument();
    resolveSlow(json({ households: [testHouseholdRow] }));
    await waitFor(() => expect(within(section).queryByText('Test Household', { selector: '.admin-household-name' })).not.toBeInTheDocument());
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
