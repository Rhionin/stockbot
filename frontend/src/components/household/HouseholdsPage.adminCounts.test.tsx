import { render, screen, waitFor, within } from '@testing-library/react';
import { MantineProvider } from '@mantine/core';
import { MemoryRouter } from 'react-router-dom';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import type { AuthSession } from '@stockbot/shared/contract';
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

const testHouseholdRow = {
  id: 'hh-test',
  name: 'Test Household',
  isPrimary: true,
  restockMode: 'auto',
  createdAt: '2026-04-01T00:00:00Z',
  members: [{ userId: 'hidden-1', email: 'secret@example.com', name: 'Secret Person', role: 'owner', createdAt: '2026-04-01T00:00:00Z' }],
  memberCount: 1,
  productCount: 2,
  itemsOnHand: 3,
  pendingScans: 0,
  scannersOnline: 1,
  scannersOffline: 0,
  scansThisWeek: 1,
  lastActivityAt: '2026-04-02T12:00:00Z',
};

describe('HouseholdsPage admin server list', () => {
  beforeEach(() => {
    resetStage1Client();
    vi.restoreAllMocks();
  });

  it('renders count stats with singular pluralization and no member PII', async () => {
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL) => {
      const url = String(input);
      if (url === '/api/operator/households') {
        return Promise.resolve(json({ households: [testHouseholdRow] }));
      }
      return Promise.reject(new Error(url));
    }));
    renderAdminPage(session(true));
    expect(await screen.findByTestId('operator-households-section')).toBeVisible();
    expect(screen.getByText('Test Household', { selector: '.admin-household-name' })).toBeVisible();
    const stats = screen.getByTestId('operator-household-stats');
    expect(stats.textContent).toContain('1 member');
    expect(stats.textContent).toContain('1 scanner online');
    expect(stats.textContent).toContain('0 scanners offline');
    expect(stats.textContent).toContain('1 scan this week');
    expect(screen.queryByText('secret@example.com')).not.toBeInTheDocument();
    expect(screen.queryByText('Secret Person')).not.toBeInTheDocument();
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
    resolveList(json({ households: [] }));
    await waitFor(() => expect(screen.getByText('No households on this server yet.')).toBeVisible());
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
    expect(within(section).getByRole('alert')).toHaveTextContent('Only an admin can do this.');
  });

  it('wraps a very long household name without leaking member rows', async () => {
    const longName = 'Test Household '.repeat(12).trim();
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL) => {
      const url = String(input);
      if (url === '/api/operator/households') {
        return Promise.resolve(json({
          households: [{
            ...testHouseholdRow,
            name: longName,
            scannersOnline: 0,
            scannersOffline: 0,
            scansThisWeek: 0,
            members: [{ userId: 'x', email: 'leak@example.com', name: 'Leak', role: 'member', createdAt: '2026-04-01T00:00:00Z' }],
          }],
        }));
      }
      return Promise.reject(new Error(url));
    }));
    renderAdminPage(session(true));
    const title = await screen.findByText(longName);
    expect(title).toHaveClass('admin-household-name');
    expect(screen.getByText(/0 scanners online/)).toBeVisible();
    expect(screen.queryByText('leak@example.com')).not.toBeInTheDocument();
  });
});
