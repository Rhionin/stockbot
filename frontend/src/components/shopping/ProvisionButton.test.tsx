import type { ComponentProps } from 'react';
import { act, fireEvent, render, screen, within } from '@testing-library/react';
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
    expect(screen.getByText('Sends these items to your Kroger cart.')).toBeInTheDocument();
    expect(screen.getByText('Build the list before sending it to Kroger.')).toBeInTheDocument();
  });

  it('labels the button with Smith\'s or the your-store fallback', () => {
    renderButton({ provider: provider({ displayName: "Smith's" }) });
    expect(screen.getByRole('button', { name: "Send to Smith's" })).toBeInTheDocument();
    renderButton({ provider: null });
    expect(screen.getByRole('button', { name: 'Send to your store' })).toBeInTheDocument();
  });

  it('shows summary lines only for non-zero counts', async () => {
    mockExport({
      provider: 'kroger',
      exported: 1,
      results: [
        { entryId: 'a', itemId: '1', name: 'Milk', quantity: 1, status: 'sent' },
        { entryId: 'e', itemId: '5', name: 'Oats', quantity: 1, status: 'unknown', reason: 'no_per_item_result' },
      ],
    });
    renderButton();
    fireEvent.click(screen.getByRole('button', { name: 'Send to Kroger' }));
    const panel = await screen.findByRole('status', { name: 'Cart send result' });
    expect(within(panel).getByText('1 item sent to your Kroger cart.')).toBeInTheDocument();
    expect(within(panel).getByText('1 might not have reached your cart.')).toBeInTheDocument();
    expect(within(panel).queryByText(/couldn't be sent/)).not.toBeInTheDocument();
    expect(within(panel).queryByText('Nothing was sent to your cart.')).not.toBeInTheDocument();
  });

  it('lists sent item names and hides empty sections', async () => {
    mockExport({
      provider: 'kroger',
      exported: 2,
      results: [
        { entryId: 'a', itemId: '1', name: 'Milk', quantity: 1, status: 'sent' },
        { entryId: 'b', itemId: '2', name: 'Bread', quantity: 1, status: 'sent' },
      ],
    });
    renderButton();
    fireEvent.click(screen.getByRole('button', { name: 'Send to Kroger' }));
    const panel = await screen.findByRole('status', { name: 'Cart send result' });
    expect(within(panel).getByText('Sent (2)')).toBeInTheDocument();
    expect(within(panel).getByText('Milk')).toBeInTheDocument();
    expect(within(panel).queryByText("Couldn't be sent")).not.toBeInTheDocument();
    expect(within(panel).queryByText('Check these in your cart')).not.toBeInTheDocument();
  });

  it('shows unavailable under Couldn\'t be sent', async () => {
    mockExport({
      provider: 'kroger',
      exported: 0,
      results: [
        { entryId: 'a', itemId: '1', name: 'Soup', quantity: 1, status: 'skipped', reason: 'unavailable' },
      ],
    });
    renderButton();
    fireEvent.click(screen.getByRole('button', { name: 'Send to Kroger' }));
    const panel = await screen.findByRole('status', { name: 'Cart send result' });
    expect(within(panel).getByText("Couldn't be sent (1)")).toBeInTheDocument();
    expect(within(panel).getByText('Soup: Not available at Kroger right now')).toBeInTheDocument();
  });

  it('puts no_per_item_result in the check section', async () => {
    mockExport({
      provider: 'kroger',
      exported: 0,
      results: [
        { entryId: 'sli-oats', itemId: 'oats', name: 'Oats', quantity: 1, status: 'unknown', reason: 'no_per_item_result' },
      ],
    });
    renderButton();
    fireEvent.click(screen.getByRole('button', { name: 'Send to Kroger' }));
    const panel = await screen.findByRole('status', { name: 'Cart send result' });
    expect(within(panel).getByText('Check these in your cart (1)')).toBeInTheDocument();
    expect(within(panel).getByRole('button', { name: "It's in my cart: Oats" })).toBeInTheDocument();
    expect(within(panel).getByRole('button', { name: "It's not: Oats" })).toBeInTheDocument();
  });

  it('marks check rows inline and undo restores the buttons', async () => {
    const body = {
      provider: 'kroger',
      exported: 0,
      results: [
        { entryId: 'sli-oats', itemId: 'oats', name: 'Oats', quantity: 1, status: 'unknown' },
      ],
    };
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url.includes('unknown-resolution')) {
        return Promise.resolve(new Response(JSON.stringify({ entryId: 'sli-oats' }), {
          status: 200,
          headers: { 'Content-Type': 'application/json' },
        }));
      }
      if (url.includes('/export') && init?.method === 'POST') {
        return Promise.resolve(new Response(JSON.stringify(body), {
          status: 200,
          headers: { 'Content-Type': 'application/json' },
        }));
      }
      return Promise.reject(new Error(`unexpected ${url}`));
    }));
    renderButton();
    fireEvent.click(screen.getByRole('button', { name: 'Send to Kroger' }));
    const panel = await screen.findByRole('status', { name: 'Cart send result' });
    fireEvent.click(within(panel).getByRole('button', { name: "It's not: Oats" }));
    expect(await within(panel).findByText('Marked as not in your cart')).toBeInTheDocument();
    fireEvent.click(within(panel).getByRole('button', { name: 'Undo' }));
    expect(within(panel).getByRole('button', { name: "It's in my cart: Oats" })).toBeInTheDocument();
  });

  it('shows Show all past five sent names', async () => {
    const sent = Array.from({ length: 6 }, (_, index) => ({
      entryId: `s-${index}`,
      itemId: `i-${index}`,
      name: `Sent ${index}`,
      quantity: 1,
      status: 'sent' as const,
    }));
    mockExport({ provider: 'kroger', exported: 6, results: sent });
    renderButton();
    fireEvent.click(screen.getByRole('button', { name: 'Send to Kroger' }));
    const panel = await screen.findByRole('status', { name: 'Cart send result' });
    expect(within(panel).getByRole('button', { name: 'Show all 6' })).toBeInTheDocument();
  });

  it('shows nothing-sent copy when N and U are zero', async () => {
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
    expect(within(panel).getByText('Nothing was sent to your cart.')).toBeInTheDocument();
    expect(within(panel).getByText("1 couldn't be sent.")).toBeInTheDocument();
  });
});

const rowsOf = (count: number, status: 'skipped' | 'unknown') => Array.from({ length: count }, (_, index) => ({
  entryId: `r-${index}`,
  itemId: `i-${index}`,
  name: `Row ${index}`,
  quantity: 1,
  status,
  reason: status === 'skipped' ? ('no_upc' as const) : ('no_per_item_result' as const),
}));

describe('ProvisionButton result panel limits (#485 UX)', () => {
  it('caps couldn\'t-be-sent rows at fifty after Show all', async () => {
    mockExport({ provider: 'kroger', exported: 0, results: rowsOf(60, 'skipped') });
    renderButton();
    fireEvent.click(screen.getByRole('button', { name: 'Send to Kroger' }));
    const panel = await screen.findByRole('status', { name: 'Cart send result' });
    fireEvent.click(within(panel).getByRole('button', { name: 'Show all 60' }));
    expect(within(panel).getAllByText(/: No barcode saved for this item$/)).toHaveLength(50);
  });

  it('removes the previous result when sending again', async () => {
    mockExport({ provider: 'kroger', exported: 1, results: [{ entryId: 'a', itemId: '1', name: 'Milk', quantity: 1, status: 'sent' }] });
    renderButton();
    fireEvent.click(screen.getByRole('button', { name: 'Send to Kroger' }));
    await screen.findByRole('status', { name: 'Cart send result' });
    let resolveFetch!: (value: Response) => void;
    vi.stubGlobal('fetch', vi.fn(() => new Promise<Response>((resolve) => { resolveFetch = resolve; })));
    fireEvent.click(screen.getByRole('button', { name: 'Send to Kroger' }));
    expect(screen.queryByRole('status', { name: 'Cart send result' })).not.toBeInTheDocument();
    await act(async () => {
      resolveFetch(new Response(JSON.stringify({ provider: 'kroger', exported: 0, results: [] }), { status: 200, headers: { 'Content-Type': 'application/json' } }));
    });
  });
});
