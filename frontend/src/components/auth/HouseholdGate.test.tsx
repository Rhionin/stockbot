import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import { act, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { MantineProvider } from '@mantine/core';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { OFFLINE_SENTENCE, SESSION_EXPIRED_SENTENCE, notifyApiSuccess } from '@stockbot/shared/contract';
import { listProducts } from '../../api/client';
import { HouseholdGate } from './HouseholdGate';

const authNotConfigured = JSON.parse(
  readFileSync(join(process.cwd(), '../testdata/contract/auth_not_configured.json'), 'utf8'),
) as { code: string };

const AUTH_BANNER = "Sign-in isn't set up yet.";
const SETUP_SENTENCE = "Whoever runs this server needs to finish setting it up.";

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

const accountSession = {
  kind: 'account',
  user: { id: 'user-1', email: 'ada@example.com', name: 'Ada' },
  memberships: [{ householdId: 'hh-1', name: 'Pantry', role: 'owner', deletedAt: null }],
  households: [{ id: 'hh-1', name: 'Pantry', role: 'owner', deletedAt: null }],
  activeHouseholdId: 'hh-1',
  operator: false,
};

const renderGate = () => render(
  <MantineProvider>
    <HouseholdGate>
      <h1>Scan Queue</h1>
    </HouseholdGate>
  </MantineProvider>,
);

describe('HouseholdGate', () => {
  afterEach(() => {
    notifyApiSuccess();
    vi.useRealTimers();
    vi.unstubAllGlobals();
  });

  it('shows the account sign-in page when there is no session', async () => {
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL) => {
      if (String(input) === '/api/auth/session') return Promise.resolve(json(signedOut));
      return Promise.reject(new Error(String(input)));
    }));
    renderGate();
    expect(await screen.findByRole('form', { name: 'Sign in' })).toBeVisible();
    expect(screen.queryByRole('heading', { name: 'Scan Queue' })).not.toBeInTheDocument();
  });

  it('opens the app when an account session is already present', async () => {
    vi.stubGlobal('fetch', vi.fn(() => Promise.resolve(json(accountSession))));
    renderGate();
    expect(await screen.findByRole('heading', { name: 'Scan Queue' })).toBeVisible();
    expect(screen.queryByRole('form', { name: 'Sign in' })).not.toBeInTheDocument();
  });

  it.each([
    { name: 'failed to fetch', error: new TypeError('Failed to fetch'), message: OFFLINE_SENTENCE },
    { name: 'abort', error: new DOMException('The operation was aborted.', 'AbortError'), message: OFFLINE_SENTENCE },
    { name: 'invalid header', error: new TypeError("Failed to construct 'Headers': Invalid name"), message: 'Stockbot could not be reached.' },
    { name: 'other error', error: new Error('Network error'), message: 'Stockbot could not be reached.' },
  ])('session $name shows $message', async ({ error, message }) => {
    vi.stubGlobal('fetch', vi.fn(() => Promise.reject(error)));
    renderGate();
    expect(await screen.findByText(message)).toBeVisible();
    if (message === OFFLINE_SENTENCE) {
      expect(screen.queryByRole('form', { name: 'Sign in' })).not.toBeInTheDocument();
      const brand = screen.getByText('Stockbot');
      const main = brand.closest('main');
      expect(main).toHaveClass('login-screen--offline');
      expect(main?.firstElementChild).toHaveAttribute('data-testid', 'server-notice-banner');
      expect(screen.getAllByText(OFFLINE_SENTENCE)).toHaveLength(1);
    } else {
      expect(screen.queryByText(OFFLINE_SENTENCE)).not.toBeInTheDocument();
      expect(screen.getByRole('form', { name: 'Sign in' })).toBeVisible();
    }
  });

  it('retries after offline and then shows sign-in', async () => {
    let calls = 0;
    vi.stubGlobal('fetch', vi.fn(() => {
      calls += 1;
      if (calls === 1) return Promise.reject(new TypeError('Failed to fetch'));
      return Promise.resolve(json(signedOut));
    }));
    renderGate();
    expect(await screen.findByText(OFFLINE_SENTENCE)).toBeVisible();
    expect(screen.queryByRole('form', { name: 'Sign in' })).not.toBeInTheDocument();
    expect(await screen.findByRole('form', { name: 'Sign in' }, { timeout: 3000 })).toBeVisible();
    expect(screen.queryByText(OFFLINE_SENTENCE)).not.toBeInTheDocument();
  });

  it('waits longer between later retries and starts again at one second', async () => {
    vi.useFakeTimers();
    let calls = 0;
    vi.stubGlobal('fetch', vi.fn(() => {
      calls += 1;
      if (calls < 4) return Promise.reject(new TypeError('Failed to fetch'));
      return Promise.resolve(json(accountSession));
    }));
    renderGate();
    await act(async () => { await Promise.resolve(); });
    expect(calls).toBe(1);
    await act(async () => { await vi.advanceTimersByTimeAsync(999); });
    expect(calls).toBe(1);
    await act(async () => { await vi.advanceTimersByTimeAsync(1); });
    expect(calls).toBe(2);
    await act(async () => { await vi.advanceTimersByTimeAsync(1999); });
    expect(calls).toBe(2);
    await act(async () => { await vi.advanceTimersByTimeAsync(1); });
    expect(calls).toBe(3);
    await act(async () => { await vi.advanceTimersByTimeAsync(3999); });
    expect(calls).toBe(3);
    await act(async () => { await vi.advanceTimersByTimeAsync(1); });
    expect(screen.getByRole('heading', { name: 'Scan Queue' })).toBeVisible();
  });

  it('caps the retry wait at 30 seconds', async () => {
    vi.useFakeTimers();
    let calls = 0;
    vi.stubGlobal('fetch', vi.fn(() => {
      calls += 1;
      return Promise.reject(new TypeError('Failed to fetch'));
    }));
    renderGate();
    await act(async () => { await Promise.resolve(); });
    const gaps = [1000, 2000, 4000, 8000, 16000, 30000, 30000];
    for (const gap of gaps) {
      const seen = calls;
      await act(async () => { await vi.advanceTimersByTimeAsync(gap - 1); });
      expect(calls).toBe(seen);
      await act(async () => { await vi.advanceTimersByTimeAsync(1); });
      expect(calls).toBe(seen + 1);
    }
  });

  it('starts the next retry at one second after a session response', async () => {
    vi.useFakeTimers();
    let calls = 0;
    vi.stubGlobal('fetch', vi.fn(() => {
      calls += 1;
      if (calls === 2) {
        return Promise.resolve(json({ error: AUTH_BANNER, code: authNotConfigured.code }, 503));
      }
      return Promise.reject(new TypeError('Failed to fetch'));
    }));
    renderGate();
    await act(async () => { await Promise.resolve(); });
    expect(calls).toBe(1);
    await act(async () => { await vi.advanceTimersByTimeAsync(1000); });
    expect(calls).toBe(2);
    expect(screen.getByRole('button', { name: 'Try again' })).toBeVisible();
    fireEvent.click(screen.getByRole('button', { name: 'Try again' }));
    await act(async () => { await Promise.resolve(); });
    expect(calls).toBe(3);
    await act(async () => { await vi.advanceTimersByTimeAsync(999); });
    expect(calls).toBe(3);
    await act(async () => { await vi.advanceTimersByTimeAsync(1); });
    expect(calls).toBe(4);
  });

  it.each([
    { next: 'form' },
    { next: 'stay' },
    { next: 'offline' },
  ])('hides the form when auth is not configured and Try again handles $next', async ({ next }) => {
    let sessionCalls = 0;
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL) => {
      const url = String(input);
      if (!url.endsWith('/api/auth/session')) return Promise.reject(new Error(url));
      sessionCalls += 1;
      if (sessionCalls === 1 || (next === 'stay' && sessionCalls < 3)) {
        return Promise.resolve(json({ error: AUTH_BANNER, code: authNotConfigured.code }, 503));
      }
      if (next === 'form') return Promise.resolve(json(signedOut));
      if (next === 'stay') return Promise.resolve(json({ error: AUTH_BANNER, code: authNotConfigured.code }, 503));
      return Promise.reject(new TypeError('Failed to fetch'));
    }));
    renderGate();
    expect(await screen.findByRole('button', { name: 'Try again' })).toBeVisible();
    expect(screen.queryByRole('form', { name: 'Sign in' })).not.toBeInTheDocument();
    expect(screen.getByText(AUTH_BANNER)).toBeVisible();
    expect(screen.getByText(SETUP_SENTENCE)).toBeVisible();
    expect(document.querySelector('a[href*="/setup"]')).toBeNull();

    const before = sessionCalls;
    fireEvent.click(screen.getByRole('button', { name: 'Try again' }));
    await waitFor(() => expect(sessionCalls).toBe(before + 1));
    if (next === 'form') {
      expect(await screen.findByRole('form', { name: 'Sign in' })).toBeVisible();
      expect(screen.queryByText(AUTH_BANNER)).not.toBeInTheDocument();
    } else if (next === 'offline') {
      expect(await screen.findByText(OFFLINE_SENTENCE)).toBeVisible();
      expect(screen.queryByRole('form', { name: 'Sign in' })).not.toBeInTheDocument();
    } else {
      expect(screen.getByText(AUTH_BANNER)).toBeVisible();
      expect(screen.queryByRole('form', { name: 'Sign in' })).not.toBeInTheDocument();
    }
  });

  it('shows the offline banner above the sign-in form', async () => {
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url.endsWith('/api/auth/session')) return Promise.resolve(json(signedOut));
      if (url.endsWith('/api/auth/login') && init?.method === 'POST') return Promise.reject(new TypeError('Failed to fetch'));
      return Promise.reject(new Error(url));
    }));
    renderGate();
    fireEvent.change(await screen.findByLabelText(/^Email/), { target: { value: 'ada@example.com' } });
    fireEvent.change(screen.getByLabelText(/^Password/), { target: { value: 'correct-horse-battery' } });
    fireEvent.click(screen.getByRole('button', { name: 'Sign in' }));
    const banner = await screen.findByTestId('server-notice-banner');
    expect(banner).toHaveTextContent(OFFLINE_SENTENCE);
    const form = screen.getByRole('form', { name: 'Sign in' });
    expect(form).toBeVisible();
    expect(form.contains(banner)).toBe(false);
  });

  it('shows the session sentence when a signed-in request is rejected', async () => {
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL) => {
      const url = String(input);
      if (url.endsWith('/api/auth/session')) return Promise.resolve(json(accountSession));
      if (url.endsWith('/api/products')) {
        return Promise.resolve(json({ error: 'Sign in required.', code: 'unauthorized' }, 401));
      }
      return Promise.resolve(json([]));
    }));
    render(
      <MantineProvider>
        <HouseholdGate>
          <button type="button" onClick={() => { void listProducts().catch(() => undefined); }}>Load products</button>
        </HouseholdGate>
      </MantineProvider>,
    );
    fireEvent.click(await screen.findByRole('button', { name: 'Load products' }));
    expect(await screen.findByRole('alert')).toHaveTextContent(SESSION_EXPIRED_SENTENCE);
    expect(screen.queryByRole('button', { name: 'Load products' })).not.toBeInTheDocument();
  });

  it('opens the app after a successful sign-in', async () => {
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url === '/api/auth/session') return Promise.resolve(json(signedOut));
      if (url === '/api/auth/login' && init?.method === 'POST') return Promise.resolve(json(accountSession));
      return Promise.reject(new Error(url));
    }));
    renderGate();
    fireEvent.change(await screen.findByLabelText(/^Email/), { target: { value: 'ada@example.com' } });
    fireEvent.change(screen.getByLabelText(/^Password/), { target: { value: 'correct-horse-battery' } });
    fireEvent.click(screen.getByRole('button', { name: 'Sign in' }));
    expect(await screen.findByRole('heading', { name: 'Scan Queue' })).toBeVisible();
  });
});
