import { fireEvent, render, screen, within } from '@testing-library/react';
import { MantineProvider } from '@mantine/core';
import { describe, expect, it, vi } from 'vitest';
import type { ProviderInfo, ShoppingListEntry } from '@stockbot/shared/types';
import { ProvisionButton } from './ProvisionButton';

const provider = (): ProviderInfo => ({
  id: 'kroger',
  displayName: 'Kroger',
  capabilities: {
    auth: 'oauth2_authorization_code',
    delivery: 'server_push',
    confirmation: 'per_request',
    mutation: 'add_only',
    identity: 'derived',
  },
  connectionState: 'connected',
  credentialsConfigured: true,
});

const entry = (): ShoppingListEntry => ({
  id: 'sli-1',
  itemId: 'item-1',
  quantity: 2,
  source: 'manual',
  purchasedAt: null,
});

const renderButton = () => render(
  <MantineProvider>
    <ProvisionButton
      provider={provider()}
      entries={[entry()]}
      onFinished={() => undefined}
    />
  </MantineProvider>,
);

const partialDisconnectLine =
  'Your Kroger account disconnected before everything was sent. Reconnect, then send again to finish.';

const nothingSentDisconnectLine =
  'Your Kroger account disconnected. Reconnect, then send again.';

describe('ProvisionButton account disconnected with nothing sent', () => {
  it('shows nothing-sent header, new disconnected line, reconnect action, and no before-everything copy', async () => {
    vi.stubGlobal('fetch', vi.fn(() => Promise.resolve(new Response(JSON.stringify({
      provider: 'kroger',
      exported: 0,
      reconnectRequired: true,
      results: [
        {
          entryId: 'b',
          itemId: '2',
          name: 'Bread',
          quantity: 1,
          status: 'failed',
          reason: 'reconnect_required',
        },
      ],
    }), {
      status: 200,
      headers: { 'Content-Type': 'application/json' },
    }))));

    renderButton();
    fireEvent.click(screen.getByRole('button', { name: 'Send to Kroger' }));
    const panel = await screen.findByRole('status', { name: 'Cart send result' });

    expect(within(panel).getByText('Nothing was sent to your cart.')).toBeInTheDocument();
    expect(within(panel).getByText(
      'Your Kroger account disconnected. Reconnect, then send again.',
    )).toBeInTheDocument();
    expect(within(panel).getByRole('button', { name: 'Reconnect Kroger' })).toBeInTheDocument();
    expect(panel.textContent).not.toContain('before everything');
  });
});

describe('ProvisionButton account disconnected after partial send', () => {
  it('still shows the existing partial disconnected line when one item was sent', async () => {
    vi.stubGlobal('fetch', vi.fn(() => Promise.resolve(new Response(JSON.stringify({
      provider: 'kroger',
      exported: 1,
      sentCount: 1,
      reconnectRequired: true,
      results: [
        { entryId: 'a', itemId: '1', name: 'Milk', quantity: 1, status: 'sent' },
        { entryId: 'b', itemId: '2', name: 'Bread', quantity: 1, status: 'failed', reason: 'reconnect_required' },
      ],
    }), {
      status: 200,
      headers: { 'Content-Type': 'application/json' },
    }))));

    renderButton();
    fireEvent.click(screen.getByRole('button', { name: 'Send to Kroger' }));
    const panel = await screen.findByRole('status', { name: 'Cart send result' });

    expect(within(panel).getByText(partialDisconnectLine)).toBeInTheDocument();
    expect(within(panel).getByRole('button', { name: 'Reconnect Kroger' })).toBeInTheDocument();
    expect(within(panel).queryByText('Nothing was sent to your cart.')).not.toBeInTheDocument();
  });
});

describe('ProvisionButton reconnect panel sent-count wiring (C3, C5, C6)', () => {
  it('C6: shows nothing-sent disconnected copy when N is 0 and check rows exist (not N+U)', async () => {
    vi.stubGlobal('fetch', vi.fn(() => Promise.resolve(new Response(JSON.stringify({
      provider: 'kroger',
      exported: 0,
      reconnectRequired: true,
      results: [
        {
          entryId: 'c',
          itemId: '3',
          name: 'Butter',
          quantity: 1,
          status: 'unknown',
          reason: 'request_incomplete',
        },
      ],
    }), {
      status: 200,
      headers: { 'Content-Type': 'application/json' },
    }))));

    renderButton();
    fireEvent.click(screen.getByRole('button', { name: 'Send to Kroger' }));
    const panel = await screen.findByRole('status', { name: 'Cart send result' });

    expect(within(panel).getByText('Check these in your cart (1)')).toBeInTheDocument();
    expect(within(panel).getByText('1 might not have reached your cart.')).toBeInTheDocument();
    expect(within(panel).getByText(nothingSentDisconnectLine)).toBeInTheDocument();
    expect(panel.textContent).not.toContain('before everything');
  });

  it('C3/C5: legacy partial send uses groups.N when sentRows and exported disagree', async () => {
    vi.stubGlobal('fetch', vi.fn(() => Promise.resolve(new Response(JSON.stringify({
      provider: 'kroger',
      exported: 0,
      sentCount: 1,
      reconnectRequired: true,
      failedItems: ['Invented Jam'],
    }), {
      status: 200,
      headers: { 'Content-Type': 'application/json' },
    }))));

    renderButton();
    fireEvent.click(screen.getByRole('button', { name: 'Send to Kroger' }));
    const panel = await screen.findByRole('status', { name: 'Cart send result' });

    expect(within(panel).getByText('1 item sent to your Kroger cart.')).toBeInTheDocument();
    expect(within(panel).getByText("Couldn't be sent (1)")).toBeInTheDocument();
    expect(within(panel).getByText('Invented Jam: Reconnect Kroger to send this')).toBeInTheDocument();
    expect(within(panel).getByText(partialDisconnectLine)).toBeInTheDocument();
    expect(within(panel).queryByText(nothingSentDisconnectLine)).not.toBeInTheDocument();
    expect(within(panel).queryByText('Nothing was sent to your cart.')).not.toBeInTheDocument();
  });
});
