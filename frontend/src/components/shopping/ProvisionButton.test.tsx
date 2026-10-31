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
    const alert = await within(row).findByRole('alert');
    expect(alert).toHaveTextContent("Couldn't save that. Try again.");
    expect(within(row).getByRole('button', { name: "It's not: Oats" })).toBeInTheDocument();
    expect(within(panel).queryByRole('alert')).toBe(alert);
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

  it('commits a pending choice when Send starts again during the undo window', async () => {
    const api = stubExportAndResolve(oneUnknown);
    renderButton();
    fireEvent.click(screen.getByRole('button', { name: 'Send to Kroger' }));
    const panel = await screen.findByRole('status', { name: 'Cart send result' });
    fireEvent.click(within(panel).getByRole('button', { name: "It's in my cart: Oats" }));
    fireEvent.click(screen.getByRole('button', { name: 'Send to Kroger' }));
    await flush();
    expect(api.resolutions).toEqual([{ provider: 'kroger', reachedProvider: true }]);
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
