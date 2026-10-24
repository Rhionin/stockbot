import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { MantineProvider } from '@mantine/core';
import { MemoryRouter, Route, Routes } from 'react-router-dom';
import { describe, expect, it, vi } from 'vitest';
import { InviteAcceptPage } from './InviteAcceptPage';
import { SetupPage } from './SetupPage';
import { HouseholdPage } from '../household/HouseholdPage';
import { HouseholdSessionContext } from './householdSession';
import type { AuthSession } from '@stockbot/shared/contract';

const json = (body: unknown, status = 200) =>
  new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } });

const owner: AuthSession = {
  user: { id: 'user-1', email: 'ada@example.com', name: 'Ada' },
  kind: 'account',
  memberships: [{ householdId: 'hh-1', name: 'Pantry', role: 'owner', deletedAt: null }],
  households: [{ id: 'hh-1', name: 'Pantry', role: 'owner', deletedAt: null }],
  activeHouseholdId: 'hh-1',
  operator: true,
};

describe('setup, invites, and import', () => {
  it('creates the first account from the setup code', async () => {
    const bodies: unknown[] = [];
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url === '/setup' && init?.method === 'POST') {
        bodies.push(JSON.parse(String(init.body)));
        return Promise.resolve(json({}));
      }
      if (url === '/api/config') return Promise.resolve(json({ live: 'sse', minClientVersion: { expo: '1' }, householdPasswordSignIn: false, appBaseUrl: 'https://app.example' }));
      return Promise.reject(new Error(url));
    }));
    render(<MantineProvider><SetupPage /></MantineProvider>);
    fireEvent.change(screen.getByLabelText(/^Setup code/), { target: { value: 'setup-code' } });
    fireEvent.change(screen.getByLabelText(/^Name/), { target: { value: 'Ada' } });
    fireEvent.change(screen.getByLabelText(/^Email/), { target: { value: 'ada@example.com' } });
    fireEvent.change(screen.getByLabelText(/^Password/), { target: { value: 'long-password' } });
    fireEvent.click(screen.getByRole('button', { name: 'Save account' }));
    expect(await screen.findByRole('status')).toHaveTextContent('The account is ready. Sign in at https://app.example.');
    expect(screen.getByRole('link', { name: 'Sign in' })).toHaveAttribute('href', 'https://app.example');
    expect(bodies[0]).toEqual({ setupCode: 'setup-code', name: 'Ada', email: 'ada@example.com', password: 'long-password' });
  });

  it('links back to sign-in on this origin when the server has no public address', async () => {
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url === '/setup' && init?.method === 'POST') return Promise.resolve(json({}));
      if (url === '/api/config') return Promise.reject(new Error('offline'));
      return Promise.reject(new Error(url));
    }));
    render(<MantineProvider><SetupPage /></MantineProvider>);
    fireEvent.change(screen.getByLabelText(/^Setup code/), { target: { value: 'setup-code' } });
    fireEvent.change(screen.getByLabelText(/^Name/), { target: { value: 'Ada' } });
    fireEvent.change(screen.getByLabelText(/^Email/), { target: { value: 'ada@example.com' } });
    fireEvent.change(screen.getByLabelText(/^Password/), { target: { value: 'long-password' } });
    fireEvent.click(screen.getByRole('button', { name: 'Save account' }));
    expect(await screen.findByRole('status')).toHaveTextContent('The account is ready. Sign-in is not set up on this server yet.');
    expect(screen.getByRole('link', { name: 'Sign in' })).toHaveAttribute('href', '/');
  });

  it('accepts a start-household invite with the restock question', async () => {
    const bodies: unknown[] = [];
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url === '/api/invites/preview') {
        return Promise.resolve(json({ kind: 'start_household', role: 'owner', expiresAt: '2026-05-01T00:00:00Z' }));
      }
      if (url === '/api/auth/session') {
        return Promise.resolve(json({ user: null, kind: 'signed_out', memberships: [], households: [], activeHouseholdId: null, operator: false }));
      }
      if (url === '/api/invites/accept') {
        bodies.push(JSON.parse(String(init?.body)));
        return Promise.resolve(json({
          user: { id: 'user-1', email: 'ada@example.com', name: 'Ada' },
          kind: 'account',
          memberships: [],
          households: [],
          activeHouseholdId: 'hh-1',
          operator: false,
          householdId: 'hh-1',
          name: 'Pantry',
          role: 'owner',
        }));
      }
      return Promise.reject(new Error(url));
    }));
    render(
      <MantineProvider>
        <MemoryRouter initialEntries={['/invite/start-code']}>
          <Routes>
            <Route path="/invite/:code" element={<InviteAcceptPage />} />
            <Route path="/" element={<h1>Home</h1>} />
          </Routes>
        </MemoryRouter>
      </MantineProvider>,
    );
    expect(await screen.findByRole('heading', { name: 'Start a household' })).toBeVisible();
    expect(screen.getByLabelText('Ask me before restocking a new item')).toBeInTheDocument();
    fireEvent.change(screen.getByLabelText(/^Name/), { target: { value: 'Ada' } });
    fireEvent.change(screen.getByLabelText(/^Email/), { target: { value: 'ada@example.com' } });
    fireEvent.change(screen.getByLabelText(/^Password/), { target: { value: 'long-password' } });
    fireEvent.change(screen.getByLabelText(/^Household name/), { target: { value: 'Pantry' } });
    fireEvent.click(screen.getByLabelText('Ask me before restocking a new item'));
    fireEvent.click(screen.getByRole('button', { name: 'Create household' }));
    expect(await screen.findByRole('heading', { name: 'Home' })).toBeVisible();
    expect(bodies[0]).toMatchObject({
      code: 'start-code',
      account: { name: 'Ada', email: 'ada@example.com', password: 'long-password' },
      household: { name: 'Pantry', restockMode: 'ask_restock' },
    });
  });

  it('lets a signed-in account start a household from an invite', async () => {
    const bodies: unknown[] = [];
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url === '/api/invites/preview') {
        return Promise.resolve(json({ kind: 'start_household', role: 'owner', expiresAt: '2026-05-01T00:00:00Z' }));
      }
      if (url === '/api/invites/accept') {
        bodies.push(JSON.parse(String(init?.body)));
        return Promise.resolve(json({
          user: { id: 'user-1', email: 'ada@example.com', name: 'Ada' },
          kind: 'account',
          memberships: [],
          households: [],
          activeHouseholdId: 'hh-2',
          operator: false,
          householdId: 'hh-2',
          name: 'Cabin',
          role: 'owner',
        }));
      }
      return Promise.reject(new Error(url));
    }));
    const onAccepted = vi.fn();
    render(
      <MantineProvider>
        <MemoryRouter>
          <InviteAcceptPage code="start-2" signedIn onAccepted={onAccepted} />
        </MemoryRouter>
      </MantineProvider>,
    );
    expect(await screen.findByRole('heading', { name: 'Start a household' })).toBeVisible();
    expect(screen.queryByLabelText(/^Email/)).not.toBeInTheDocument();
    fireEvent.change(screen.getByLabelText(/^Household name/), { target: { value: 'Cabin' } });
    fireEvent.click(screen.getByRole('button', { name: 'Create household' }));
    await waitFor(() => expect(onAccepted).toHaveBeenCalled());
    expect(bodies[0]).toEqual({ code: 'start-2', household: { name: 'Cabin', restockMode: 'auto' } });
  });

  it('imports a file into an empty household', async () => {
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url.includes('/members')) return Promise.resolve(json({ members: [] }));
      if (url === '/api/invites') return Promise.resolve(json({ invites: [] }));
      if (url === '/api/household/settings') return Promise.resolve(json({ restockMode: 'auto', householdPasswordEnabled: false }));
      if (url === '/api/households/hh-1') return Promise.resolve(json({ id: 'hh-1', name: 'Pantry', role: 'owner', deletedAt: null, purgeAt: null }));
      if (url === '/api/household/import/uploads') return Promise.resolve(json({ uploadUrl: 'https://uploads.example/file', key: 'import-key', maxBytes: 33554432 }));
      if (url === 'https://uploads.example/file' && init?.method === 'PUT') return Promise.resolve(new Response(null, { status: 200 }));
      if (url === '/api/import' && init?.method === 'POST') {
        expect(JSON.parse(String(init.body))).toMatchObject({ importId: 'imp-1', key: 'import-key' });
        return Promise.resolve(json({ jobId: 'job-1', importId: 'imp-1', status: 'pending' }));
      }
      if (url === '/api/import/job-1') return Promise.resolve(json({ jobId: 'job-1', importId: 'imp-1', status: 'ready', warnings: [] }));
      return Promise.reject(new Error(url));
    }));
    render(
      <MantineProvider env="test">
        <MemoryRouter>
          <HouseholdSessionContext.Provider value={{ required: true, logout: vi.fn(), auth: owner, replaceAuth: vi.fn() }}>
            <HouseholdPage />
          </HouseholdSessionContext.Provider>
        </MemoryRouter>
      </MantineProvider>,
    );
    expect(await screen.findByRole('button', { name: 'Export household' })).toBeVisible();
    const file = new File([JSON.stringify({ schemaVersion: 1, importId: 'imp-1' })], 'pantry.json', { type: 'application/json' });
    fireEvent.change(screen.getByLabelText('Import file'), { target: { files: [file] } });
    expect(await screen.findByText('Import finished.')).toBeVisible();
  });
});
