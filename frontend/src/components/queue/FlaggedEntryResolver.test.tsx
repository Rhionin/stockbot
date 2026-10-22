import { beforeEach, describe, expect, it, vi } from 'vitest';
import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { MantineProvider } from '@mantine/core';
import { FlaggedEntryResolver } from './FlaggedEntryResolver';
import type { Product, ScanEntry } from '../../types';

HTMLElement.prototype.scrollIntoView = vi.fn();

// Report prefers-reduced-motion so the MantineProvider theme below (which sets
// respectReducedMotion) collapses transitions to zero duration. This keeps the
// combobox dropdown and the resolve button's loader from scheduling a 150ms
// timer that could fire a React state update after jsdom's window is torn
// down, which otherwise fails the run with an unhandled ReferenceError even
// though every assertion passes. Scoped to this file so it does not affect
// components (e.g. ScanEntryCard) that assert on animated vs reduced-motion
// behavior; vitest isolates each test file's jsdom environment.
beforeEach(() => {
  vi.stubGlobal(
    'matchMedia',
    (query: string) =>
      ({
        matches: /prefers-reduced-motion/.test(query),
        media: query,
        onchange: null,
        addListener: () => {},
        removeListener: () => {},
        addEventListener: () => {},
        removeEventListener: () => {},
        dispatchEvent: () => false,
      }) as unknown as MediaQueryList,
  );
});

const flaggedEntry: ScanEntry = {
  id: 'scan-1',
  userId: 'default-user',
  barcode: '012345',
  scannedAt: '2026-03-20T10:00:00Z',
  direction: null,
  unitCount: 1,
  expiresAt: null,
  status: 'flagged',
  productId: null,
  product: null,
  committedAt: null,
  createdAt: '2026-03-20T10:00:00Z',
};

const milk: Product = {
  id: 'product-1',
  name: 'Whole Milk',
  category: 'Dairy',
  unitOfMeasure: 'carton',
  createdAt: '2026-03-20T10:00:00Z',
};

const yogurt: Product = {
  id: 'product-2',
  name: 'Greek Yogurt',
  category: 'Dairy',
  unitOfMeasure: 'tub',
  createdAt: '2026-03-20T11:00:00Z',
};

const jsonResponse = (body: unknown, status = 200) =>
  new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } });

// respectReducedMotion, paired with the prefers-reduced-motion stub above,
// collapses Mantine transitions (combobox dropdown, the resolve button's
// loader) to zero duration. This stops a transition timer from outliving the
// test and updating React state after jsdom's window is gone, which otherwise
// fails the run with an unhandled ReferenceError even though every assertion
// passes.
const renderResolver = (onResolved = vi.fn()) => {
  render(
    // env="test" disables Popover hideDetached so the combobox list stays
    // open in jsdom, which reports no layout box for the search field.
    <MantineProvider env="test" theme={{ respectReducedMotion: true }}>
      <FlaggedEntryResolver entry={flaggedEntry} onResolved={onResolved} />
    </MantineProvider>,
  );
  return onResolved;
};

describe('FlaggedEntryResolver', () => {
  it('selects a filtered product with the keyboard, creates an override, and moves the scan to pending', async () => {
    const resolvedEntry = { ...flaggedEntry, status: 'pending', productId: milk.id, product: milk };
    const fetchMock = vi.fn((input: RequestInfo | URL, request?: RequestInit) => {
      const url = String(input);
      if (url === '/api/products' && request?.method === undefined) {
        return Promise.resolve(jsonResponse([yogurt, milk]));
      }
      if (url === '/api/products/overrides') {
        return Promise.resolve(
          jsonResponse({ barcode: flaggedEntry.barcode, productId: milk.id }),
        );
      }
      if (url === '/api/scans/scan-1') return Promise.resolve(jsonResponse(resolvedEntry));
      throw new Error(`Unexpected request: ${url}`);
    });
    vi.stubGlobal('fetch', fetchMock);
    const onResolved = renderResolver();

    const search = screen.getByRole('combobox', { name: 'Search products' });
    fireEvent.change(search, { target: { value: 'whole' } });
    expect(search).toHaveAttribute('aria-expanded', 'true');
    expect(await screen.findByRole('option', { name: 'Whole Milk — Dairy' })).toBeInTheDocument();
    expect(screen.queryByRole('option', { name: 'Greek Yogurt — Dairy' })).not.toBeInTheDocument();
    fireEvent.keyDown(search, { key: 'ArrowDown', code: 'ArrowDown' });
    fireEvent.keyDown(search, { key: 'Enter', code: 'Enter' });

    expect(search).toHaveValue('Whole Milk — Dairy');
    const resolveButton = screen.getByRole('button', { name: 'Use selected product' });
    expect(resolveButton).toBeEnabled();
    fireEvent.click(resolveButton);

    await waitFor(() => expect(onResolved).toHaveBeenCalledWith(resolvedEntry));
    expect(fetchMock).toHaveBeenCalledWith(
      '/api/products/overrides',
      expect.objectContaining({
        method: 'POST',
        body: JSON.stringify({ barcode: '012345', productId: 'product-1' }),
      }),
    );
    expect(fetchMock).toHaveBeenCalledWith(
      '/api/scans/scan-1',
      expect.objectContaining({
        method: 'PATCH',
        body: JSON.stringify({ productId: 'product-1', status: 'pending' }),
      }),
    );
    const sequence = fetchMock.mock.calls.map((call) => `${String(call[0])} ${call[1]?.method ?? 'GET'}`);
    expect(sequence.indexOf('/api/scans/scan-1 PATCH')).toBeLessThan(sequence.indexOf('/api/products/overrides POST'));
  });

  it('clears the selected product when the search text changes', async () => {
    vi.stubGlobal('fetch', vi.fn(() => Promise.resolve(jsonResponse([milk]))));
    renderResolver();

    const search = screen.getByRole('combobox', { name: 'Search products' });
    fireEvent.click(search);
    fireEvent.click(await screen.findByRole('option', { name: 'Whole Milk — Dairy' }));
    expect(screen.getByRole('button', { name: 'Use selected product' })).toBeEnabled();

    fireEvent.change(search, { target: { value: 'something else' } });

    expect(screen.getByRole('button', { name: 'Use selected product' })).toBeDisabled();
    expect(await screen.findByText('No matching products.')).toBeInTheDocument();
  });

  it('creates a product and reloads it when the create response omits its generated ID', async () => {
    let productListCalls = 0;
    const fetchMock = vi.fn((input: RequestInfo | URL, request?: RequestInit) => {
      const url = String(input);
      if (url === '/api/products' && request?.method === undefined) {
        productListCalls += 1;
        return Promise.resolve(jsonResponse(productListCalls === 1 ? [] : [milk]));
      }
      if (url === '/api/products' && request?.method === 'POST') {
        return Promise.resolve(jsonResponse({ ...milk, id: '' }, 201));
      }
      if (url === '/api/products/overrides') return Promise.resolve(jsonResponse({}));
      if (url === '/api/scans/scan-1') return Promise.resolve(jsonResponse({ ...flaggedEntry, status: 'pending' }));
      throw new Error(`Unexpected request: ${url}`);
    });
    vi.stubGlobal('fetch', fetchMock);
    const onResolved = renderResolver();

    fireEvent.change(screen.getByRole('textbox', { name: /^Product name/ }), { target: { value: milk.name } });
    fireEvent.change(screen.getByLabelText('Category'), { target: { value: milk.category } });
    fireEvent.change(screen.getByLabelText('Package'), { target: { value: milk.unitOfMeasure } });
    fireEvent.click(screen.getByRole('button', { name: 'Create and use product' }));

    await waitFor(() => expect(onResolved).toHaveBeenCalled());
    expect(productListCalls).toBe(2);
  });

  it('sends a contribution only when sharing is on and this product is checked', async () => {
    const created = { ...milk, id: 'created-1' };
    const fetchMock = vi.fn((input: RequestInfo | URL, request?: RequestInit) => {
      const url = String(input);
      if (url === '/api/settings/contribution' && request?.method === 'PUT') {
        return Promise.resolve(jsonResponse({ enabled: true, configured: false }));
      }
      if (url === '/api/settings/contribution') {
        return Promise.resolve(jsonResponse({ enabled: false, configured: false }));
      }
      if (url === '/api/products' && request?.method === undefined) {
        return Promise.resolve(jsonResponse([]));
      }
      if (url === '/api/products' && request?.method === 'POST') {
        return Promise.resolve(jsonResponse({
          ...created,
          contribution: {
            status: 'not_configured',
            detail: 'Saved in your pantry. Nothing was sent because this Stockbot is not signed in to the open databases.',
          },
        }, 201));
      }
      if (url === '/api/products/overrides') return Promise.resolve(jsonResponse({}));
      if (url === '/api/scans/scan-1') return Promise.resolve(jsonResponse({ ...flaggedEntry, status: 'pending' }));
      throw new Error(`Unexpected request: ${url}`);
    });
    vi.stubGlobal('fetch', fetchMock);
    const onResolved = renderResolver();

    fireEvent.click(await screen.findByRole('switch', { name: 'Let me contribute products I type in' }));
    fireEvent.click(await screen.findByRole('checkbox', { name: 'Contribute this product' }));
    fireEvent.change(screen.getByLabelText('Open database'), { target: { value: 'openproductsfacts' } });
    fireEvent.change(screen.getByRole('textbox', { name: /^Product name/ }), { target: { value: milk.name } });
    fireEvent.change(screen.getByLabelText('Category'), { target: { value: milk.category } });
    fireEvent.change(screen.getByLabelText('Package'), { target: { value: milk.unitOfMeasure } });
    fireEvent.click(screen.getByRole('button', { name: 'Create and use product' }));

    await waitFor(() => expect(onResolved).toHaveBeenCalled());
    expect(fetchMock).toHaveBeenCalledWith(
      '/api/products',
      expect.objectContaining({
        method: 'POST',
        body: JSON.stringify({
          name: milk.name,
          category: milk.category,
          unitOfMeasure: milk.unitOfMeasure,
          contribute: true,
          contributeTo: 'openproductsfacts',
          barcode: flaggedEntry.barcode,
        }),
      }),
    );
  });

  const goneSentence = "That item isn't available anymore. Pick another.";

  it.each([
    {
      name: 'drops a product that is no longer available and does not save the barcode',
      status: 409,
      body: { error: goneSentence, code: 'link_target_gone' },
      sentence: goneSentence,
    },
  ])('$name', async (row) => {
    const fetchMock = vi.fn((input: RequestInfo | URL, request?: RequestInit) => {
      const url = String(input);
      if (url === '/api/products' && request?.method === undefined) {
        return Promise.resolve(jsonResponse([yogurt, milk]));
      }
      if (url === '/api/products/overrides') return Promise.resolve(jsonResponse({}));
      if (url === '/api/scans/scan-1') return Promise.resolve(jsonResponse(row.body, row.status));
      throw new Error(`Unexpected request: ${url}`);
    });
    vi.stubGlobal('fetch', fetchMock);
    renderResolver();

    const search = screen.getByRole('combobox', { name: 'Search products' });
    fireEvent.click(search);
    fireEvent.click(await screen.findByRole('option', { name: 'Whole Milk — Dairy' }));
    fireEvent.click(screen.getByRole('button', { name: 'Use selected product' }));

    expect(await screen.findByText(row.sentence)).toBeInTheDocument();
    expect(fetchMock).toHaveBeenCalledWith(
      '/api/scans/scan-1',
      expect.objectContaining({
        method: 'PATCH',
        body: JSON.stringify({ productId: 'product-1', status: 'pending' }),
      }),
    );
    expect(fetchMock.mock.calls.some((call) => String(call[0]) === '/api/products/overrides')).toBe(false);
    fireEvent.click(screen.getByRole('combobox', { name: 'Search products' }));
    expect(screen.queryByRole('option', { name: 'Whole Milk — Dairy' })).not.toBeInTheDocument();
    expect(await screen.findByRole('option', { name: 'Greek Yogurt — Dairy' })).toBeInTheDocument();
  });
});
