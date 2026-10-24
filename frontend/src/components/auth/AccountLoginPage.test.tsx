import { fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { MantineProvider } from '@mantine/core';
import { describe, expect, it, vi } from 'vitest';
import {
  CHANGEOVER_SIGN_IN_NOTE,
  HOUSEHOLD_PASSWORD_LINK,
  PASSWORD_TOO_SHORT,
} from '@stockbot/shared/contract';
import { AccountLoginPage } from './AccountLoginPage';
import { HouseholdGate } from './HouseholdGate';

const json = (body: unknown, status = 200) =>
  new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } });

const signedOut = {
  user: null,
  kind: 'signed_out',
  memberships: [],
  households: [],
  activeHouseholdId: null,
  operator: false,
};

const config = (householdPasswordSignIn: boolean) => ({
  live: 'sse',
  minClientVersion: { expo: '1.0.0' },
  householdPasswordSignIn,
  entry: 'public',
});

describe('AccountLoginPage', () => {
  it('asks for email and password and hides the household-password link when that sign-in is off', async () => {
    const fetchMock = vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url === '/api/auth/session') return Promise.resolve(json(signedOut));
      if (url === '/api/config') return Promise.resolve(json(config(false)));
      if (url === '/api/auth/login') {
        expect(JSON.parse(String(init?.body))).toEqual({ email: 'ada@example.com', password: 'long-password' });
        return Promise.resolve(json({
          ...signedOut,
          kind: 'account',
          user: { id: 'user-1', email: 'ada@example.com', name: 'Ada' },
          activeHouseholdId: 'hh-1',
          households: [{ id: 'hh-1', name: 'Pantry', role: 'owner', deletedAt: null }],
          memberships: [{ householdId: 'hh-1', name: 'Pantry', role: 'owner', deletedAt: null }],
        }));
      }
      return Promise.reject(new Error(url));
    });
    vi.stubGlobal('fetch', fetchMock);
    render(
      <MantineProvider>
        <HouseholdGate><h1>Inside</h1></HouseholdGate>
      </MantineProvider>,
    );
    expect(await screen.findByRole('note')).toHaveTextContent(CHANGEOVER_SIGN_IN_NOTE);
    expect(screen.queryByRole('button', { name: HOUSEHOLD_PASSWORD_LINK })).not.toBeInTheDocument();
    fireEvent.change(screen.getByLabelText(/^Email/), { target: { value: 'ada@example.com' } });
    fireEvent.change(screen.getByLabelText(/^Password/), { target: { value: 'short' } });
    fireEvent.click(screen.getByRole('button', { name: 'Sign in' }));
    expect(await screen.findByRole('alert')).toHaveTextContent(PASSWORD_TOO_SHORT);
    expect(fetchMock.mock.calls.some((call) => String(call[0]) === '/api/auth/login')).toBe(false);

    fireEvent.change(screen.getByLabelText(/^Password/), { target: { value: 'long-password' } });
    fireEvent.click(screen.getByRole('button', { name: 'Sign in' }));
    expect(await screen.findByRole('heading', { name: 'Inside' })).toBeVisible();
  });

  it('offers the household password only while that sign-in is still on', async () => {
    const bodies: unknown[] = [];
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url === '/api/auth/login' && init?.body) bodies.push(JSON.parse(String(init.body)));
      if (url === '/api/auth/login') {
        return Promise.resolve(json({
          user: null,
          kind: 'household_password',
          memberships: [{ householdId: 'hh-1', name: 'Pantry', role: 'member', deletedAt: null }],
          households: [{ id: 'hh-1', name: 'Pantry', role: 'member', deletedAt: null }],
          activeHouseholdId: 'hh-1',
          operator: false,
        }));
      }
      return Promise.reject(new Error(url));
    }));
    const onSignedIn = vi.fn();
    render(
      <MantineProvider>
        <AccountLoginPage householdPasswordSignIn onSignedIn={onSignedIn} />
      </MantineProvider>,
    );
    expect(screen.queryByRole('note')).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: HOUSEHOLD_PASSWORD_LINK }));
    const householdForm = screen.getByRole('form', { name: 'Household password' });
    fireEvent.change(within(householdForm).getByLabelText(/^Household password/), { target: { value: 'shared-secret' } });
    fireEvent.click(screen.getByRole('button', { name: 'Sign in' }));
    await waitFor(() => expect(onSignedIn).toHaveBeenCalled());
    expect(bodies).toEqual([{ householdPassword: 'shared-secret' }]);
  });

  it('shows the hosted sentence for a known code', async () => {
    vi.stubGlobal('fetch', vi.fn(() => Promise.resolve(json({
      error: 'ignored',
      code: 'rate_limited',
    }, 429))));
    render(
      <MantineProvider>
        <AccountLoginPage householdPasswordSignIn={false} onSignedIn={vi.fn()} />
      </MantineProvider>,
    );
    fireEvent.change(screen.getByLabelText(/^Email/), { target: { value: 'ada@example.com' } });
    fireEvent.change(screen.getByLabelText(/^Password/), { target: { value: 'long-password' } });
    fireEvent.click(screen.getByRole('button', { name: 'Sign in' }));
    expect(await screen.findByRole('alert')).toHaveTextContent('Too many tries. Wait a moment and try again.');
  });
});
