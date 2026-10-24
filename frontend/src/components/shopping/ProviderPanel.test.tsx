import { fireEvent, render, screen } from '@testing-library/react';
import { MantineProvider } from '@mantine/core';
import { afterEach, describe, expect, it, vi } from 'vitest';
import type { ProviderInfo } from '@stockbot/shared/types';
import { ProviderPanel } from './ProviderPanel';

const provider = (overrides: Partial<ProviderInfo>): ProviderInfo => ({
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

const renderPanel = (providers: ProviderInfo[], onChanged: () => void = () => undefined) => render(
  <MantineProvider>
    <ProviderPanel providers={providers} onChanged={onChanged} />
  </MantineProvider>,
);

const expectCredentialsStayOffTheRow = () => {
  expect(screen.queryByRole('button', { name: /setup/i })).not.toBeInTheDocument();
  expect(screen.queryByRole('menuitem', { name: /credentials/i })).not.toBeInTheDocument();
  expect(screen.queryByLabelText('Client ID')).not.toBeInTheDocument();
};

describe('ProviderPanel', () => {
  afterEach(() => {
    vi.unstubAllGlobals();
  });
  it('offers Connect when the provider is configured and disconnected', () => {
    renderPanel([provider({ connectionState: 'disconnected' })]);
    expect(screen.getByRole('button', { name: 'Connect' })).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Disconnect' })).not.toBeInTheDocument();
  });

  it('explains an expired connection and offers Reconnect', () => {
    renderPanel([provider({ connectionState: 'reauth_required' })]);
    expect(screen.getByText(/connection expired/i)).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Reconnect' })).toBeInTheDocument();
  });

  it('offers a new-cart control when connected and leaves disconnect off the row', () => {
    renderPanel([provider({ connectionState: 'connected' })]);
    expect(screen.queryByRole('button', { name: 'Disconnect' })).not.toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Start a new cart' })).toBeInTheDocument();
    expect(screen.getByText('This forgets what was already sent and clears checked-off lines.')).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Save credentials' })).not.toBeInTheDocument();
    expectCredentialsStayOffTheRow();
  });

  it('marks an unconfigured provider and hides the connection control', () => {
    renderPanel([provider({ credentialsConfigured: false, connectionState: 'disconnected' })]);
    expect(screen.getByText('unconfigured')).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Connect' })).not.toBeInTheDocument();
    expectCredentialsStayOffTheRow();
  });

  it('shows no connection control when a connection is not required', () => {
    renderPanel([provider({ connectionState: 'not_required' })]);
    expect(screen.queryByRole('button', { name: 'Connect' })).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Disconnect' })).not.toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Start a new cart' })).toBeInTheDocument();
    expect(screen.getByText('This forgets what was already sent and clears checked-off lines.')).toBeInTheDocument();
  });

  it('starts the authorization redirect', async () => {
    const assign = vi.fn();
    vi.stubGlobal('location', { assign });
    vi.stubGlobal('fetch', vi.fn(() => Promise.resolve(new Response(JSON.stringify({
      authorizationUrl: 'https://api.kroger.com/oauth?state=abc',
    }), { status: 200, headers: { 'Content-Type': 'application/json' } }))));
    renderPanel([provider({ connectionState: 'disconnected' })]);
    fireEvent.click(screen.getByRole('button', { name: 'Connect' }));
    await screen.findByRole('button', { name: 'Connect' });
    expect(assign).toHaveBeenCalledWith('https://api.kroger.com/oauth?state=abc');
  });
});
