import type { ReactElement } from 'react';
import { fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { MantineProvider } from '@mantine/core';
import { MemoryRouter } from 'react-router-dom';
import { describe, expect, it, vi } from 'vitest';
import type { InventoryItem, ShoppingListEntry } from '@stockbot/shared/types';
import { ShoppingListPage } from './ShoppingListPage';

const inventoryItem = (id: string, name: string): InventoryItem => ({
  item: {
    id,
    userId: 'user-1',
    productId: `product-${id}`,
    product: { id: `product-${id}`, name, category: 'Pantry', unitOfMeasure: 'boxes' },
    targetQuantity: 2,
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

const emptyStatus = () => ({
  cursor: '',
  listStale: false,
  items: [],
  offList: [],
  stores: [],
  attentionGroups: [],
  storeDisabled: null,
  lastTripEndedAt: null,
  lastTripEndedBy: null,
});

const renderPage = (ui: ReactElement) => render(
  <MantineProvider>
    <MemoryRouter>{ui}</MemoryRouter>
  </MantineProvider>,
);

describe('ShoppingListPage zero-for-trip inline confirm', () => {
  it('keeps A6 and nudge after refresh when adjustment is 0', async () => {
    vi.stubGlobal(
      'matchMedia',
      (query: string) =>
        ({
          matches: false,
          media: query,
          onchange: null,
          addListener: () => {},
          removeListener: () => {},
          addEventListener: () => {},
          removeEventListener: () => {},
          dispatchEvent: () => false,
        }) as unknown as MediaQueryList,
    );
    const entry = (): ShoppingListEntry => ({
      id: 'line-1',
      itemId: 'tea',
      quantity: 2,
      source: 'manual',
      purchasedAt: null,
      provider: 'kroger',
    });
    let listEntry = entry();
    const fetchMock = vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      const method = init?.method ?? 'GET';
      if (url === '/api/providers') {
        return Promise.resolve(jsonResponse([{
          id: 'kroger',
          displayName: 'Kroger',
          capabilities: { auth: 'oauth2_authorization_code', delivery: 'server_push', confirmation: 'per_request', mutation: 'add_only', identity: 'derived' },
          connectionState: 'connected',
          credentialsConfigured: true,
        }]));
      }
      if (url.includes('status=1')) {
        return Promise.resolve(jsonResponse(emptyStatus()));
      }
      if (url.startsWith('/api/shopping-list') && method === 'GET' && !url.includes('/items/')) {
        return Promise.resolve(jsonResponse([listEntry]));
      }
      if (url === '/api/inventory') {
        return Promise.resolve(jsonResponse([inventoryItem('tea', 'Green Tea')]));
      }
      if (url === '/api/restock') {
        return Promise.resolve(jsonResponse({
          household: { kind: 'months', value: 2 },
          householdLabel: '2 months',
          appliesTo: 1,
          itemCount: 1,
          items: [{
            itemId: 'tea',
            productId: 'p1',
            name: 'Green Tea',
            unit: 'boxes',
            target: { kind: 'amount', value: 2 },
            level: 'low',
            sourceLabel: '',
            valueLabel: '2',
            rowSource: '',
            chain: [],
            categories: [],
            setLabel: '',
          }],
          categories: [],
          notRestocked: 0,
          suggestionCount: 0,
        }));
      }
      if (url === '/api/shopping-list/items/line-1/adjustment?provider=kroger' && method === 'PUT') {
        const body = JSON.parse(String(init?.body)) as { adjustment: number };
        expect(body).toEqual({ adjustment: 0 });
        listEntry = { ...entry(), quantity: 0, adjustment: 0 };
        return Promise.resolve(jsonResponse({ adjustment: 0 }));
      }
      throw new Error(`Unexpected request: ${method} ${url}`);
    });
    vi.stubGlobal('fetch', fetchMock);
    renderPage(<ShoppingListPage />);

    const input = await screen.findByLabelText('Change amount for Green Tea');
    expect(input).toHaveValue('2');
    fireEvent.change(input, { target: { value: '0' } });
    fireEvent.blur(input);

    await waitFor(() => expect(screen.getByLabelText('Change amount for Green Tea')).toHaveValue('0'));
    await waitFor(() => expect(
      within(screen.getByRole('table', { name: 'Shopping plan entries' })).getByText('0 boxes'),
    ).toBeInTheDocument());
    expect(screen.getByText('Skipped for this trip. It comes back next trip.')).toBeInTheDocument();
    expect(screen.getByText("Don't want it back next trip?")).toBeInTheDocument();
  });
});