import { render, screen, waitFor, within } from '@testing-library/react';
import { MantineProvider } from '@mantine/core';
import { MemoryRouter } from 'react-router-dom';
import { beforeEach, describe, expect, it, vi } from 'vitest';
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

describe('HouseholdsPage admin server list', () => {
  beforeEach(() => {
    resetStage1Client();
    vi.restoreAllMocks();
  });

  it('renders count stats with singular pluralization and no member PII', async () => {
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
    expect(stats.textContent).toMatch(/^1 member · 2 products · 3 items on hand · 1 scanner online · 0 scanners offline · 1 scan this week · 0 pending scans · /);
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
    expect(within(section).getByRole('alert')).toHaveTextContent(/^Only an admin can do this\.$/);
    expect(within(section).queryByText('No households on this server yet.')).not.toBeInTheDocument();
    expect(document.body.textContent).not.toMatch(/\boperator\b/i);
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
    expect(screen.getByText(/0 scanners online/)).toBeVisible();
    expectNoMemberPii(section, ['leak@example.com', 'Leak Person']);
  });
});
