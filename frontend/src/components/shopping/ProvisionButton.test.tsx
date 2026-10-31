import type { ComponentProps } from 'react';
import { fireEvent, render, screen, within } from '@testing-library/react';
import { MantineProvider } from '@mantine/core';
import { describe, expect, it, vi } from 'vitest';
import type { ProviderInfo, ProvisionReport, ShoppingListEntry } from '@stockbot/shared/types';
import { ProvisionButton } from './ProvisionButton';

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
  connectionState: 'connected',
  credentialsConfigured: true,
  ...overrides,
});

const entry = (overrides: Partial<ShoppingListEntry> = {}): ShoppingListEntry => ({
  id: 'sli-1',
  itemId: 'item-1',
  quantity: 2,
  source: 'manual',
  purchasedAt: null,
  ...overrides,
});

const renderButton = (props: Partial<ComponentProps<typeof ProvisionButton>> = {}) => render(
  <MantineProvider>
    <ProvisionButton
      provider={props.provider === undefined ? provider() : props.provider}
      entries={props.entries ?? [entry()]}
      explainEmpty={props.explainEmpty}
      onFinished={props.onFinished ?? (() => undefined)}
    />
  </MantineProvider>,
);

const mockExport = (body: ProvisionReport, status = 200) => {
  vi.stubGlobal('fetch', vi.fn(() => Promise.resolve(new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  }))));
};

describe('ProvisionButton', () => {
  it('explains a disabled send when nothing is staged', () => {
    renderButton({ entries: [], explainEmpty: true });
    expect(screen.getByRole('button', { name: 'Send to Kroger' })).toBeDisabled();
    expect(screen.getByText('Adds these items to your Kroger cart.')).toBeInTheDocument();
    expect(screen.getByText('Build the list before sending it to Kroger.')).toBeInTheDocument();
  });

  it('is enabled for a connected configured provider with something to buy', () => {
    renderButton();
    expect(screen.getByRole('button', { name: 'Send to Kroger' })).toBeEnabled();
  });

  it('stays disabled when disconnected or quantity is zero', () => {
    const { rerender } = renderButton({ provider: provider({ connectionState: 'disconnected' }) });
    expect(screen.getByRole('button', { name: 'Send to Kroger' })).toBeDisabled();

    rerender(
      <MantineProvider>
        <ProvisionButton provider={provider({ credentialsConfigured: false })} entries={[entry()]} onFinished={() => undefined} />
      </MantineProvider>,
    );
    expect(screen.getByRole('button', { name: 'Send to Kroger' })).toBeEnabled();
    expect(screen.getByText('Kroger is unconfigured.')).toBeInTheDocument();

    rerender(
      <MantineProvider>
        <ProvisionButton provider={provider()} entries={[entry({ quantity: 0 })]} onFinished={() => undefined} />
      </MantineProvider>,
    );
    expect(screen.getByRole('button', { name: 'Send to Kroger' })).toBeDisabled();
  });

  it('shows the all-sent summary with cart confirmation copy', async () => {
    mockExport({
      provider: 'kroger',
      exported: 2,
      sentCount: 2,
      results: [
        { entryId: 'a', itemId: '1', name: 'Milk', quantity: 1, status: 'sent' },
        { entryId: 'b', itemId: '2', name: 'Bread', quantity: 1, status: 'sent' },
      ],
    });
    renderButton();
    fireEvent.click(screen.getByRole('button', { name: 'Send to Kroger' }));
    const panel = await screen.findByRole('status', { name: 'Cart send result' });
    expect(within(panel).getByText('2 items sent to your Kroger cart.')).toBeInTheDocument();
    expect(within(panel).getByText(/doesn't confirm each item/)).toBeInTheDocument();
    expect(within(panel).getByText('Sent or added')).toBeInTheDocument();
  });

  it('shows mixed results with not-added reasons for every UX bucket', async () => {
    mockExport({
      provider: 'kroger',
      exported: 1,
      sentCount: 1,
      results: [
        { entryId: 'a', itemId: '1', name: 'Milk', quantity: 1, status: 'sent' },
        { entryId: 'b', itemId: '2', name: 'No UPC', quantity: 1, status: 'skipped', reason: 'no_upc' },
        { entryId: 'c', itemId: '3', name: 'Missing', quantity: 1, status: 'skipped', reason: 'no_match' },
        { entryId: 'd', itemId: '4', name: 'Rejected', quantity: 1, status: 'failed', reason: 'batch_rejected' },
        { entryId: 'e', itemId: '5', name: 'Broken', quantity: 1, status: 'failed', reason: 'api_error' },
      ],
    });
    renderButton();
    fireEvent.click(screen.getByRole('button', { name: 'Send to Kroger' }));
    const panel = await screen.findByRole('status', { name: 'Cart send result' });
    expect(within(panel).getByText(/4 couldn't be sent:/)).toBeInTheDocument();
    expect(within(panel).getByText('No UPC: No barcode saved for this item')).toBeInTheDocument();
    expect(within(panel).getByText("Missing: Couldn't find this at Kroger")).toBeInTheDocument();
    expect(within(panel).getByText("Rejected: Kroger didn't accept this item")).toBeInTheDocument();
    expect(within(panel).getByText('Broken: Something went wrong sending this item')).toBeInTheDocument();
  });

  it('handles unknown reasons without crashing', async () => {
    mockExport({
      provider: 'kroger',
      exported: 0,
      results: [
        { entryId: 'a', itemId: '1', name: 'Weird', quantity: 1, status: 'failed', reason: 'unavailable' },
      ],
    });
    renderButton();
    fireEvent.click(screen.getByRole('button', { name: 'Send to Kroger' }));
    const panel = await screen.findByRole('status', { name: 'Cart send result' });
    expect(within(panel).getByText('Weird: Something went wrong sending this item')).toBeInTheDocument();
  });

  it('shows not confirmed actions and caps unknown rows at fifty', async () => {
    mockExport({
      provider: 'kroger',
      exported: 0,
      unknownCount: 1,
      results: [
        { entryId: 'sli-oats', itemId: 'oats', name: 'Oats', quantity: 1, status: 'unknown', reason: 'no_per_item_result' },
      ],
    });
    renderButton();
    fireEvent.click(screen.getByRole('button', { name: 'Send to Kroger' }));
    const panel = await screen.findByRole('status', { name: 'Cart send result' });
    expect(within(panel).getByText('Not confirmed')).toBeInTheDocument();
    expect(within(panel).getByRole('button', { name: 'It reached Kroger' })).toBeInTheDocument();
    expect(within(panel).getByRole('button', { name: 'It did not' })).toBeInTheDocument();
  });

  it('shows show-all for many skipped items', async () => {
    const skipped = Array.from({ length: 12 }, (_, index) => ({
      entryId: `s-${index}`,
      itemId: `i-${index}`,
      name: `Item ${index}`,
      quantity: 1,
      status: 'skipped' as const,
      reason: 'no_upc' as const,
    }));
    mockExport({ provider: 'kroger', exported: 0, results: skipped });
    renderButton();
    fireEvent.click(screen.getByRole('button', { name: 'Send to Kroger' }));
    const panel = await screen.findByRole('status', { name: 'Cart send result' });
    expect(within(panel).getByRole('button', { name: 'Show all 12' })).toBeInTheDocument();
    fireEvent.click(within(panel).getByRole('button', { name: 'Show all 12' }));
    expect(within(panel).getByText('Item 11: No barcode saved for this item')).toBeInTheDocument();
  });

  it('falls back to legacy failed and unknown lists', async () => {
    mockExport({
      provider: 'kroger',
      exported: 1,
      failedItems: ['Jam'],
      unknownItems: ['Oats'],
      entries: [{ entryId: 'sli-oats', itemId: 'oats', name: 'Oats', quantity: 1, outcome: 'unknown' }],
    });
    renderButton();
    fireEvent.click(screen.getByRole('button', { name: 'Send to Kroger' }));
    const panel = await screen.findByRole('status', { name: 'Cart send result' });
    expect(within(panel).getByText('Jam: Something went wrong sending this item')).toBeInTheDocument();
    expect(within(panel).getByRole('button', { name: 'It reached Kroger' })).toBeInTheDocument();
  });

  it('shows whole-request provider_unavailable with Try again', async () => {
    mockExport({ provider: 'kroger', exported: 0, error: 'provider_unavailable' });
    renderButton();
    fireEvent.click(screen.getByRole('button', { name: 'Send to Kroger' }));
    const panel = await screen.findByRole('status', { name: 'Cart send result' });
    expect(within(panel).getByText("Kroger isn't responding right now. Nothing was sent.")).toBeInTheDocument();
    expect(within(panel).getByRole('button', { name: 'Try again' })).toBeInTheDocument();
  });

  it('shows reconnect_required with Reconnect action', async () => {
    mockExport({ provider: 'kroger', exported: 0, error: 'reconnect_required' });
    renderButton();
    fireEvent.click(screen.getByRole('button', { name: 'Send to Kroger' }));
    const panel = await screen.findByRole('status', { name: 'Cart send result' });
    expect(within(panel).getByText("Couldn't connect to your Kroger account. Nothing was sent.")).toBeInTheDocument();
    expect(within(panel).getByRole('button', { name: 'Reconnect Kroger' })).toBeInTheDocument();
  });

  it('shows HTTP errors with Try again', async () => {
    vi.stubGlobal('fetch', vi.fn(() => Promise.resolve(new Response(JSON.stringify({
      error: 'Kroger is not connected',
      code: 'conflict',
    }), { status: 409, headers: { 'Content-Type': 'application/json' } }))));
    renderButton();
    fireEvent.click(screen.getByRole('button', { name: 'Send to Kroger' }));
    const panel = await screen.findByRole('status', { name: 'Cart send result' });
    expect(within(panel).getByText('Kroger is not connected')).toBeInTheDocument();
    expect(within(panel).getByRole('button', { name: 'Try again' })).toBeInTheDocument();
  });

  it('clears the prior result while sending and labels the button Sending…', async () => {
    let resolveFetch!: (value: Response) => void;
    vi.stubGlobal('fetch', vi.fn(() => new Promise<Response>((resolve) => {
      resolveFetch = resolve;
    })));
    renderButton();
    fireEvent.click(screen.getByRole('button', { name: 'Send to Kroger' }));
    expect(await screen.findByRole('button', { name: 'Sending…' })).toBeDisabled();
    expect(screen.queryByRole('status', { name: 'Cart send result' })).not.toBeInTheDocument();
    resolveFetch(new Response(JSON.stringify({
      provider: 'kroger',
      exported: 1,
      sentCount: 1,
      results: [{ entryId: 'a', itemId: '1', name: 'Milk', quantity: 1, status: 'sent' }],
    }), { status: 200, headers: { 'Content-Type': 'application/json' } }));
    await screen.findByRole('status', { name: 'Cart send result' });
  });

  it('dismisses the result panel', async () => {
    mockExport({
      provider: 'kroger',
      exported: 1,
      sentCount: 1,
      results: [{ entryId: 'a', itemId: '1', name: 'Milk', quantity: 1, status: 'sent' }],
    });
    renderButton();
    fireEvent.click(screen.getByRole('button', { name: 'Send to Kroger' }));
    const panel = await screen.findByRole('status', { name: 'Cart send result' });
    fireEvent.click(within(panel).getByRole('button', { name: 'Dismiss cart send result' }));
    expect(screen.queryByRole('status', { name: 'Cart send result' })).not.toBeInTheDocument();
  });

  it('shows nothing-sent summary for a batch rejection', async () => {
    mockExport({
      provider: 'kroger',
      exported: 0,
      results: [
        { entryId: 'a', itemId: '1', name: 'Soup', quantity: 1, status: 'failed', reason: 'batch_rejected' },
      ],
    });
    renderButton();
    fireEvent.click(screen.getByRole('button', { name: 'Send to Kroger' }));
    const panel = await screen.findByRole('status', { name: 'Cart send result' });
    expect(within(panel).getByText("Nothing was sent to your cart. 1 item couldn't be sent:")).toBeInTheDocument();
  });
});
