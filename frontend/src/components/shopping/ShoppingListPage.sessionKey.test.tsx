import { fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { MantineProvider } from '@mantine/core';
import { MemoryRouter } from 'react-router-dom';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { getSession, resetStage1Client } from '../../api/client';
import { ShoppingListPage } from './ShoppingListPage';

class FakeEventSource {
  static latest: FakeEventSource | null = null;
  url: string;
  listeners = new Map<string, (event: MessageEvent) => void>();
  constructor(url: string) {
    this.url = url;
    FakeEventSource.latest = this;
  }
  addEventListener(type: string, listener: (event: MessageEvent) => void) {
    this.listeners.set(type, listener);
  }
  close() {}
  emit(type: string, data: unknown) {
    this.listeners.get(type)?.({ data: JSON.stringify(data) } as MessageEvent);
  }
}

const json = (body: unknown, status = 200) =>
  new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } });

const session = {
  user: { id: 'user-1', email: 'ada@example.com', name: 'Ada' },
  kind: 'account',
  memberships: [],
  households: [],
  activeHouseholdId: 'hh-1',
  operator: false,
};

const restock = (months: number) => ({
  household: { kind: 'months', value: 3 },
  householdLabel: '3 months',
  appliesTo: 1,
  itemCount: 1,
  notRestocked: 0,
  suggestionCount: 0,
  categories: [],
  items: [{
    itemId: 'tea',
    productId: 'product-tea',
    name: 'Green Tea',
    unit: 'boxes',
    target: { kind: 'months', value: months },
    level: 'item',
    sourceLabel: 'For this item',
    valueLabel: `${months} months`,
    rowSource: 'For this item',
    chain: [],
    categories: [],
    setLabel: 'Change',
  }],
});

describe('ShoppingListPage restock editor', () => {
  beforeEach(() => {
    resetStage1Client();
    FakeEventSource.latest = null;
    vi.stubGlobal('EventSource', FakeEventSource);
  });

  it('spec 729: typing in the shopping-list editor survives a restock refresh that changes initial', async () => {
    let months = 3;
    const fetchMock = vi.fn((input: RequestInfo | URL) => {
      const url = String(input);
      if (url === '/api/auth/session') return Promise.resolve(json(session));
      if (url === '/api/providers') return Promise.resolve(json([]));
      if (url.startsWith('/api/shopping-list')) {
        return Promise.resolve(json([
          { id: 'manual-1', itemId: 'tea', quantity: 1, source: 'manual', purchasedAt: null },
        ]));
      }
      if (url === '/api/inventory') {
        return Promise.resolve(json([{
          item: {
            id: 'tea',
            userId: 'user-1',
            productId: 'product-tea',
            product: { id: 'product-tea', name: 'Green Tea', category: 'Pantry', unitOfMeasure: 'boxes' },
            targetQuantity: null,
            createdAt: '2026-01-01T00:00:00Z',
          },
          instanceCount: 1,
          nearExpiryCount: 0,
          expiredCount: 0,
          needsAttention: false,
        }]));
      }
      if (url === '/api/restock') return Promise.resolve(json(restock(months)));
      return Promise.resolve(json([]));
    });
    vi.stubGlobal('fetch', fetchMock);
    await getSession();
    render(
      <MantineProvider env="test">
        <MemoryRouter>
          <ShoppingListPage />
        </MemoryRouter>
      </MantineProvider>,
    );
    fireEvent.click(await screen.findByRole('button', { name: 'Actions for Green Tea' }));
    fireEvent.click(screen.getByRole('menuitem', { name: 'Change target…' }));
    const dialog = await screen.findByRole('dialog', { name: 'Restock target' });
    const monthsInput = within(dialog).getByLabelText('Months');
    expect(monthsInput).toHaveValue('3');
    fireEvent.change(monthsInput, { target: { value: '8' } });
    expect(monthsInput).toHaveValue('8');
    months = 1;
    const restockCalls = () => fetchMock.mock.calls.filter((call) => String(call[0]) === '/api/restock').length;
    const before = restockCalls();
    FakeEventSource.latest?.emit('hint', { type: 'list' });
    await waitFor(() => expect(restockCalls()).toBeGreaterThan(before));
    expect(within(dialog).getByLabelText('Months')).toHaveValue('8');
  });
});
