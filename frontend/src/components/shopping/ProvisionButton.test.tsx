import type { ComponentProps } from 'react';
import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { MantineProvider } from '@mantine/core';
import { describe, expect, it, vi } from 'vitest';
import type { ProviderInfo, ProvisionReport, ShoppingListEntry } from '@stockbot/shared/types';
import { CODE_PROVIDER_UNAVAILABLE, CODE_RECONNECT_REQUIRED } from '@stockbot/shared/contract';
import { PROVISION_RESOLVE_UNDO_MS } from '@stockbot/shared/provisionExportUX';
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

const mockExport = (body: ProvisionReport | Record<string, unknown>, status = 200) => {
  vi.stubGlobal('fetch', vi.fn(() => Promise.resolve(new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  }))));
};

const mockExportEnvelope = (
  status: number,
  code: typeof CODE_RECONNECT_REQUIRED | typeof CODE_PROVIDER_UNAVAILABLE,
  message = 'export failed',
) => {
  vi.stubGlobal('fetch', vi.fn(() => Promise.resolve(new Response(JSON.stringify({
    error: message,
    code,
  }), {
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
    expect(within(panel).queryByText(/^Couldn't be sent/)).not.toBeInTheDocument();
    expect(within(panel).queryByText(/^Check these in your cart/)).not.toBeInTheDocument();
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
    fireEvent.click(within(panel).getByRole('button', { name: 'Undo: Oats' }));
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

  it('renders a failed request_incomplete line in the couldnt-be-sent section', async () => {
    mockExport({
      provider: 'example',
      exported: 0,
      results: [
        {
          entryId: 'e1',
          itemId: 'i1',
          name: 'Synthetic line A',
          quantity: 1,
          status: 'failed',
          reason: 'request_incomplete',
        },
      ],
    });
    renderButton({ provider: provider({ id: 'example', displayName: 'Example store' }) });
    fireEvent.click(screen.getByRole('button', { name: 'Send to Example store' }));
    const panel = await screen.findByRole('status', { name: 'Cart send result' });
    const rowLine = within(panel).getByText('Synthetic line A: Not sent. Send again to finish.');
    expect(rowLine).toBeVisible();
    expect(within(panel).getByText("Couldn't be sent (1)")).toBeVisible();
    expect(within(panel).queryByText(/^Check these in your cart/)).not.toBeInTheDocument();
    expect(within(panel).queryByRole('button', { name: /Try again:/ })).not.toBeInTheDocument();
  });
});

const spuriousUnknownResults = Array.from({ length: 4 }, (_, index) => ({
  entryId: `u-${index}`,
  itemId: `i-${index}`,
  name: `Item ${index}`,
  quantity: 1,
  status: 'unknown' as const,
  reason: 'request_incomplete' as const,
}));

describe('ProvisionButton export envelopes (409/502)', () => {
  it('409 reconnect_required shows reconnect copy and no Try again', async () => {
    mockExportEnvelope(409, CODE_RECONNECT_REQUIRED);
    renderButton();
    fireEvent.click(screen.getByRole('button', { name: 'Send to Kroger' }));
    const panel = await screen.findByRole('status', { name: 'Cart send result' });
    expect(within(panel).getByText("Couldn't connect to your Kroger account. Nothing was sent.")).toBeInTheDocument();
    expect(within(panel).getByRole('button', { name: 'Reconnect Kroger' })).toBeInTheDocument();
    expect(within(panel).queryByRole('button', { name: 'Try again' })).not.toBeInTheDocument();
    expect(within(panel).queryByText(/^Check these in your cart/)).not.toBeInTheDocument();
  });

  it('502 provider_unavailable shows store-unavailable copy and Try again, not Reconnect', async () => {
    mockExportEnvelope(502, CODE_PROVIDER_UNAVAILABLE);
    renderButton();
    fireEvent.click(screen.getByRole('button', { name: 'Send to Kroger' }));
    const panel = await screen.findByRole('status', { name: 'Cart send result' });
    expect(within(panel).getByText("Nothing was sent. Kroger isn't responding right now.")).toBeInTheDocument();
    expect(within(panel).getByRole('button', { name: 'Try again' })).toBeInTheDocument();
    expect(within(panel).queryByRole('button', { name: 'Reconnect Kroger' })).not.toBeInTheDocument();
  });

  it('Try again on 502 retries export', async () => {
    let exportCalls = 0;
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url.includes('/export') && init?.method === 'POST') {
        exportCalls += 1;
        if (exportCalls === 1) {
          return Promise.resolve(new Response(JSON.stringify({ error: 'down', code: CODE_PROVIDER_UNAVAILABLE }), {
            status: 502,
            headers: { 'Content-Type': 'application/json' },
          }));
        }
        return Promise.resolve(new Response(JSON.stringify({
          provider: 'kroger',
          exported: 1,
          results: [{ entryId: 'a', itemId: '1', name: 'Milk', quantity: 1, status: 'sent' }],
        }), {
          status: 200,
          headers: { 'Content-Type': 'application/json' },
        }));
      }
      return Promise.resolve(new Response('{}', { status: 200, headers: { 'Content-Type': 'application/json' } }));
    }));
    renderButton();
    fireEvent.click(screen.getByRole('button', { name: 'Send to Kroger' }));
    const panel = await screen.findByRole('status', { name: 'Cart send result' });
    fireEvent.click(within(panel).getByRole('button', { name: 'Try again' }));
    await waitFor(() => expect(exportCalls).toBe(2));
    const panelAfter = await screen.findByRole('status', { name: 'Cart send result' });
    expect(within(panelAfter).getByText('1 item sent to your Kroger cart.')).toBeInTheDocument();
  });

  it('Reconnect on 409 starts authorize', async () => {
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url.includes('/export') && init?.method === 'POST') {
        return Promise.resolve(new Response(JSON.stringify({ error: 'revoked', code: CODE_RECONNECT_REQUIRED }), {
          status: 409,
          headers: { 'Content-Type': 'application/json' },
        }));
      }
      if (url.includes('/authorize')) {
        return Promise.resolve(new Response(JSON.stringify({ authorizationUrl: 'https://oauth.test/start' }), {
          status: 200,
          headers: { 'Content-Type': 'application/json' },
        }));
      }
      return Promise.reject(new Error(`unexpected ${url}`));
    }));
    const assign = vi.fn();
    vi.stubGlobal('location', { ...window.location, assign });
    renderButton();
    fireEvent.click(screen.getByRole('button', { name: 'Send to Kroger' }));
    const panel = await screen.findByRole('status', { name: 'Cart send result' });
    fireEvent.click(within(panel).getByRole('button', { name: 'Reconnect Kroger' }));
    await waitFor(() => expect(assign).toHaveBeenCalledWith('https://oauth.test/start'));
  });
});

describe('ProvisionButton partial reconnectRequired', () => {
  it('shows sections and Reconnect when some items sent', async () => {
    mockExport({
      provider: 'kroger',
      exported: 1,
      sentCount: 1,
      reconnectRequired: true,
      results: [
        { entryId: 'a', itemId: '1', name: 'Milk', quantity: 1, status: 'sent' },
        { entryId: 'b', itemId: '2', name: 'Bread', quantity: 1, status: 'failed', reason: 'reconnect_required' },
        { entryId: 'c', itemId: '3', name: 'Butter', quantity: 1, status: 'unknown', reason: 'request_incomplete' },
      ],
    });
    renderButton();
    fireEvent.click(screen.getByRole('button', { name: 'Send to Kroger' }));
    const panel = await screen.findByRole('status', { name: 'Cart send result' });
    expect(within(panel).getByText('Sent (1)')).toBeInTheDocument();
    expect(within(panel).getByText("Couldn't be sent (1)")).toBeInTheDocument();
    expect(within(panel).getByText('Check these in your cart (1)')).toBeInTheDocument();
    expect(within(panel).getByText('Butter')).toBeInTheDocument();
    expect(within(panel).queryByText(/Butter: Not sent/)).not.toBeInTheDocument();
    expect(within(panel).getByText('Bread: Reconnect Kroger to send this')).toBeInTheDocument();
    expect(within(panel).getByText(
      'Your Kroger account disconnected before everything was sent. Reconnect, then send again to finish.',
    )).toBeInTheDocument();
    expect(within(panel).getByRole('button', { name: 'Reconnect Kroger' })).toBeInTheDocument();
  });

  it('partial-send Reconnect starts authorize', async () => {
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url.includes('/export') && init?.method === 'POST') {
        return Promise.resolve(new Response(JSON.stringify({
          provider: 'kroger',
          exported: 1,
          sentCount: 1,
          failedCount: 1,
          reconnectRequired: true,
          results: [
            { entryId: 'a', itemId: '1', name: 'Milk', quantity: 1, status: 'sent' },
            { entryId: 'b', itemId: '2', name: 'Bread', quantity: 1, status: 'failed', reason: 'reconnect_required' },
          ],
        }), { status: 200, headers: { 'Content-Type': 'application/json' } }));
      }
      if (url.includes('/authorize')) {
        return Promise.resolve(new Response(JSON.stringify({ authorizationUrl: 'https://oauth.test/start' }), {
          status: 200,
          headers: { 'Content-Type': 'application/json' },
        }));
      }
      return Promise.reject(new Error(`unexpected ${url}`));
    }));
    const assign = vi.fn();
    vi.stubGlobal('location', { ...window.location, assign });
    renderButton();
    fireEvent.click(screen.getByRole('button', { name: 'Send to Kroger' }));
    const panel = await screen.findByRole('status', { name: 'Cart send result' });
    fireEvent.click(within(panel).getByRole('button', { name: 'Reconnect Kroger' }));
    await waitFor(() => expect(assign).toHaveBeenCalledWith('https://oauth.test/start'));
  });
});

describe('ProvisionButton legacy HTTP 200 top-level error', () => {
  it('blocks per-item UI for reconnect_required on 200 body', async () => {
    mockExport({
      provider: 'kroger',
      exported: 0,
      error: 'reconnect_required',
      results: spuriousUnknownResults,
    });
    renderButton();
    fireEvent.click(screen.getByRole('button', { name: 'Send to Kroger' }));
    const panel = await screen.findByRole('status', { name: 'Cart send result' });
    expect(within(panel).getByText("Couldn't connect to your Kroger account. Nothing was sent.")).toBeInTheDocument();
    expect(within(panel).getByRole('button', { name: 'Reconnect Kroger' })).toBeInTheDocument();
    expect(within(panel).queryByText(/might not have reached your cart/)).not.toBeInTheDocument();
    expect(within(panel).queryByText(/^Check these in your cart/)).not.toBeInTheDocument();
    expect(within(panel).queryByText(/^Sent \(/)).not.toBeInTheDocument();
    expect(within(panel).queryByText(/^Couldn't be sent \(/)).not.toBeInTheDocument();
    expect(within(panel).queryByText('Item 0')).not.toBeInTheDocument();
    expect(within(panel).queryByText(/doesn't confirm each item/)).not.toBeInTheDocument();
  });

  it('502 legacy body shows Try again only', async () => {
    mockExport({
      provider: 'kroger',
      exported: 0,
      error: 'provider_unavailable',
      results: spuriousUnknownResults,
    });
    renderButton();
    fireEvent.click(screen.getByRole('button', { name: 'Send to Kroger' }));
    const panel = await screen.findByRole('status', { name: 'Cart send result' });
    expect(within(panel).getByText('Nothing was sent. Kroger isn\'t responding right now.')).toBeInTheDocument();
    expect(within(panel).getByRole('button', { name: 'Try again' })).toBeInTheDocument();
    expect(within(panel).queryByRole('button', { name: 'Reconnect Kroger' })).not.toBeInTheDocument();
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

const jsonResponse = (body: unknown, status = 200) => Promise.resolve(new Response(JSON.stringify(body), {
  status,
  headers: { 'Content-Type': 'application/json' },
}));

const stubExportAndResolve = (body: unknown, { exportStatus = 200, resolveStatus = 200 } = {}) => {
  const resolutions: { provider: string; reachedProvider: boolean }[] = [];
  let exports = 0;
  vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
    const url = String(input);
    if (url.includes('unknown-resolution')) {
      resolutions.push(JSON.parse(String(init?.body)) as { provider: string; reachedProvider: boolean });
      return resolveStatus === 200 ? jsonResponse({ entryId: 'sli-oats' }) : jsonResponse({ error: 'boom' }, resolveStatus);
    }
    if (url.includes('/export') && init?.method === 'POST') {
      exports += 1;
      return jsonResponse(body, exportStatus);
    }
    return jsonResponse({});
  }));
  return { resolutions, exportCount: () => exports };
};

const oneUnknown = {
  provider: 'kroger',
  exported: 0,
  results: [{ entryId: 'sli-oats', itemId: 'oats', name: 'Oats', quantity: 1, status: 'unknown' }],
};

const flush = async () => {
  await act(async () => {
    await Promise.resolve();
    await Promise.resolve();
  });
};

describe('ProvisionButton check-row undo window (ledger advance is not reversible)', () => {
  it('does not record a choice until the undo window ends, then hides Undo', async () => {
    const api = stubExportAndResolve(oneUnknown);
    renderButton();
    fireEvent.click(screen.getByRole('button', { name: 'Send to Kroger' }));
    const panel = await screen.findByRole('status', { name: 'Cart send result' });
    vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout'] });
    try {
      fireEvent.click(within(panel).getByRole('button', { name: "It's in my cart: Oats" }));
      expect(within(panel).getByText('Marked as in your cart')).toBeInTheDocument();
      expect(api.resolutions).toEqual([]);
      await act(async () => { vi.advanceTimersByTime(PROVISION_RESOLVE_UNDO_MS); });
      await flush();
      expect(api.resolutions).toEqual([{ provider: 'kroger', reachedProvider: true }]);
      await flush();
      expect(within(panel).queryByRole('button', { name: 'Undo: Oats' })).not.toBeInTheDocument();
      expect(within(panel).getByText('Marked as in your cart')).toBeInTheDocument();
    } finally {
      vi.useRealTimers();
    }
  });

  it('Undo cancels the choice so in, undo, in records once', async () => {
    const api = stubExportAndResolve(oneUnknown);
    renderButton();
    fireEvent.click(screen.getByRole('button', { name: 'Send to Kroger' }));
    const panel = await screen.findByRole('status', { name: 'Cart send result' });
    vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout'] });
    try {
      fireEvent.click(within(panel).getByRole('button', { name: "It's in my cart: Oats" }));
      fireEvent.click(within(panel).getByRole('button', { name: 'Undo: Oats' }));
      fireEvent.click(within(panel).getByRole('button', { name: "It's not: Oats" }));
      await act(async () => { vi.advanceTimersByTime(PROVISION_RESOLVE_UNDO_MS); });
      await flush();
      expect(api.resolutions).toEqual([{ provider: 'kroger', reachedProvider: false }]);
    } finally {
      vi.useRealTimers();
    }
  });

  it('records a pending choice when the panel is dismissed', async () => {
    const api = stubExportAndResolve(oneUnknown);
    renderButton();
    fireEvent.click(screen.getByRole('button', { name: 'Send to Kroger' }));
    const panel = await screen.findByRole('status', { name: 'Cart send result' });
    fireEvent.click(within(panel).getByRole('button', { name: "It's in my cart: Oats" }));
    fireEvent.click(within(panel).getByRole('button', { name: 'Dismiss cart send result' }));
    await flush();
    expect(api.resolutions).toEqual([{ provider: 'kroger', reachedProvider: true }]);
  });

  it('keeps focus in the row after marking and after Undo', async () => {
    stubExportAndResolve(oneUnknown);
    renderButton();
    fireEvent.click(screen.getByRole('button', { name: 'Send to Kroger' }));
    const panel = await screen.findByRole('status', { name: 'Cart send result' });
    fireEvent.click(within(panel).getByRole('button', { name: "It's in my cart: Oats" }));
    await act(async () => { await new Promise((r) => requestAnimationFrame(() => r(undefined))); });
    expect(document.activeElement).toBe(within(panel).getByRole('button', { name: 'Undo: Oats' }));
    fireEvent.click(within(panel).getByRole('button', { name: 'Undo: Oats' }));
    await act(async () => { await new Promise((r) => requestAnimationFrame(() => r(undefined))); });
    expect(document.activeElement).toBe(within(panel).getByRole('button', { name: "It's in my cart: Oats" }));
  });

  it('shows a row error under the row and restores the buttons when recording fails', async () => {
    stubExportAndResolve(oneUnknown, { resolveStatus: 500 });
    renderButton();
    fireEvent.click(screen.getByRole('button', { name: 'Send to Kroger' }));
    const panel = await screen.findByRole('status', { name: 'Cart send result' });
    const row = within(panel).getByText('Oats').closest('.provision-check-row') as HTMLElement;
    vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout'] });
    try {
      fireEvent.click(within(panel).getByRole('button', { name: "It's not: Oats" }));
      await act(async () => { vi.advanceTimersByTime(PROVISION_RESOLVE_UNDO_MS); });
    } finally {
      vi.useRealTimers();
    }
    expect(await within(row).findByText("Couldn't save that.")).toBeInTheDocument();
    expect(within(row).getByRole('alert')).toHaveTextContent("Couldn't save that.");
    expect(within(row).getByRole('button', { name: 'Try again: Oats' })).toBeInTheDocument();
    expect(within(row).queryByRole('button', { name: "It's not: Oats" })).not.toBeInTheDocument();
    expect(within(panel).queryByText('Marked as not in your cart')).not.toBeInTheDocument();
  });

  it('commits a pending choice when the panel unmounts', async () => {
    const api = stubExportAndResolve(oneUnknown);
    const { unmount } = renderButton();
    fireEvent.click(screen.getByRole('button', { name: 'Send to Kroger' }));
    const panel = await screen.findByRole('status', { name: 'Cart send result' });
    fireEvent.click(within(panel).getByRole('button', { name: "It's in my cart: Oats" }));
    unmount();
    await flush();
    expect(api.resolutions).toEqual([{ provider: 'kroger', reachedProvider: true }]);
  });

  const orderedStub = (resolveStatus = 200) => {
    const order: string[] = [];
    const inits: RequestInit[] = [];
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url.includes('unknown-resolution')) {
        order.push('resolve');
        inits.push(init ?? {});
        return resolveStatus === 200 ? jsonResponse({ entryId: 'sli-oats' }) : jsonResponse({ error: 'boom' }, resolveStatus);
      }
      if (url.includes('/export') && init?.method === 'POST') {
        order.push('export');
        return jsonResponse(oneUnknown);
      }
      return jsonResponse({});
    }));
    return { order, inits };
  };

  it('waits for a save the timer already started before sending again', async () => {
    const order: string[] = [];
    let finishSave: (r: Response) => void = () => undefined;
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url.includes('unknown-resolution')) {
        order.push('resolve-start');
        return new Promise<Response>((resolve) => { finishSave = resolve; });
      }
      if (url.includes('/export') && init?.method === 'POST') {
        order.push('export');
        return jsonResponse(oneUnknown);
      }
      return jsonResponse({});
    }));
    renderButton();
    fireEvent.click(screen.getByRole('button', { name: 'Send to Kroger' }));
    const panel = await screen.findByRole('status', { name: 'Cart send result' });
    vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout'] });
    try {
      fireEvent.click(within(panel).getByRole('button', { name: "It's in my cart: Oats" }));
      await act(async () => { vi.advanceTimersByTime(PROVISION_RESOLVE_UNDO_MS); });
    } finally {
      vi.useRealTimers();
    }
    expect(order).toEqual(['export', 'resolve-start']);
    fireEvent.click(screen.getByRole('button', { name: 'Send to Kroger' }));
    await flush();
    expect(order).toEqual(['export', 'resolve-start']);
    await act(async () => { finishSave(new Response(JSON.stringify({ entryId: 'sli-oats' }), { status: 200, headers: { 'Content-Type': 'application/json' } })); });
    await waitFor(() => expect(order).toEqual(['export', 'resolve-start', 'export']));
  });

  it('records a pending choice on pagehide', async () => {
    const api = orderedStub();
    renderButton();
    fireEvent.click(screen.getByRole('button', { name: 'Send to Kroger' }));
    const panel = await screen.findByRole('status', { name: 'Cart send result' });
    fireEvent.click(within(panel).getByRole('button', { name: "It's in my cart: Oats" }));
    act(() => { window.dispatchEvent(new Event('pagehide')); });
    await waitFor(() => expect(api.order).toEqual(['export', 'resolve']));
    expect(api.inits[0].keepalive).toBe(true);
  });

  it('records the pending choice before the new export starts', async () => {
    const api = orderedStub();
    renderButton();
    fireEvent.click(screen.getByRole('button', { name: 'Send to Kroger' }));
    const panel = await screen.findByRole('status', { name: 'Cart send result' });
    fireEvent.click(within(panel).getByRole('button', { name: "It's in my cart: Oats" }));
    fireEvent.click(screen.getByRole('button', { name: 'Send to Kroger' }));
    await waitFor(() => expect(api.order).toEqual(['export', 'resolve', 'export']));
    expect(api.inits[0].keepalive).toBe(true);
  });

  it('keeps the panel and does not send again when the pending save fails', async () => {
    const api = orderedStub(500);
    renderButton();
    fireEvent.click(screen.getByRole('button', { name: 'Send to Kroger' }));
    const panel = await screen.findByRole('status', { name: 'Cart send result' });
    fireEvent.click(within(panel).getByRole('button', { name: "It's in my cart: Oats" }));
    fireEvent.click(screen.getByRole('button', { name: 'Send to Kroger' }));
    await waitFor(() => expect(screen.getByText("One item didn't save. Fix it below, then send again.")).toBeInTheDocument());
    const row = within(panel).getByText('Oats').closest('.provision-check-row') as HTMLElement;
    await waitFor(() => expect(within(row).getByRole('button', { name: 'Try again: Oats' })).toHaveFocus());
    expect(api.order).toEqual(['export', 'resolve']);
    expect(screen.getByRole('button', { name: 'Send to Kroger' })).toBeEnabled();
  });

  it('keeps the panel open with the row alert when the save on dismiss fails', async () => {
    orderedStub(500);
    renderButton();
    fireEvent.click(screen.getByRole('button', { name: 'Send to Kroger' }));
    const panel = await screen.findByRole('status', { name: 'Cart send result' });
    fireEvent.click(within(panel).getByRole('button', { name: "It's in my cart: Oats" }));
    fireEvent.click(within(panel).getByRole('button', { name: 'Dismiss cart send result' }));
    const row = within(panel).getByText('Oats').closest('.provision-check-row') as HTMLElement;
    expect(await within(row).findByText("Couldn't save that.")).toBeInTheDocument();
    await waitFor(() => expect(within(row).getByRole('button', { name: 'Try again: Oats' })).toHaveFocus());
    expect(screen.getByRole('status', { name: 'Cart send result' })).toBe(panel);
    expect(screen.queryByText("One item didn't save. Fix it below, then send again.")).not.toBeInTheDocument();
  });

  it('hides Undo while the save is in flight, shows a spinner, and focuses the row container', async () => {
    let finishSave: (r: Response) => void = () => undefined;
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url.includes('unknown-resolution')) {
        return new Promise<Response>((resolve) => { finishSave = resolve; });
      }
      if (url.includes('/export') && init?.method === 'POST') {
        return jsonResponse(oneUnknown);
      }
      return jsonResponse({});
    }));
    renderButton();
    fireEvent.click(screen.getByRole('button', { name: 'Send to Kroger' }));
    const panel = await screen.findByRole('status', { name: 'Cart send result' });
    const row = within(panel).getByText('Oats').closest('.provision-check-row') as HTMLElement;
    vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout'] });
    try {
      fireEvent.click(within(panel).getByRole('button', { name: "It's in my cart: Oats" }));
      within(row).getByRole('button', { name: 'Undo: Oats' }).focus();
      const markedGroup = within(row).getByText('Marked as in your cart').closest('.provision-check-marked') as HTMLElement;
      const heightBefore = markedGroup.getBoundingClientRect().height;
      await act(async () => { vi.advanceTimersByTime(PROVISION_RESOLVE_UNDO_MS); });
      expect(row.querySelector('.provision-row-saving-spinner')).toHaveAttribute('aria-hidden', 'true');
      expect(within(row).queryByRole('button', { name: 'Undo: Oats' })).not.toBeInTheDocument();
      expect(within(row).getByText('Marked as in your cart')).toBeInTheDocument();
      expect(markedGroup.getBoundingClientRect().height).toBeCloseTo(heightBefore, 0);
    } finally {
      vi.useRealTimers();
    }
    await act(async () => { await new Promise((r) => requestAnimationFrame(() => r(undefined))); });
    expect(document.activeElement).toBe(row);
    await act(async () => {
      finishSave(new Response(JSON.stringify({ entryId: 'sli-oats' }), { status: 200, headers: { 'Content-Type': 'application/json' } }));
    });
    expect(within(panel).getByText('Marked as in your cart')).toBeInTheDocument();
    expect(within(panel).queryByRole('button', { name: 'Undo: Oats' })).not.toBeInTheDocument();
  });

  it('row Try again succeeds and clears the blocked Send hint', async () => {
    let resolveCalls = 0;
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url.includes('unknown-resolution')) {
        resolveCalls += 1;
        if (resolveCalls === 1) {
          return jsonResponse({ error: 'boom' }, 500);
        }
        return jsonResponse({ entryId: 'sli-oats' });
      }
      if (url.includes('/export') && init?.method === 'POST') {
        return jsonResponse(oneUnknown);
      }
      return jsonResponse({});
    }));
    renderButton();
    fireEvent.click(screen.getByRole('button', { name: 'Send to Kroger' }));
    const panel = await screen.findByRole('status', { name: 'Cart send result' });
    fireEvent.click(within(panel).getByRole('button', { name: "It's in my cart: Oats" }));
    fireEvent.click(screen.getByRole('button', { name: 'Send to Kroger' }));
    await waitFor(() => expect(screen.getByText("One item didn't save. Fix it below, then send again.")).toBeInTheDocument());
    const row = within(panel).getByText('Oats').closest('.provision-check-row') as HTMLElement;
    fireEvent.click(within(row).getByRole('button', { name: 'Try again: Oats' }));
    await waitFor(() => expect(within(panel).getByText('Marked as in your cart')).toBeInTheDocument());
    expect(within(panel).queryByRole('button', { name: 'Undo: Oats' })).not.toBeInTheDocument();
    await waitFor(() => expect(screen.queryByText("One item didn't save. Fix it below, then send again.")).not.toBeInTheDocument());
  });
});

const twoUnknown = {
  provider: 'kroger',
  exported: 0,
  results: [
    { entryId: 'sli-oats', itemId: 'oats', name: 'Oats', quantity: 1, status: 'unknown' },
    { entryId: 'sli-jam', itemId: 'jam', name: 'Jam', quantity: 1, status: 'unknown' },
  ],
};

// Scripted mock: each entry's resolve calls pop a status from its queue (200 once the queue is empty).
const stubScripted = (body: unknown, failures: Record<string, number[]>) => {
  const calls: string[] = [];
  vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
    const url = String(input);
    if (url.includes('unknown-resolution')) {
      const id = decodeURIComponent(url.split('/').find((part) => part.startsWith('sli-')) ?? '');
      calls.push(`resolve:${id}`);
      const status = failures[id]?.shift() ?? 200;
      return status === 200 ? jsonResponse({ entryId: id }) : jsonResponse({ error: 'boom' }, status);
    }
    if (url.includes('/export') && init?.method === 'POST') {
      calls.push('export');
      return jsonResponse(body);
    }
    return jsonResponse({});
  }));
  return calls;
};

const sendNow = () => fireEvent.click(screen.getByRole('button', { name: 'Send to Kroger' }));
const ONE_HINT = "One item didn't save. Fix it below, then send again.";
const TWO_HINT = "2 items didn't save. Fix them below, then send again.";
const nextFrame = async () => {
  await act(async () => { await new Promise((r) => requestAnimationFrame(() => r(undefined))); });
};

describe('ProvisionButton blocked send and failed saves (63cf468 review)', () => {
  it('a second Send retries the failed save and still blocks while it fails', async () => {
    const calls = stubScripted(oneUnknown, { 'sli-oats': [500, 500] });
    renderButton();
    sendNow();
    const panel = await screen.findByRole('status', { name: 'Cart send result' });
    fireEvent.click(within(panel).getByRole('button', { name: "It's in my cart: Oats" }));
    sendNow();
    await waitFor(() => expect(screen.getByText(ONE_HINT)).toBeInTheDocument());
    sendNow();
    await waitFor(() => expect(calls.filter((c) => c === 'resolve:sli-oats')).toHaveLength(2));
    await flush();
    expect(calls.filter((c) => c === 'export')).toHaveLength(1);
    expect(screen.getByText(ONE_HINT)).toBeInTheDocument();
    sendNow();
    await waitFor(() => expect(calls.filter((c) => c === 'export')).toHaveLength(2));
    expect(calls.slice(-2)).toEqual(['resolve:sli-oats', 'export']);
  });

  it('Send after a failed timer save retries the save before exporting', async () => {
    const calls = stubScripted(oneUnknown, { 'sli-oats': [500] });
    renderButton();
    sendNow();
    const panel = await screen.findByRole('status', { name: 'Cart send result' });
    vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout'] });
    try {
      fireEvent.click(within(panel).getByRole('button', { name: "It's in my cart: Oats" }));
      await act(async () => { vi.advanceTimersByTime(PROVISION_RESOLVE_UNDO_MS); });
    } finally {
      vi.useRealTimers();
    }
    await waitFor(() => expect(within(panel).getByRole('button', { name: 'Try again: Oats' })).toBeInTheDocument());
    sendNow();
    await waitFor(() => expect(calls.filter((c) => c === 'export')).toHaveLength(2));
    expect(calls).toEqual(['export', 'resolve:sli-oats', 'resolve:sli-oats', 'export']);
  });

  it('the plural hint counts down and stays until every failed save is fixed', async () => {
    const calls = stubScripted(twoUnknown, { 'sli-oats': [500], 'sli-jam': [500] });
    renderButton();
    sendNow();
    const panel = await screen.findByRole('status', { name: 'Cart send result' });
    fireEvent.click(within(panel).getByRole('button', { name: "It's in my cart: Oats" }));
    fireEvent.click(within(panel).getByRole('button', { name: "It's in my cart: Jam" }));
    sendNow();
    await waitFor(() => expect(screen.getByText(TWO_HINT)).toBeInTheDocument());
    expect(screen.getByText(TWO_HINT).closest('[role="alert"]')).toBeTruthy();
    fireEvent.click(within(panel).getByRole('button', { name: 'Try again: Oats' }));
    await waitFor(() => expect(screen.getByText(ONE_HINT)).toBeInTheDocument());
    expect(within(panel).getByRole('button', { name: 'Try again: Jam' })).toBeInTheDocument();
    fireEvent.click(within(panel).getByRole('button', { name: 'Try again: Jam' }));
    await waitFor(() => expect(screen.queryByText(ONE_HINT)).not.toBeInTheDocument());
    expect(calls.filter((c) => c === 'export')).toHaveLength(1);
  });

  it('a blocked dismiss shows the row alert but not the Send hint alert', async () => {
    stubScripted(oneUnknown, { 'sli-oats': [500] });
    renderButton();
    sendNow();
    const panel = await screen.findByRole('status', { name: 'Cart send result' });
    fireEvent.click(within(panel).getByRole('button', { name: "It's in my cart: Oats" }));
    fireEvent.click(within(panel).getByRole('button', { name: 'Dismiss cart send result' }));
    await waitFor(() => expect(within(panel).getByRole('button', { name: 'Try again: Oats' })).toBeInTheDocument());
    expect(screen.queryByText(ONE_HINT)).not.toBeInTheDocument();
    expect(document.querySelector('.provision-blocked-send-hint')).toBeNull();
  });

  it('a blocked Send shows the Send hint alert under the Send control', async () => {
    stubScripted(oneUnknown, { 'sli-oats': [500] });
    renderButton();
    sendNow();
    const panel = await screen.findByRole('status', { name: 'Cart send result' });
    fireEvent.click(within(panel).getByRole('button', { name: "It's in my cart: Oats" }));
    sendNow();
    await waitFor(() => expect(screen.getByText(ONE_HINT)).toBeInTheDocument());
    const hint = document.querySelector('.provision-blocked-send-hint');
    expect(hint).toHaveAttribute('role', 'alert');
    expect(hint).toHaveTextContent(ONE_HINT);
  });

  it('clears the blocked Send hint when every failed save succeeds', async () => {
    const calls = stubScripted(twoUnknown, { 'sli-oats': [500], 'sli-jam': [500] });
    renderButton();
    sendNow();
    const panel = await screen.findByRole('status', { name: 'Cart send result' });
    fireEvent.click(within(panel).getByRole('button', { name: "It's in my cart: Oats" }));
    fireEvent.click(within(panel).getByRole('button', { name: "It's in my cart: Jam" }));
    sendNow();
    await waitFor(() => expect(screen.getByText(TWO_HINT)).toBeInTheDocument());
    fireEvent.click(within(panel).getByRole('button', { name: 'Try again: Oats' }));
    await waitFor(() => expect(screen.getByText(ONE_HINT)).toBeInTheDocument());
    fireEvent.click(within(panel).getByRole('button', { name: 'Try again: Jam' }));
    await waitFor(() => expect(screen.queryByText(TWO_HINT)).not.toBeInTheDocument());
    await waitFor(() => expect(screen.queryByText(ONE_HINT)).not.toBeInTheDocument());
    expect(screen.queryByText(/0 items didn't save/)).not.toBeInTheDocument();
    expect(document.querySelector('.provision-blocked-send-hint')).toBeNull();
    expect(calls.filter((c) => c.startsWith('resolve:')).length).toBeGreaterThanOrEqual(2);
  });

  it('does not start a second resolve while the first save is still in flight', async () => {
    let resolveCalls = 0;
    let release!: (value: Response) => void;
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url.includes('unknown-resolution')) {
        resolveCalls += 1;
        return new Promise<Response>((resolve) => { release = resolve; });
      }
      if (url.includes('/export') && init?.method === 'POST') {
        return jsonResponse(oneUnknown);
      }
      return jsonResponse({});
    }));
    renderButton();
    sendNow();
    const panel = await screen.findByRole('status', { name: 'Cart send result' });
    vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout'] });
    try {
      fireEvent.click(within(panel).getByRole('button', { name: "It's in my cart: Oats" }));
      await act(async () => { vi.advanceTimersByTime(PROVISION_RESOLVE_UNDO_MS); });
    } finally {
      vi.useRealTimers();
    }
    await waitFor(() => expect(resolveCalls).toBe(1));
    sendNow();
    expect(resolveCalls).toBe(1);
    await act(async () => {
      release(new Response(JSON.stringify({ entryId: 'sli-oats' }), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      }));
    });
  });

  it('a second dismiss with a still-failing save keeps the panel', async () => {
    stubScripted(oneUnknown, { 'sli-oats': [500, 500] });
    renderButton();
    sendNow();
    const panel = await screen.findByRole('status', { name: 'Cart send result' });
    fireEvent.click(within(panel).getByRole('button', { name: "It's in my cart: Oats" }));
    fireEvent.click(within(panel).getByRole('button', { name: 'Dismiss cart send result' }));
    await waitFor(() => expect(within(panel).getByRole('button', { name: 'Try again: Oats' })).toBeInTheDocument());
    fireEvent.click(within(panel).getByRole('button', { name: 'Dismiss cart send result' }));
    await flush();
    await nextFrame();
    expect(screen.getByRole('status', { name: 'Cart send result' })).toBeInTheDocument();
    expect(screen.queryByText(ONE_HINT)).not.toBeInTheDocument();
  });

  it('a double tap on Try again sends one save', async () => {
    const calls = stubScripted(oneUnknown, { 'sli-oats': [500] });
    renderButton();
    sendNow();
    const panel = await screen.findByRole('status', { name: 'Cart send result' });
    fireEvent.click(within(panel).getByRole('button', { name: "It's in my cart: Oats" }));
    fireEvent.click(within(panel).getByRole('button', { name: 'Dismiss cart send result' }));
    const retry = await within(panel).findByRole('button', { name: 'Try again: Oats' });
    fireEvent.click(retry);
    fireEvent.click(retry);
    await waitFor(() => expect(within(panel).getByText('Marked as in your cart')).toBeInTheDocument());
    expect(calls.filter((c) => c === 'resolve:sli-oats')).toHaveLength(2);
  });

  it('after Try again works, focus stays on the row, not body', async () => {
    stubScripted(oneUnknown, { 'sli-oats': [500] });
    renderButton();
    sendNow();
    const panel = await screen.findByRole('status', { name: 'Cart send result' });
    fireEvent.click(within(panel).getByRole('button', { name: "It's in my cart: Oats" }));
    sendNow();
    const retry = await within(panel).findByRole('button', { name: 'Try again: Oats' });
    await waitFor(() => expect(document.activeElement).toBe(retry));
    const row = retry.closest('.provision-check-row') as HTMLElement;
    fireEvent.click(retry);
    await waitFor(() => expect(within(panel).getByText('Marked as in your cart')).toBeInTheDocument());
    await nextFrame();
    expect(document.activeElement).toBe(row);
  });

  it('a blocked Send scrolls the failing row into view', async () => {
    stubScripted(twoUnknown, { 'sli-jam': [500] });
    const scrolled: Element[] = [];
    const spy = vi.spyOn(Element.prototype, 'scrollIntoView').mockImplementation(function (this: Element) { scrolled.push(this); });
    try {
      renderButton();
      sendNow();
      const panel = await screen.findByRole('status', { name: 'Cart send result' });
      fireEvent.click(within(panel).getByRole('button', { name: "It's in my cart: Jam" }));
      sendNow();
      await waitFor(() => expect(screen.getByText(ONE_HINT)).toBeInTheDocument());
      const jamRow = within(panel).getByText('Jam').closest('.provision-check-row');
      await waitFor(() => expect(scrolled).toContain(jamRow));
    } finally {
      spy.mockRestore();
    }
  });

  it('a failed timer save does not pull focus from another row', async () => {
    stubScripted(twoUnknown, { 'sli-oats': [500] });
    renderButton();
    sendNow();
    const panel = await screen.findByRole('status', { name: 'Cart send result' });
    const jam = within(panel).getByRole('button', { name: "It's in my cart: Jam" });
    vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout'] });
    try {
      fireEvent.click(within(panel).getByRole('button', { name: "It's in my cart: Oats" }));
      await nextFrame();
      jam.focus();
      await act(async () => { vi.advanceTimersByTime(PROVISION_RESOLVE_UNDO_MS); });
    } finally {
      vi.useRealTimers();
    }
    await waitFor(() => expect(within(panel).getByRole('button', { name: 'Try again: Oats' })).toBeInTheDocument());
    await nextFrame();
    expect(document.activeElement).toBe(jam);
  });

  it('a timer save does not pull focus from another row', async () => {
    let finishSave: (r: Response) => void = () => undefined;
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url.includes('unknown-resolution')) {
        return new Promise<Response>((resolve) => { finishSave = resolve; });
      }
      if (url.includes('/export') && init?.method === 'POST') return jsonResponse(twoUnknown);
      return jsonResponse({});
    }));
    renderButton();
    sendNow();
    const panel = await screen.findByRole('status', { name: 'Cart send result' });
    vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout'] });
    const jam = within(panel).getByRole('button', { name: "It's in my cart: Jam" });
    try {
      fireEvent.click(within(panel).getByRole('button', { name: "It's in my cart: Oats" }));
      await nextFrame();
      jam.focus();
      await act(async () => { vi.advanceTimersByTime(PROVISION_RESOLVE_UNDO_MS); });
    } finally {
      vi.useRealTimers();
    }
    await nextFrame();
    expect(document.activeElement).toBe(jam);
    await act(async () => {
      finishSave(new Response(JSON.stringify({ entryId: 'sli-oats' }), { status: 200, headers: { 'Content-Type': 'application/json' } }));
    });
    await nextFrame();
    expect(document.activeElement).toBe(jam);
  });
});

describe('ProvisionButton legacy reports', () => {
  it('shows the legacy sent count without inventing item names', async () => {
    mockExport({ provider: 'kroger', exported: 3 });
    renderButton();
    fireEvent.click(screen.getByRole('button', { name: 'Send to Kroger' }));
    const panel = await screen.findByRole('status', { name: 'Cart send result' });
    expect(within(panel).getByText('3 items sent to your Kroger cart.')).toBeInTheDocument();
    expect(within(panel).queryByText(/^Item \d/)).not.toBeInTheDocument();
    expect(within(panel).queryByText(/^Sent \(/)).not.toBeInTheDocument();
  });
});

describe('ProvisionResultPanel summary and sections (mutants)', () => {
  it('omits empty Sent and Check sections when only failures exist', async () => {
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
    expect(within(panel).getByText("Couldn't be sent (1)")).toBeInTheDocument();
    expect(within(panel).queryByText(/^Sent \(/)).not.toBeInTheDocument();
    expect(within(panel).queryByText(/^Check these in your cart/)).not.toBeInTheDocument();
    expect(within(panel).queryByText(/doesn't confirm each item/)).not.toBeInTheDocument();
  });

  it('Show all labels the number of sent rows, not summary N alone', async () => {
    const sent = Array.from({ length: 6 }, (_, index) => ({
      entryId: `s-${index}`,
      itemId: `i-${index}`,
      name: `Sent ${index}`,
      quantity: 1,
      status: 'sent' as const,
    }));
    mockExport({ provider: 'kroger', exported: 6, sentCount: 6, results: sent });
    renderButton();
    fireEvent.click(screen.getByRole('button', { name: 'Send to Kroger' }));
    const panel = await screen.findByRole('status', { name: 'Cart send result' });
    expect(within(panel).getByRole('button', { name: 'Show all 6' })).toBeInTheDocument();
    expect(within(panel).queryByRole('button', { name: 'Show all 4' })).not.toBeInTheDocument();
    expect(within(panel).getAllByText(/^Sent \d/)).toHaveLength(5);
  });

  it('shows singular and plural summary lines from grouped counts', async () => {
    mockExport({
      provider: 'kroger',
      exported: 1,
      results: [
        { entryId: 'a', itemId: '1', name: 'Milk', quantity: 1, status: 'sent' },
        { entryId: 'b', itemId: '2', name: 'Bread', quantity: 1, status: 'failed', reason: 'no_match' },
        { entryId: 'c', itemId: '3', name: 'Oats', quantity: 1, status: 'unknown' },
      ],
    });
    renderButton();
    fireEvent.click(screen.getByRole('button', { name: 'Send to Kroger' }));
    const panel = await screen.findByRole('status', { name: 'Cart send result' });
    expect(within(panel).getByText('1 item sent to your Kroger cart.')).toBeInTheDocument();
    expect(within(panel).getByText("1 couldn't be sent.")).toBeInTheDocument();
    expect(within(panel).getByText('1 might not have reached your cart.')).toBeInTheDocument();
    expect(within(panel).getByText(/doesn't confirm each item/)).toBeInTheDocument();
  });

  it('focuses the undo control after confirming a check row', async () => {
    stubExportAndResolve(oneUnknown);
    renderButton();
    fireEvent.click(screen.getByRole('button', { name: 'Send to Kroger' }));
    const panel = await screen.findByRole('status', { name: 'Cart send result' });
    fireEvent.click(within(panel).getByRole('button', { name: "It's not: Oats" }));
    await act(async () => { await new Promise((r) => requestAnimationFrame(() => r(undefined))); });
    expect(document.activeElement).toBe(within(panel).getByRole('button', { name: 'Undo: Oats' }));
  });
});
