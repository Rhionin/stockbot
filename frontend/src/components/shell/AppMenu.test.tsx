import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { MantineProvider } from '@mantine/core';
import { MemoryRouter, Route, Routes } from 'react-router-dom';
import { afterEach, describe, expect, it, vi } from 'vitest';
import type { ProviderInfo } from '@stockbot/shared/types';
import { HouseholdSessionContext } from '../auth/householdSession';
import { AppMenu } from './AppMenu';

const provider = (overrides: Partial<ProviderInfo> = {}): ProviderInfo => ({
  id: 'kroger',
  displayName: 'Kroger',
  capabilities: {
    auth: 'oauth2_authorization_code',
    delivery: 'server_push',
    confirmation: 'per_request',
    mutation: 'add_only',
    identity: 'derived',
  },
  connectionState: 'disconnected',
  credentialsConfigured: true,
  ...overrides,
});

const jsonResponse = (body: unknown, status = 200) =>
  new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } });

// env="test" skips the menu fade. While that transition runs the dropdown is
// already in the document at opacity 0, so a visibility check can fail.
const renderMenu = (
  rows: ProviderInfo[],
  onCredentialsChanged: () => void = () => undefined,
  suggestions: { id: string }[] = [],
) => {
  vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL) => {
    const url = String(input);
    if (url === '/api/providers') return Promise.resolve(jsonResponse(rows));
    if (url === '/api/build') return Promise.resolve(jsonResponse({ commit: 'abc123def456' }));
    if (url === '/api/group-suggestions') return Promise.resolve(jsonResponse(suggestions));
    return Promise.reject(new Error(`Unexpected request: ${url}`));
  }));
  return render(
    <MantineProvider env="test">
      <MemoryRouter initialEntries={['/shopping']}>
        <Routes>
          <Route path="/shopping" element={<AppMenu onCredentialsChanged={onCredentialsChanged} />} />
          <Route path="/diagnostics" element={<h1>Diagnostics</h1>} />
          <Route path="/inventory" element={<h1>Inventory</h1>} />
        </Routes>
      </MemoryRouter>
    </MantineProvider>,
  );
};

const openMenu = async () => {
  fireEvent.click(screen.getByRole('button', { name: 'Menu' }));
  return screen.findByRole('menuitem', { name: 'Manage Kroger connection' });
};

describe('AppMenu', () => {
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it('keeps the connection action, diagnostics, and a labeled build section in the menu', async () => {
    renderMenu([provider({ connectionState: 'connected' })]);
    const connection = await openMenu();
    expect(connection).toBeEnabled();
    expect(screen.queryByRole('menuitem', { name: 'Edit Kroger credentials' })).not.toBeInTheDocument();
    expect(screen.queryByRole('menuitem', { name: 'Disconnect Kroger' })).not.toBeInTheDocument();
    expect(screen.getByRole('menuitem', { name: 'Diagnostics' })).toBeInTheDocument();
    const settings = screen.getByRole('menuitem', { name: 'Settings' });
    const build = screen.getByText('Build');
    expect(settings.compareDocumentPosition(build) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
    expect(screen.queryByRole('button', { name: /setup/i })).not.toBeInTheDocument();
    const note = await screen.findByRole('note', { name: 'build abc123def456' });
    await waitFor(() => expect(note).toBeVisible());
    await waitFor(() => expect(screen.getByText('Build')).toBeVisible());
    expect(screen.queryByRole('menuitem', { name: 'build abc123def456' })).not.toBeInTheDocument();
    expect(screen.queryByRole('menuitem', { name: 'Build' })).not.toBeInTheDocument();
    expect(screen.queryByLabelText('Client ID')).not.toBeInTheDocument();
  });

  it('opens credentials from the connection item without leaving the page', async () => {
    renderMenu([provider({ credentialsConfigured: false, connectionState: 'disconnected' })]);
    const connection = await openMenu();
    expect(screen.queryByRole('menuitem', { name: 'Disconnect Kroger' })).not.toBeInTheDocument();
    fireEvent.click(connection);
    expect(await screen.findByRole('dialog', { name: 'Kroger connection' })).toBeInTheDocument();
    expect(screen.getByRole('form', { name: 'Kroger credentials' })).toBeInTheDocument();
    expect(screen.getByLabelText('Client ID')).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Disconnect Kroger' })).not.toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Menu' })).toBeInTheDocument();
  });

  it('reaches disconnect from the connection item only while connected', async () => {
    renderMenu([provider({ connectionState: 'connected' })]);
    fireEvent.click(await openMenu());
    expect(await screen.findByRole('dialog', { name: 'Kroger connection' })).toBeInTheDocument();
    expect(screen.getByLabelText('Client ID')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Disconnect Kroger' })).toBeEnabled();
    expect(screen.getByRole('button', { name: 'Menu' })).toBeInTheDocument();
  });

  it('disconnects a connected store from the connection dialog', async () => {
    const onChanged = vi.fn();
    const fetchMock = vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url === '/api/providers') return Promise.resolve(jsonResponse([provider({ connectionState: 'connected' })]));
      if (url === '/api/providers/kroger/connection' && init?.method === 'DELETE') {
        return Promise.resolve(jsonResponse({}));
      }
      if (url === '/api/build') return Promise.resolve(jsonResponse({ commit: 'abc123def456' }));
      return Promise.reject(new Error(`Unexpected request: ${url}`));
    });
    vi.stubGlobal('fetch', fetchMock);
    render(
      <MantineProvider env="test">
        <MemoryRouter>
          <AppMenu onCredentialsChanged={onChanged} />
        </MemoryRouter>
      </MantineProvider>,
    );

    fireEvent.click(screen.getByRole('button', { name: 'Menu' }));
    fireEvent.click(await screen.findByRole('menuitem', { name: 'Manage Kroger connection' }));
    fireEvent.click(await screen.findByRole('button', { name: 'Disconnect Kroger' }));

    await waitFor(() => expect(onChanged).toHaveBeenCalledTimes(1));
    expect(fetchMock).toHaveBeenCalledWith('/api/providers/kroger/connection', expect.objectContaining({ method: 'DELETE' }));
    await waitFor(() => expect(screen.queryByRole('button', { name: 'Disconnect Kroger' })).not.toBeInTheDocument());
    expect(screen.queryByRole('dialog', { name: 'Kroger connection' })).not.toBeInTheDocument();
  });

  it('reports a disconnect failure and leaves the connection dialog open', async () => {
    const onChanged = vi.fn();
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url === '/api/providers') return Promise.resolve(jsonResponse([provider({ connectionState: 'connected' })]));
      if (url === '/api/providers/kroger/connection' && init?.method === 'DELETE') {
        return Promise.resolve(jsonResponse({ error: 'Kroger could not be disconnected' }, 500));
      }
      if (url === '/api/build') return Promise.resolve(jsonResponse({ commit: 'abc123def456' }));
      return Promise.reject(new Error(`Unexpected request: ${url}`));
    }));
    render(
      <MantineProvider env="test">
        <MemoryRouter>
          <AppMenu onCredentialsChanged={onChanged} />
        </MemoryRouter>
      </MantineProvider>,
    );

    fireEvent.click(screen.getByRole('button', { name: 'Menu' }));
    fireEvent.click(await screen.findByRole('menuitem', { name: 'Manage Kroger connection' }));
    fireEvent.click(await screen.findByRole('button', { name: 'Disconnect Kroger' }));

    expect(await screen.findByRole('alert')).toHaveTextContent('Kroger could not be disconnected');
    expect(onChanged).not.toHaveBeenCalled();
    expect(screen.getByRole('dialog', { name: 'Kroger connection' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Disconnect Kroger' })).toBeInTheDocument();
    expect(screen.getByLabelText('Client ID')).toBeInTheDocument();
  });

  it('shows the open suggestion count on the menu and opens product groups', async () => {
    renderMenu([provider()], () => undefined, [{ id: 'a' }, { id: 'b' }]);
    expect(await screen.findByText('2')).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: 'Menu' }));
    fireEvent.click(await screen.findByRole('menuitem', { name: 'Look for more groups' }));
    expect(await screen.findByRole('heading', { name: 'Inventory' })).toBeInTheDocument();
  });

  it('reviews pending group suggestions from the first menu item', async () => {
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL) => {
      const url = String(input);
      if (url === '/api/providers') return Promise.resolve(jsonResponse([provider()]));
      if (url === '/api/build') return Promise.resolve(jsonResponse({ commit: 'abc123def456' }));
      if (url === '/api/group-suggestions') return Promise.resolve(jsonResponse([{ id: 'a' }, { id: 'b' }]));
      return Promise.reject(new Error(`Unexpected request: ${url}`));
    }));
    render(
      <MantineProvider env="test">
        <MemoryRouter initialEntries={['/shopping']}>
          <AppMenu onCredentialsChanged={() => undefined} />
          <Routes>
            <Route path="/inventory" element={<h1>Inventory</h1>} />
          </Routes>
        </MemoryRouter>
      </MantineProvider>,
    );

    expect(await screen.findByText('2')).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: 'Menu' }));
    const review = await screen.findByRole('menuitem', { name: 'Review 2 group suggestions' });
    const items = screen.getAllByRole('menuitem');
    expect(items[0]).toBe(review);
    expect(review).toHaveTextContent('2');
    expect(screen.getAllByText('2')).toHaveLength(2);
    expect(screen.getByRole('menuitem', { name: 'Look for more groups' })).toBeInTheDocument();

    fireEvent.click(review);
    expect(await screen.findByRole('heading', { name: 'Inventory' })).toBeInTheDocument();
    await waitFor(() => {
      expect(screen.queryByRole('menuitem', { name: 'Look for more groups' })).not.toBeInTheDocument();
    });
    expect(screen.getByRole('button', { name: 'Menu' })).toBeInTheDocument();
  });

  it('uses a singular label for one group suggestion', async () => {
    renderMenu([provider()], () => undefined, [{ id: 'only' }]);
    fireEvent.click(screen.getByRole('button', { name: 'Menu' }));
    const review = await screen.findByRole('menuitem', { name: 'Review 1 group suggestion' });
    expect(review).toHaveTextContent('1');
    expect(screen.getAllByText('1')).toHaveLength(2);
    expect(screen.getAllByRole('menuitem')[0]).toBe(review);
  });

  it('hides the suggestion review when nothing is pending', async () => {
    renderMenu([provider()]);
    fireEvent.click(screen.getByRole('button', { name: 'Menu' }));
    await screen.findByRole('menuitem', { name: 'Look for more groups' });
    expect(screen.queryByRole('menuitem', { name: /group suggestion/i })).not.toBeInTheDocument();
    expect(screen.queryByText('0')).not.toBeInTheDocument();
  });

  it('offers log out when the public site required a sign-in', async () => {
    const logout = vi.fn();
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL) => {
      const url = String(input);
      if (url === '/api/providers') return Promise.resolve(jsonResponse([provider()]));
      if (url === '/api/build') return Promise.resolve(jsonResponse({ commit: 'abc123def456' }));
      if (url === '/api/group-suggestions') return Promise.resolve(jsonResponse([]));
      return Promise.reject(new Error(`Unexpected request: ${url}`));
    }));
    render(
      <MantineProvider env="test">
        <HouseholdSessionContext.Provider value={{ required: true, logout }}>
          <MemoryRouter>
            <AppMenu onCredentialsChanged={() => undefined} />
          </MemoryRouter>
        </HouseholdSessionContext.Provider>
      </MantineProvider>,
    );
    fireEvent.click(screen.getByRole('button', { name: 'Menu' }));
    const build = await screen.findByText('Build');
    const note = await screen.findByRole('note', { name: 'build abc123def456' });
    const logOut = await screen.findByRole('menuitem', { name: 'Log out' });
    await waitFor(() => expect(note).toBeVisible());
    expect(build.compareDocumentPosition(logOut) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
    fireEvent.click(logOut);
    expect(logout).toHaveBeenCalledTimes(1);
  });

  it('hides log out on the LAN listener', async () => {
    renderMenu([provider()]);
    fireEvent.click(screen.getByRole('button', { name: 'Menu' }));
    await screen.findByRole('menuitem', { name: 'Look for more groups' });
    expect(screen.queryByRole('menuitem', { name: 'Log out' })).not.toBeInTheDocument();
  });

  it('opens diagnostics from the menu', async () => {
    renderMenu([provider()]);
    await openMenu();
    fireEvent.click(screen.getByRole('menuitem', { name: 'Diagnostics' }));
    expect(await screen.findByRole('heading', { name: 'Diagnostics' })).toBeInTheDocument();
  });
});
