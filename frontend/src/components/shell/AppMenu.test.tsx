import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { MantineProvider } from '@mantine/core';
import { MemoryRouter, Route, Routes } from 'react-router-dom';
import { afterEach, describe, expect, it, vi } from 'vitest';
import type { AuthSession } from '@stockbot/shared/contract';
import { HouseholdSessionContext } from '../auth/householdSession';
import { AppMenu } from './AppMenu';

const jsonResponse = (body: unknown, status = 200) =>
  new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } });

const renderMenu = (suggestions: { id: string }[] = []) => {
  vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL) => {
    const url = String(input);
    if (url === '/api/build') return Promise.resolve(jsonResponse({ commit: 'abc123def456' }));
    if (url === '/api/group-suggestions') return Promise.resolve(jsonResponse(suggestions));
    return Promise.reject(new Error(`Unexpected request: ${url}`));
  }));
  return render(
    <MantineProvider env="test">
      <HouseholdSessionContext.Provider value={{
        required: false,
        logout: () => undefined,
        auth: accountSession(),
        replaceAuth: () => undefined,
      }}>
        <MemoryRouter initialEntries={['/shopping']}>
          <Routes>
            <Route path="/shopping" element={<AppMenu onCredentialsChanged={() => undefined} />} />
            <Route path="/diagnostics" element={<h1>Diagnostics</h1>} />
            <Route path="/inventory" element={<h1>Inventory</h1>} />
          </Routes>
        </MemoryRouter>
      </HouseholdSessionContext.Provider>
    </MantineProvider>,
  );
};

const accountSession = (): AuthSession => ({
  kind: 'account',
  user: { id: 'user-1', email: 'owner@test.example', name: 'Owner' },
  memberships: [{ householdId: 'hh-test', name: 'Test Household', role: 'owner', deletedAt: null }],
  households: [{ id: 'hh-test', name: 'Test Household', role: 'owner', deletedAt: null }],
  activeHouseholdId: 'hh-test',
  operator: false,
});

describe('AppMenu', () => {
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it('does not offer Kroger credential management in the menu', async () => {
    renderMenu();
    fireEvent.click(screen.getByRole('button', { name: 'Menu' }));
    await screen.findByRole('menuitem', { name: 'Diagnostics' });
    expect(screen.queryByRole('menuitem', { name: 'Manage Kroger connection' })).not.toBeInTheDocument();
    expect(screen.queryByRole('menuitem', { name: 'Disconnect Kroger' })).not.toBeInTheDocument();
  });

  it('keeps diagnostics, settings, and the build stamp reachable', async () => {
    renderMenu();
    fireEvent.click(screen.getByRole('button', { name: 'Menu' }));
    expect(await screen.findByRole('menuitem', { name: 'Diagnostics' })).toBeInTheDocument();
    expect(screen.getByRole('menuitem', { name: 'Settings' })).toBeInTheDocument();
    const note = await screen.findByRole('note', { name: 'build abc123def456' });
    await waitFor(() => expect(note).toBeVisible());
  });

  it('shows the suggestion review entry when suggestions exist', async () => {
    renderMenu([{ id: 's-1' }]);
    fireEvent.click(screen.getByRole('button', { name: 'Menu' }));
    expect(await screen.findByRole('menuitem', { name: 'Review 1 group suggestion' })).toBeInTheDocument();
  });
});
