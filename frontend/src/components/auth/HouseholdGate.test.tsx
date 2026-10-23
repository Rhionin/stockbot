import { fireEvent, render, screen } from '@testing-library/react';
import { MantineProvider } from '@mantine/core';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { OFFLINE_SENTENCE } from '../../api/contract';
import { HouseholdGate } from './HouseholdGate';

const json = (body: unknown, status = 200) =>
  new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } });

const renderGate = () => render(
  <MantineProvider>
    <HouseholdGate>
      <h1>Scan Queue</h1>
    </HouseholdGate>
  </MantineProvider>,
);

describe('HouseholdGate', () => {
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it('shows the login page when the public site has no session', async () => {
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL) => {
      if (String(input) === '/api/session') return Promise.resolve(json({ error: 'Sign in required.' }, 401));
      return Promise.reject(new Error(String(input)));
    }));
    renderGate();
    expect(await screen.findByRole('form', { name: 'Sign in' })).toBeVisible();
    expect(screen.queryByRole('heading', { name: 'Scan Queue' })).not.toBeInTheDocument();
  });

  it('opens the app on the LAN, where sign-in is not required', async () => {
    vi.stubGlobal('fetch', vi.fn(() => Promise.resolve(json({ required: false }))));
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
    if (message !== OFFLINE_SENTENCE) {
      expect(screen.queryByText(OFFLINE_SENTENCE)).not.toBeInTheDocument();
    }
  });

  it('opens the app after a successful sign-in', async () => {
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url === '/api/session') return Promise.resolve(json({ error: 'Sign in required.' }, 401));
      if (url === '/api/login' && init?.method === 'POST') return Promise.resolve(json({ username: 'pantry' }));
      return Promise.reject(new Error(url));
    }));
    renderGate();
    fireEvent.change(await screen.findByLabelText(/Username/), { target: { value: 'pantry' } });
    fireEvent.change(screen.getByLabelText(/Password/), { target: { value: 'correct-horse-battery' } });
    fireEvent.click(screen.getByRole('button', { name: 'Sign in' }));
    expect(await screen.findByRole('heading', { name: 'Scan Queue' })).toBeVisible();
  });
});
