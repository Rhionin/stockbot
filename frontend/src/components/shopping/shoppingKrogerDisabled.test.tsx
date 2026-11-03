import { render, screen, within } from '@testing-library/react';
import { MantineProvider } from '@mantine/core';
import { Notifications } from '@mantine/notifications';
import { MemoryRouter } from 'react-router-dom';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import type { InventoryItem, ShoppingListEntry } from '@stockbot/shared/types';
import { ShoppingListPage } from './ShoppingListPage';

const inventoryItem = (id: string, name: string): InventoryItem => ({
  item: {
    id,
    userId: 'user-1',
    productId: `product-${id}`,
    product: { id: `product-${id}`, name, category: 'Pantry', unitOfMeasure: 'boxes' },
    targetQuantity: null,
    createdAt: '2026-01-01T00:00:00Z',
  },
  instanceCount: 1,
  nearExpiryCount: 0,
  expiredCount: 0,
  needsAttention: false,
});

const jsonResponse = (body: unknown, status = 200) =>
  new Response(JSON.stringify(body), {
    status,
    statusText: status >= 400 ? 'Request failed' : 'OK',
    headers: { 'Content-Type': 'application/json' },
  });

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

describe('shopping list with Kroger disabled on providers', () => {
  it('shows the list but hides connect, setup copy, and send controls', async () => {
    const entry: ShoppingListEntry = {
      id: 'manual-1',
      itemId: 'tea',
      quantity: 1,
      source: 'manual',
      purchasedAt: null,
    };
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url === '/api/providers') {
        return Promise.resolve(jsonResponse([{ id: 'kroger', displayName: 'Kroger', status: 'disabled' }]));
      }
      if (url === '/api/shopping-list' && init?.method === undefined) {
        return Promise.resolve(jsonResponse([entry]));
      }
      if (url === '/api/inventory') {
        return Promise.resolve(jsonResponse([inventoryItem('tea', 'Green Tea')]));
      }
      if (url === '/api/restock') {
        return Promise.resolve(jsonResponse(null, 404));
      }
      throw new Error(`Unexpected request: ${url}`);
    }));

    render(
      <MantineProvider>
        <Notifications />
        <MemoryRouter>
          <ShoppingListPage />
        </MemoryRouter>
      </MantineProvider>,
    );

    const table = await screen.findByRole('table', { name: 'Shopping plan entries' });
    expect(within(table).getByText('Green Tea')).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: /connect/i })).not.toBeInTheDocument();
    expect(screen.queryByText(/unconfigured|not configured|set up/i)).not.toBeInTheDocument();
    expect(screen.queryByText(/^disabled$/i)).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: /^Send to /i })).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: /Link to online cart/i })).not.toBeInTheDocument();
  });
});
