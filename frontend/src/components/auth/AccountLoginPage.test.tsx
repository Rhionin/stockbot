import { fireEvent, render, screen } from '@testing-library/react';
import { MantineProvider } from '@mantine/core';
import { describe, expect, it, vi } from 'vitest';
import { PASSWORD_TOO_SHORT, SETUP_PATH } from '@stockbot/shared/contract';
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

const config = () => ({
  live: 'sse',
  minClientVersion: { expo: '1.0.0' },
  entry: 'public',
});

describe('AccountLoginPage', () => {
  it('asks for email and password and links to setup', async () => {
    const fetchMock = vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url === '/api/auth/session') return Promise.resolve(json(signedOut));
      if (url === '/api/config') return Promise.resolve(json(config()));
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
    expect(await screen.findByRole('link', { name: 'Set up Stockbot' })).toHaveAttribute('href', SETUP_PATH);
    expect(document.body.textContent).not.toMatch(/pi:8080/);
    fireEvent.change(screen.getByLabelText(/^Email/), { target: { value: 'ada@example.com' } });
    fireEvent.change(screen.getByLabelText(/^Password/), { target: { value: 'short' } });
    fireEvent.click(screen.getByRole('button', { name: 'Sign in' }));
    expect(await screen.findByRole('alert')).toHaveTextContent(PASSWORD_TOO_SHORT);
    expect(fetchMock.mock.calls.some((call) => String(call[0]) === '/api/auth/login')).toBe(false);

    fireEvent.change(screen.getByLabelText(/^Password/), { target: { value: 'long-password' } });
    fireEvent.click(screen.getByRole('button', { name: 'Sign in' }));
    expect(await screen.findByRole('heading', { name: 'Inside' })).toBeVisible();
  });

  it('shows the hosted sentence for a known code', async () => {
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url === '/api/auth/login' && init?.method === 'POST') {
        return Promise.resolve(json({ error: 'ignored', code: 'rate_limited' }, 429));
      }
      return Promise.reject(new Error(url));
    }));
    render(
      <MantineProvider>
        <AccountLoginPage onSignedIn={vi.fn()} />
      </MantineProvider>,
    );
    fireEvent.change(screen.getByLabelText(/^Email/), { target: { value: 'ada@example.com' } });
    fireEvent.change(screen.getByLabelText(/^Password/), { target: { value: 'long-password' } });
    fireEvent.click(screen.getByRole('button', { name: 'Sign in' }));
    expect(await screen.findByRole('alert')).toHaveTextContent('Too many tries. Wait a moment and try again.');
  });
});
