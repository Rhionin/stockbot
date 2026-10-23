import type { ReactElement } from 'react';
import { fireEvent, waitFor, within } from '@testing-library/react-native';
import { SafeAreaProvider } from 'react-native-safe-area-context';
import { BATCH_FAILURE_FALLBACK, OFFLINE_SENTENCE } from '../../../frontend/src/api/contract';
import type { InventoryItem, ScanEntry } from '../../../frontend/src/types';
import { createStockbotClient } from '../api/client';
import { renderScreen } from '../test/renderScreen';
import { QueueScreen } from './QueueScreen';

const RAW = ['Failed to fetch', 'Network request failed'];

const serverUrl = 'https://stockbot.example';

function entry(overrides: Partial<ScanEntry> = {}): ScanEntry {
  return {
    id: 'scan-1',
    userId: 'user-1',
    barcode: '111222333444',
    scannedAt: '2026-10-16T12:00:00Z',
    direction: 'stock_in',
    unitCount: 1,
    expiresAt: null,
    status: 'pending',
    productId: 'prod-1',
    product: { id: 'prod-1', name: 'Oat milk', category: 'Dairy', unitOfMeasure: 'carton' },
    committedAt: null,
    createdAt: '2026-10-16T12:00:00Z',
    ...overrides,
  };
}

function household(instanceCount: number): InventoryItem {
  return {
    item: {
      id: 'item-prod-1',
      userId: 'user-1',
      productId: 'prod-1',
      product: { id: 'prod-1', name: 'Oat milk', category: 'Dairy', unitOfMeasure: 'carton' },
      targetQuantity: null,
      createdAt: '2026-10-16T12:00:00Z',
    },
    instanceCount,
    nearExpiryCount: 0,
    expiredCount: 0,
    needsAttention: false,
  };
}

function jsonResponse(status: number, body: unknown): Response {
  return {
    ok: status >= 200 && status < 300,
    status,
    statusText: status === 200 ? 'OK' : 'Error',
    text: async () => JSON.stringify(body),
    json: async () => body,
  } as Response;
}

function framed(ui: ReactElement) {
  return (
    <SafeAreaProvider
      initialMetrics={{
        insets: { top: 0, right: 0, bottom: 0, left: 0 },
        frame: { x: 0, y: 0, width: 390, height: 844 },
      }}
    >
      {ui}
    </SafeAreaProvider>
  );
}

type Action = 'confirm' | 'all' | 'remove';

function queue(action: Action, failure: unknown) {
  const stockOut = action === 'remove';
  let pending = stockOut
    ? [entry({ direction: 'stock_out' })]
    : [entry(), entry({ id: 'scan-2', barcode: '999', productId: 'prod-2', product: { id: 'prod-2', name: 'Oats', category: 'Grain', unitOfMeasure: 'bag' } })];
  let open = true;
  const fetchImpl = jest.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const url = String(input);
    const method = init?.method ?? 'GET';
    if (url.includes('batch-commit') && method === 'POST') {
      if (open) throw failure;
      pending = [];
      return jsonResponse(200, { updatedCount: 2, committedIds: ['scan-1', 'scan-2'] });
    }
    if (url.endsWith('/commit') && method === 'POST') {
      if (open) throw failure;
      const id = url.split('/').at(-2) ?? '';
      pending = pending.filter((row) => row.id !== id);
      return jsonResponse(200, { ...entry({ id, status: 'committed' }) });
    }
    if (method === 'PATCH' && /\/api\/scans\/[^/]+$/.test(url)) {
      if (open) throw failure;
      const id = url.split('/').at(-1) ?? '';
      pending = pending.filter((row) => row.id !== id);
      return jsonResponse(200, { ...entry({ id, status: 'cancelled' }) });
    }
    if (url.includes('status=pending')) return jsonResponse(200, pending);
    if (url.includes('/api/inventory')) return jsonResponse(200, [household(stockOut ? 0 : 2)]);
    return jsonResponse(500, { error: `unexpected ${method} ${url}` });
  });
  const client = createStockbotClient({ serverUrl, password: 'correct-horse' }, { fetchImpl });
  return {
    client,
    succeed: () => {
      open = false;
    },
  };
}

describe('offline notice', () => {
  it.each([
    { name: 'single confirm', action: 'confirm' as const, failure: new TypeError('Network request failed') },
    { name: 'confirm all', action: 'all' as const, failure: new TypeError('Network request failed') },
    { name: 'remove', action: 'remove' as const, failure: new TypeError('Network request failed') },
    { name: 'timeout', action: 'confirm' as const, failure: Object.assign(new Error('The operation was aborted due to timeout'), { name: 'TimeoutError' }) },
    { name: 'abort', action: 'confirm' as const, failure: Object.assign(new Error('The operation was aborted.'), { name: 'AbortError' }) },
  ])('$name shows the sentence and a later success clears it', async ({ action, failure }) => {
    const api = queue(action, failure);
    const screen = await renderScreen(<QueueScreen client={api.client} warning="" />);

    expect(await screen.findByText('Oat milk')).toBeTruthy();
    if (action === 'all') {
      await fireEvent.press(screen.getByLabelText('Confirm all'));
    } else if (action === 'remove') {
      await fireEvent.press(screen.getByLabelText('Remove Oat milk'));
    } else {
      await fireEvent.press(screen.getByLabelText('Confirm Oat milk'));
    }

    expect(within(await screen.findByTestId('batch-banner')).getByText(OFFLINE_SENTENCE)).toBeTruthy();
    expect(screen.getAllByText(OFFLINE_SENTENCE)).toHaveLength(1);
    expect(screen.queryByTestId('confirm-toast')).toBeNull();
    expect(within(screen.getByTestId('scan-scan-1')).queryByText(OFFLINE_SENTENCE)).toBeNull();
    expect(screen.getByText('Oat milk')).toBeTruthy();
    if (action !== 'remove') expect(screen.getByLabelText('Confirm Oat milk')).toBeTruthy();
    else expect(screen.getByLabelText('Remove Oat milk')).toBeTruthy();
    for (const raw of RAW) expect(screen.queryByText(raw)).toBeNull();

    api.succeed();
    if (action === 'all') {
      await fireEvent.press(screen.getByLabelText('Confirm all'));
    } else if (action === 'remove') {
      await fireEvent.press(screen.getByLabelText('Remove Oat milk'));
    } else {
      await fireEvent.press(screen.getByLabelText('Confirm Oat milk'));
    }

    await waitFor(() => expect(screen.queryByText(OFFLINE_SENTENCE)).toBeNull());
    for (const raw of RAW) expect(screen.queryByText(raw)).toBeNull();
    await waitFor(() => expect(screen.queryByText('Oat milk')).toBeNull());
  });

  it('keeps a 409 body and leaves the scan open', async () => {
    const fetchImpl = jest.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      const method = init?.method ?? 'GET';
      if (url.endsWith('/commit') && method === 'POST') {
        return jsonResponse(409, { error: 'That scan is no longer pending.', code: 'not_pending' });
      }
      if (url.includes('status=pending')) return jsonResponse(200, [entry()]);
      if (url.includes('/api/inventory')) return jsonResponse(200, [household(2)]);
      return jsonResponse(500, { error: 'unexpected' });
    });
    const client = createStockbotClient({ serverUrl, password: 'correct-horse' }, { fetchImpl });
    const screen = await renderScreen(<QueueScreen client={client} warning="" />);

    await fireEvent.press(await screen.findByLabelText('Confirm Oat milk'));

    expect(await screen.findByText('That scan is no longer pending.')).toBeTruthy();
    expect(screen.queryByText(OFFLINE_SENTENCE)).toBeNull();
    expect(screen.getByText('Oat milk')).toBeTruthy();
    expect(within(screen.getByTestId('scan-scan-1')).getByLabelText('Confirm Oat milk')).toBeTruthy();
  });

  it('keeps the sentence until a refresh succeeds', async () => {
    const api = queue('confirm', new TypeError('Network request failed'));
    const screen = await renderScreen(<QueueScreen client={api.client} warning="" focused />);

    await fireEvent.press(await screen.findByLabelText('Confirm Oat milk'));
    expect(within(await screen.findByTestId('batch-banner')).getByText(OFFLINE_SENTENCE)).toBeTruthy();
    expect(screen.queryByTestId('confirm-toast')).toBeNull();

    await fireEvent.press(screen.getByLabelText('Confirm Oat milk'));
    expect(within(await screen.findByTestId('batch-banner')).getByText(OFFLINE_SENTENCE)).toBeTruthy();
    expect(within(screen.getByTestId('scan-scan-1')).queryByText(OFFLINE_SENTENCE)).toBeNull();
    expect(screen.getByText('Oat milk')).toBeTruthy();
    expect(screen.queryByText('Network request failed')).toBeNull();

    await screen.rerender(framed(<QueueScreen client={api.client} warning="" focused={false} />));
    await screen.rerender(framed(<QueueScreen client={api.client} warning="" focused />));

    await waitFor(() => expect(screen.queryByText(OFFLINE_SENTENCE)).toBeNull());
    expect(screen.getByText('Oat milk')).toBeTruthy();
    expect(screen.queryByText('Network request failed')).toBeNull();
  });

  it('replaces the batch banner and leaves the card failure', async () => {
    const itemFailure = 'That scan is no longer pending.';
    const oats = entry({
      id: 'scan-2',
      barcode: '999',
      productId: 'prod-2',
      product: { id: 'prod-2', name: 'Oats', category: 'Grain', unitOfMeasure: 'bag' },
    });
    let offline = false;
    const fetchImpl = jest.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      const method = init?.method ?? 'GET';
      if (url.includes('batch-commit') && method === 'POST') {
        return jsonResponse(200, {
          updatedCount: 0,
          committedIds: [],
          failedIds: ['scan-1'],
          failedReasons: [itemFailure],
          failedCodes: ['not_pending'],
        });
      }
      if (url.endsWith('/commit') && method === 'POST') {
        if (offline) throw new TypeError('Network request failed');
        return jsonResponse(200, { ...oats, status: 'committed' });
      }
      if (url.includes('status=pending')) return jsonResponse(200, [entry(), oats]);
      if (url.includes('/api/inventory')) return jsonResponse(200, [household(2)]);
      return jsonResponse(500, { error: 'unexpected' });
    });
    const client = createStockbotClient({ serverUrl, password: 'correct-horse' }, { fetchImpl });
    const screen = await renderScreen(<QueueScreen client={client} warning="" />);

    await fireEvent.press(await screen.findByLabelText('Confirm all'));
    expect(await screen.findByText(BATCH_FAILURE_FALLBACK)).toBeTruthy();
    expect(within(screen.getByTestId('scan-scan-1')).getByText(itemFailure)).toBeTruthy();

    offline = true;
    await fireEvent.press(screen.getByLabelText('Confirm Oats'));
    expect(within(await screen.findByTestId('batch-banner')).getByText(OFFLINE_SENTENCE)).toBeTruthy();
    expect(screen.queryByText(BATCH_FAILURE_FALLBACK)).toBeNull();
    expect(within(screen.getByTestId('scan-scan-1')).getByText(itemFailure)).toBeTruthy();
    expect(screen.queryByTestId('confirm-toast')).toBeNull();
    expect(screen.queryByText('Network request failed')).toBeNull();
  });
});
