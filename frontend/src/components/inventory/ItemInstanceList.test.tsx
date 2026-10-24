import { fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { MantineProvider } from '@mantine/core';
import { describe, expect, it, vi } from 'vitest';
import type { ItemInstanceWithStatus } from '@stockbot/shared/types';
import { ItemInstanceList } from './ItemInstanceList';

const DAY_MS = 24 * 60 * 60 * 1000;

const instance = (
  id: string,
  expiresAt: string | null,
  expiryStatus: ItemInstanceWithStatus['expiryStatus'] = 'ok',
): ItemInstanceWithStatus => ({
  id,
  itemId: 'item-1',
  stockInAt: '2026-01-01T00:00:00Z',
  expiresAt,
  removedAt: null,
  removalReason: null,
  createdAt: '2026-01-01T00:00:00Z',
  expiryStatus,
});

const jsonResponse = (body: unknown, status = 200) =>
  new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  });

const renderList = (onHand: number, onHandChange = vi.fn(), onInventoryChanged = vi.fn()) => render(
  <MantineProvider>
    <ItemInstanceList
      itemId="item-1"
      productName="Milk"
      onHand={onHand}
      onHandChange={onHandChange}
      onInventoryChanged={onInventoryChanged}
    />
  </MantineProvider>,
);

describe('ItemInstanceList', () => {
  it('sorts use-oldest-first and computes expiry badges from expiration dates', async () => {
    const now = Date.now();
    const fetchMock = vi.fn(() => Promise.resolve(jsonResponse([
      instance('no-expiry', null, 'expired'),
      instance('near', new Date(now + (2 * DAY_MS)).toISOString()),
      instance('expired', new Date(now - (2 * DAY_MS)).toISOString()),
    ])));
    vi.stubGlobal('fetch', fetchMock);

    renderList(3);

    const rows = await screen.findAllByRole('listitem');
    expect(within(rows[0]).getByText('Expired')).toBeInTheDocument();
    expect(within(rows[1]).getByText('Near expiry')).toBeInTheDocument();
    expect(within(rows[2]).getByText('No expiration date')).toBeInTheDocument();
    expect(within(rows[2]).queryByText('Expired')).not.toBeInTheDocument();
    expect(fetchMock).toHaveBeenCalledWith(
      '/api/inventory/item-1/instances',
      expect.any(Object),
    );
    expect(within(rows[0]).queryByRole('button', { name: 'Stock in' })).not.toBeInTheDocument();
    expect(within(rows[0]).queryByRole('button', { name: 'Stock out' })).not.toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Stock out' })).toBeEnabled();
    expect(screen.queryByText('There is nothing on hand.')).not.toBeInTheDocument();
  });

  it('disables stock out when nothing is on hand', async () => {
    vi.stubGlobal('fetch', vi.fn(() => Promise.resolve(jsonResponse([]))));
    renderList(0);

    expect(await screen.findByRole('button', { name: 'Stock in' })).toBeEnabled();
    expect(screen.getByRole('button', { name: 'Stock out' })).toBeDisabled();
    expect(screen.getByText('There is nothing on hand.')).toBeInTheDocument();
  });

  it('stocks in one unit and can include an optional expiration', async () => {
    const fetchMock = vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      const method = init?.method ?? 'GET';
      if (url === '/api/inventory/item-1/instances' && method === 'GET') {
        return Promise.resolve(jsonResponse([]));
      }
      if (url === '/api/inventory/item-1/instances' && method === 'POST') {
        return Promise.resolve(jsonResponse({ id: 'instance-1', itemId: 'item-1' }, 201));
      }
      throw new Error(`Unexpected request: ${method} ${url}`);
    });
    vi.stubGlobal('fetch', fetchMock);
    const onHandChange = vi.fn();
    const onInventoryChanged = vi.fn();
    renderList(0, onHandChange, onInventoryChanged);

    fireEvent.click(await screen.findByRole('button', { name: 'Add expiration' }));
    fireEvent.change(screen.getByLabelText('Expiration date'), { target: { value: '2026-12-01' } });
    fireEvent.click(screen.getByRole('button', { name: 'Stock in' }));

    await waitFor(() => expect(onHandChange).toHaveBeenCalledWith(1));
    expect(onInventoryChanged).toHaveBeenCalledOnce();
    expect(fetchMock).toHaveBeenCalledWith(
      '/api/inventory/item-1/instances',
      expect.objectContaining({
        method: 'POST',
        body: JSON.stringify({ expiresAt: '2026-12-01T00:00:00.000Z' }),
      }),
    );
  });

  it('stocks out one unit from the product, not from an instance row', async () => {
    const fetchMock = vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      const method = init?.method ?? 'GET';
      if (url === '/api/inventory/item-1/instances' && method === 'GET') {
        return Promise.resolve(jsonResponse([instance('one', '2026-06-01T00:00:00Z')]));
      }
      if (url === '/api/inventory/item-1/stock-out' && method === 'POST') {
        return Promise.resolve(jsonResponse({}));
      }
      throw new Error(`Unexpected request: ${method} ${url}`);
    });
    vi.stubGlobal('fetch', fetchMock);
    const onHandChange = vi.fn();
    renderList(1, onHandChange);

    const row = await screen.findByRole('listitem');
    expect(within(row).queryByRole('button')).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: 'Stock out' }));

    await waitFor(() => expect(onHandChange).toHaveBeenCalledWith(-1));
    expect(fetchMock).toHaveBeenCalledWith(
      '/api/inventory/item-1/stock-out',
      expect.objectContaining({ method: 'POST' }),
    );
  });
});
