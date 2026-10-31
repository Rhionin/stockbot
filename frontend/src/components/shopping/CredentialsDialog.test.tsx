import { fireEvent, render, screen } from '@testing-library/react';
import { MantineProvider } from '@mantine/core';
import { afterEach, describe, expect, it, vi } from 'vitest';
import type { ProviderInfo } from '@stockbot/shared/types';
import { PROVIDER_APP_CREDENTIALS_FORBIDDEN_MESSAGE } from '@stockbot/shared/providerCredentials';
import { CredentialsDialog } from './CredentialsDialog';

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
  credentialsConfigured: false,
  ...overrides,
});

const renderDialog = (
  row: ProviderInfo,
  onChanged: () => void = () => undefined,
  onAccessDenied: () => void = () => undefined,
) => render(
  <MantineProvider>
    <CredentialsDialog
      provider={row}
      opened
      onClose={() => undefined}
      onChanged={onChanged}
      onAccessDenied={onAccessDenied}
    />
  </MantineProvider>,
);

describe('CredentialsDialog', () => {
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it('saves credentials without showing a stored secret', async () => {
    const onChanged = vi.fn();
    const fetchMock = vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url === '/api/providers/kroger/credentials' && init?.method === 'PUT') {
        expect(init.body).toBe(JSON.stringify({
          clientId: 'ui-client',
          clientSecret: 'typed-once',
          redirectUri: 'https://pantry.example/cb',
          modality: 'PICKUP',
        }));
        return Promise.resolve(new Response(JSON.stringify({
          clientId: 'ui-client',
          redirectUri: 'https://pantry.example/cb',
          modality: 'PICKUP',
          secretSet: true,
          source: 'saved',
        }), { status: 200, headers: { 'Content-Type': 'application/json' } }));
      }
      return Promise.reject(new Error(`Unexpected request: ${url}`));
    });
    vi.stubGlobal('fetch', fetchMock);
    renderDialog(provider({
      credentials: {
        clientId: '',
        redirectUri: '',
        modality: 'PICKUP',
        secretSet: true,
        source: 'none',
      },
    }), onChanged);

    expect(screen.getByLabelText('Client secret')).toHaveValue('');
    expect(screen.getByText('A client secret is saved and is not shown.')).toBeInTheDocument();
    fireEvent.change(screen.getByLabelText('Client ID'), { target: { value: 'ui-client' } });
    fireEvent.change(screen.getByLabelText('Client secret'), { target: { value: 'typed-once' } });
    fireEvent.change(screen.getByLabelText('Redirect URI'), { target: { value: 'https://pantry.example/cb' } });
    fireEvent.click(screen.getByRole('button', { name: 'Save credentials' }));
    await screen.findByRole('button', { name: 'Save credentials' });
    expect(onChanged).toHaveBeenCalled();
  });

  it('clears saved credentials', async () => {
    const onChanged = vi.fn();
    const fetchMock = vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url === '/api/providers/kroger/credentials' && init?.method === 'DELETE') {
        return Promise.resolve(new Response(JSON.stringify({
          clientId: '',
          redirectUri: '',
          modality: 'PICKUP',
          secretSet: false,
          source: 'none',
        }), { status: 200, headers: { 'Content-Type': 'application/json' } }));
      }
      return Promise.reject(new Error(`Unexpected request: ${url}`));
    });
    vi.stubGlobal('fetch', fetchMock);
    renderDialog(provider({
      connectionState: 'connected',
      credentialsConfigured: true,
      credentials: {
        clientId: 'ui-client',
        redirectUri: 'https://pantry.example/cb',
        modality: 'PICKUP',
        secretSet: true,
        source: 'saved',
      },
    }), onChanged);

    expect(screen.getByLabelText('Client ID')).toHaveValue('ui-client');
    fireEvent.click(screen.getByRole('button', { name: 'Clear saved credentials' }));
    await screen.findByRole('button', { name: 'Clear saved credentials' });
    expect(onChanged).toHaveBeenCalled();
    expect(fetchMock).toHaveBeenCalledWith(
      '/api/providers/kroger/credentials',
      expect.objectContaining({ method: 'DELETE' }),
    );
  });

  it('shows the disconnect-only dialog copy for a non-admin', () => {
    render(
      <MantineProvider>
        <CredentialsDialog
          provider={provider({ connectionState: 'connected', credentialsConfigured: true })}
          opened
          onClose={() => undefined}
          onChanged={() => undefined}
          showCredentialForm={false}
          onDisconnect={() => undefined}
        />
      </MantineProvider>,
    );
    expect(screen.getByRole('dialog', { name: 'Disconnect Kroger?' })).toBeInTheDocument();
    expect(screen.getByText(
      'This disconnects your Kroger account for everyone in your household. Nothing on your list is deleted. Sending to your Kroger cart stops until someone reconnects.',
    )).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Cancel' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Disconnect' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Close' })).toBeInTheDocument();
    const cancel = screen.getByRole('button', { name: 'Cancel' });
    const disconnect = screen.getByRole('button', { name: 'Disconnect', exact: true });
    const disconnectBgVar = disconnect.style.getPropertyValue('--button-bg');
    const cancelBgVar = cancel.style.getPropertyValue('--button-bg');
    expect(disconnectBgVar.toLowerCase()).toContain('red');
    expect(cancelBgVar.toLowerCase()).not.toContain('red');
    expect(cancelBgVar).not.toBe(disconnectBgVar);
  });

  it('closes the disconnect-only dialog from Cancel without calling disconnect', () => {
    const onClose = vi.fn();
    const onDisconnect = vi.fn();
    vi.stubGlobal('fetch', vi.fn(() => Promise.reject(new Error('Unexpected fetch'))));
    render(
      <MantineProvider>
        <CredentialsDialog
          provider={provider({ connectionState: 'connected', credentialsConfigured: true })}
          opened
          onClose={onClose}
          onChanged={() => undefined}
          showCredentialForm={false}
          onDisconnect={onDisconnect}
        />
      </MantineProvider>,
    );
    fireEvent.click(screen.getByRole('button', { name: 'Cancel' }));
    expect(onClose).toHaveBeenCalledTimes(1);
    expect(onDisconnect).not.toHaveBeenCalled();
    expect(vi.mocked(fetch)).not.toHaveBeenCalled();
  });

  it('closes the disconnect-only dialog from Close without calling disconnect', () => {
    const onClose = vi.fn();
    const onDisconnect = vi.fn();
    vi.stubGlobal('fetch', vi.fn(() => Promise.reject(new Error('Unexpected fetch'))));
    render(
      <MantineProvider>
        <CredentialsDialog
          provider={provider({ connectionState: 'connected', credentialsConfigured: true })}
          opened
          onClose={onClose}
          onChanged={() => undefined}
          showCredentialForm={false}
          onDisconnect={onDisconnect}
        />
      </MantineProvider>,
    );
    fireEvent.click(screen.getByRole('button', { name: 'Close' }));
    expect(onClose).toHaveBeenCalledTimes(1);
    expect(onDisconnect).not.toHaveBeenCalled();
    expect(vi.mocked(fetch)).not.toHaveBeenCalled();
  });

  it('shows the admin-only alert without the app keys form when access is denied', () => {
    render(
      <MantineProvider>
        <CredentialsDialog
          provider={provider({ connectionState: 'connected', credentialsConfigured: true })}
          opened
          onClose={() => undefined}
          onChanged={() => undefined}
          showCredentialForm={false}
          accessDenied
          onDisconnect={() => undefined}
        />
      </MantineProvider>,
    );
    expect(screen.getByRole('alert')).toHaveTextContent(/^Only the server admin can change the Kroger app keys\.$/);
    expect(screen.queryByRole('form', { name: 'Kroger credentials' })).not.toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Disconnect Kroger' })).toBeInTheDocument();
  });

  it('shows an admin-only message on forbidden_operator and hides access', async () => {
    const onAccessDenied = vi.fn();
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url === '/api/providers/kroger/credentials' && init?.method === 'PUT') {
        return Promise.resolve(new Response(JSON.stringify({
          error: 'Only an admin can do this.',
          code: 'forbidden_operator',
        }), { status: 403, headers: { 'Content-Type': 'application/json' } }));
      }
      return Promise.reject(new Error(`Unexpected request: ${url}`));
    }));
    renderDialog(provider({
      credentials: {
        clientId: '',
        redirectUri: '',
        modality: 'PICKUP',
        secretSet: false,
        source: 'saved',
      },
    }), () => undefined, onAccessDenied);

    fireEvent.change(screen.getByLabelText('Client ID'), { target: { value: 'ui-client' } });
    fireEvent.change(screen.getByLabelText('Client secret'), { target: { value: 'typed-once' } });
    fireEvent.change(screen.getByLabelText('Redirect URI'), { target: { value: 'https://pantry.example/cb' } });
    fireEvent.click(screen.getByRole('button', { name: 'Save credentials' }));
    expect(await screen.findByRole('alert')).toHaveTextContent(PROVIDER_APP_CREDENTIALS_FORBIDDEN_MESSAGE);
    expect(onAccessDenied).toHaveBeenCalledTimes(1);
  });

  it('shows sign-in failure copy on unauthorized, not the admin-only message', async () => {
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url === '/api/providers/kroger/credentials' && init?.method === 'PUT') {
        return Promise.resolve(new Response(JSON.stringify({
          error: 'Sign in required.',
          code: 'unauthorized',
        }), { status: 401, headers: { 'Content-Type': 'application/json' } }));
      }
      return Promise.reject(new Error(`Unexpected request: ${url}`));
    }));
    renderDialog(provider({
      credentials: {
        clientId: '',
        redirectUri: '',
        modality: 'PICKUP',
        secretSet: false,
        source: 'saved',
      },
    }));

    fireEvent.change(screen.getByLabelText('Client ID'), { target: { value: 'ui-client' } });
    fireEvent.change(screen.getByLabelText('Client secret'), { target: { value: 'typed-once' } });
    fireEvent.change(screen.getByLabelText('Redirect URI'), { target: { value: 'https://pantry.example/cb' } });
    fireEvent.click(screen.getByRole('button', { name: 'Save credentials' }));
    const alert = await screen.findByRole('alert');
    expect(alert).toHaveTextContent('Sign in required.');
    expect(alert).not.toHaveTextContent(PROVIDER_APP_CREDENTIALS_FORBIDDEN_MESSAGE);
  });
});
