import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { MantineProvider } from '@mantine/core';
import { notifications } from '@mantine/notifications';
import { ScanEntryCard } from './ScanEntryCard';
import type { ScanEntry } from '@stockbot/shared/types';

const barcode = '074182463298';
const catalogId = '074182463298';
const catalogName = 'Milk & Honey Scent Liquid Soap Refill';

interface Call {
  method: string;
  url: string;
  body?: unknown;
}

const resolvedEntry = (): ScanEntry => ({
  id: 'scan-569',
  userId: 'user-1',
  barcode,
  scannedAt: '2026-11-08T19:34:52.000Z',
  direction: 'stock_out',
  unitCount: 1,
  expiresAt: null,
  status: 'pending',
  productId: catalogId,
  product: {
    id: catalogId,
    name: catalogName,
    category: 'Personal care',
    unitOfMeasure: 'bottle',
  },
  committedAt: null,
  createdAt: '2026-11-08T19:34:52.000Z',
});

const unresolvedEntry = (): ScanEntry => ({
  ...resolvedEntry(),
  productId: null,
  product: undefined,
});

const json = (body: unknown, status = 200) =>
  new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } });

function stubQueueFetch(calls: Call[], onHand = 0) {
  vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
    const url = String(input);
    const method = init?.method ?? 'GET';
    const body = init?.body === undefined ? undefined : JSON.parse(String(init.body)) as unknown;
    calls.push({ method, url, body });
    if (url.endsWith('/commit') && method === 'POST') {
      return Promise.resolve(json({
        error: 'barcode is not linked',
        code: 'no_item',
        suggestions: [],
      }, 409));
    }
    if (url.startsWith('/api/products/lookup')) {
      return Promise.resolve(json({ product: null, source: '' }));
    }
    if (url === '/api/products' && method === 'POST') {
      const written = body as { name: string };
      return Promise.resolve(json({
        id: 'prod-typed-new',
        name: written.name,
        category: '',
        unitOfMeasure: '',
        createdAt: '2026-11-08T19:34:59.000Z',
      }, 201));
    }
    if (url === '/api/products/overrides' && method === 'POST') {
      return Promise.resolve(json({ ...(body as object), source: 'user' }));
    }
    if (url === `/api/products/${catalogId}` && method === 'PUT') {
      return Promise.resolve(json({
        id: catalogId,
        name: (body as { name: string }).name,
        category: 'Personal care',
        unitOfMeasure: 'bottle',
        createdAt: '2026-11-08T19:34:53.000Z',
      }));
    }
    if (url === '/api/scans/scan-569' && method === 'PATCH') {
      const written = body as { productId?: string; status?: string };
      return Promise.resolve(json({
        ...resolvedEntry(),
        productId: written.productId ?? catalogId,
        status: written.status ?? 'pending',
        onHand,
        product: {
          id: written.productId ?? catalogId,
          name: catalogName,
          category: 'Personal care',
          unitOfMeasure: 'bottle',
        },
      }));
    }
    if (url === '/api/inventory' || url.startsWith('/api/inventory?')) return Promise.resolve(json([]));
    return Promise.resolve(json([]));
  }));
}

async function openDefinePanel() {
  fireEvent.click(screen.getByRole('button', { name: 'Confirm' }));
  await screen.findByRole('region', { name: 'Which item is this?' });
}

const scanPatchCall = (calls: Call[]) =>
  calls.find((call) => call.url === '/api/scans/scan-569' && call.method === 'PATCH');

const displayNamePutCall = (calls: Call[]) =>
  calls.find((call) => call.url === `/api/products/${catalogId}` && call.method === 'PUT');

function linkCallIndex(calls: Call[]) {
  return calls.findIndex((call) => call.url === '/api/scans/scan-569' && call.method === 'PATCH');
}

function renameCallIndex(calls: Call[]) {
  return calls.findIndex((call) => call.url === `/api/products/${catalogId}` && call.method === 'PUT');
}

describe('issue 569: add as a new item on resolved stock-out', () => {
  beforeEach(() => {
    notifications.clean();
  });

  afterEach(() => {
    notifications.clean();
    vi.unstubAllGlobals();
  });

  it('links the scan to the resolved catalog product without creating a fork', async () => {
    const calls: Call[] = [];
    stubQueueFetch(calls, 0);
    const onChanged = vi.fn();

    render(
      <MantineProvider>
        <ScanEntryCard entry={resolvedEntry()} selected={false} onSelectedChange={vi.fn()} onChanged={onChanged} />
      </MantineProvider>,
    );

    await openDefinePanel();
    await waitFor(() => {
      expect(screen.getByRole('textbox', { name: 'Product name' })).toHaveValue(catalogName);
    });
    fireEvent.click(screen.getByRole('button', { name: 'Add as a new item' }));

    await waitFor(() => {
      expect(calls.some((call) => call.url === '/api/products' && call.method === 'POST')).toBe(false);
      expect(calls.some((call) => call.url === '/api/products/overrides' && call.method === 'POST')).toBe(false);
      expect(displayNamePutCall(calls)).toBeUndefined();
      const patch = scanPatchCall(calls);
      expect(patch?.body).toEqual({ productId: catalogId, status: 'pending' });
      expect(onChanged).toHaveBeenCalled();
      expect(screen.getByRole('button', { name: 'Add it and take it out' })).toBeInTheDocument();
    });
  });

  it('does not rename when the prefilled catalog name is accepted unchanged', async () => {
    const calls: Call[] = [];
    stubQueueFetch(calls, 0);

    render(
      <MantineProvider>
        <ScanEntryCard entry={resolvedEntry()} selected={false} onSelectedChange={vi.fn()} onChanged={vi.fn()} />
      </MantineProvider>,
    );

    await openDefinePanel();
    const nameField = screen.getByRole('textbox', { name: 'Product name' });
    await waitFor(() => expect(nameField).toHaveValue(catalogName));
    fireEvent.click(screen.getByRole('button', { name: 'Add as a new item' }));

    await waitFor(() => {
      expect(displayNamePutCall(calls)).toBeUndefined();
      expect(scanPatchCall(calls)?.body).toEqual({ productId: catalogId, status: 'pending' });
      expect(screen.getByRole('button', { name: 'Add it and take it out' })).toBeInTheDocument();
    });
  });

  it('saves an edited name as the household display name on the existing product', async () => {
    const calls: Call[] = [];
    stubQueueFetch(calls, 0);
    const edited = 'Milk & Honey refill';

    render(
      <MantineProvider>
        <ScanEntryCard entry={resolvedEntry()} selected={false} onSelectedChange={vi.fn()} onChanged={vi.fn()} />
      </MantineProvider>,
    );

    await openDefinePanel();
    fireEvent.change(screen.getByRole('textbox', { name: 'Product name' }), { target: { value: edited } });
    fireEvent.click(screen.getByRole('button', { name: 'Add as a new item' }));

    await waitFor(() => {
      const rename = displayNamePutCall(calls);
      expect(rename?.body).toMatchObject({
        name: edited,
        category: 'Personal care',
        unitOfMeasure: 'bottle',
      });
      expect(scanPatchCall(calls)?.body).toEqual({ productId: catalogId, status: 'pending' });
      expect(calls.some((call) => call.url === '/api/products' && call.method === 'POST')).toBe(false);
      expect(calls.some((call) => call.url === '/api/products/overrides' && call.method === 'POST')).toBe(false);
      expect(renameCallIndex(calls)).toBeGreaterThanOrEqual(0);
      expect(linkCallIndex(calls)).toBeGreaterThan(renameCallIndex(calls));
      expect(screen.getByRole('button', { name: 'Add it and take it out' })).toBeInTheDocument();
    });
  });

  it('still creates a product when the scan never resolved to one', async () => {
    const calls: Call[] = [];
    stubQueueFetch(calls, 0);
    const typed = 'Hand soap';

    render(
      <MantineProvider>
        <ScanEntryCard entry={unresolvedEntry()} selected={false} onSelectedChange={vi.fn()} onChanged={vi.fn()} />
      </MantineProvider>,
    );

    await openDefinePanel();
    fireEvent.change(screen.getByRole('textbox', { name: 'Product name' }), { target: { value: typed } });
    fireEvent.click(screen.getByRole('button', { name: 'Add as a new item' }));

    await waitFor(() => {
      const product = calls.find((call) => call.url === '/api/products' && call.method === 'POST');
      const override = calls.find((call) => call.url === '/api/products/overrides' && call.method === 'POST');
      const patch = calls.find((call) => call.url === '/api/scans/scan-569' && call.method === 'PATCH');
      expect(product?.body).toMatchObject({ name: typed, barcode });
      expect(override?.body).toEqual({ barcode, productId: 'prod-typed-new' });
      expect(patch?.body).toEqual({ productId: 'prod-typed-new', status: 'pending' });
    });
  });
});
