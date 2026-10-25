import { render, screen } from '@testing-library/react';
import { MantineProvider } from '@mantine/core';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { notifyApiSuccess } from '@stockbot/shared/contract';
import { resetStage1Client } from './api/client';
import App from './App';

class FakeEventSource {
  addEventListener() {}
  close() {}
}

const json = (body: unknown, status = 200) =>
  new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } });

const accountSession = {
  kind: 'account',
  user: { id: 'user-1', email: 'ada@example.com', name: 'Ada' },
  memberships: [{ householdId: 'hh-1', name: 'Pantry', role: 'owner', deletedAt: null }],
  households: [{ id: 'hh-1', name: 'Pantry', role: 'owner', deletedAt: null }],
  activeHouseholdId: 'hh-1',
  operator: false,
};

describe('section tabs', () => {
  afterEach(() => {
    notifyApiSuccess();
    resetStage1Client();
    vi.unstubAllGlobals();
    window.history.pushState({}, '', '/');
  });

  it('opens History from the same tabs as Scan Queue, Inventory, and Shopping List', async () => {
    vi.stubGlobal('EventSource', FakeEventSource);
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL) => {
      const url = String(input);
      if (url === '/api/auth/session') return Promise.resolve(json(accountSession));
      if (url === '/api/config') return Promise.resolve(json({ live: 'sse', minClientVersion: { expo: '1.0.0' }, entry: 'public' }));
      if (url.includes('/api/history')) return Promise.resolve(json({ moves: [] }));
      if (url.includes('/api/providers')) return Promise.resolve(json([]));
      if (url.includes('/api/group-suggestions')) return Promise.resolve(json([]));
      if (url.includes('/api/build')) return Promise.resolve(json({}));
      return Promise.resolve(json({}));
    }));
    window.history.pushState({}, '', '/history');

    render(
      <MantineProvider env="test">
        <App />
      </MantineProvider>,
    );

    const nav = await screen.findByRole('navigation', { name: 'Sections' });
    expect(nav).toHaveTextContent('Scan Queue');
    expect(nav).toHaveTextContent('Inventory');
    expect(nav).toHaveTextContent('Shopping List');
    expect(screen.getByRole('link', { name: 'History' })).toHaveAttribute('aria-current', 'page');
    expect(await screen.findByRole('heading', { name: 'History' })).toBeInTheDocument();
    expect(await screen.findByText(/No stock moves yet/)).toBeInTheDocument();
  });
});
