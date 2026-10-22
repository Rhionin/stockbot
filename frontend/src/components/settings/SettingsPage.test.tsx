import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { MantineProvider } from '@mantine/core';
import { describe, expect, it, vi } from 'vitest';
import { SettingsPage } from './SettingsPage';

const jsonResponse = (body: unknown, status = 200) =>
  new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  });

const settings = { months: 3, opening: false, wipePhrase: 'WIPE INVENTORY' };
const restock = {
  household: { kind: 'months', value: 3 },
  householdLabel: '3 months',
  appliesTo: 1,
  itemCount: 1,
  items: [],
  categories: [],
  notRestocked: 0,
  suggestionCount: 0,
};

describe('SettingsPage', () => {
  it('has one household default and no separate supply-length field', async () => {
    const fetchMock = vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      const method = init?.method ?? 'GET';
      if (url === '/api/restock' && method === 'PUT') {
        return Promise.resolve(jsonResponse({ ...restock, household: { kind: 'months', value: 6 }, householdLabel: '6 months' }));
      }
      if (url === '/api/restock') {
        return Promise.resolve(jsonResponse(restock));
      }
      if (url === '/api/settings/supply') {
        return Promise.resolve(jsonResponse(settings));
      }
      throw new Error(`Unexpected request: ${method} ${url}`);
    });
    vi.stubGlobal('fetch', fetchMock);

    render(<MantineProvider><SettingsPage /></MantineProvider>);
    expect(await screen.findAllByText('Household default')).not.toHaveLength(0);
    expect(screen.getByText('3 months')).toBeInTheDocument();
    expect(screen.queryByText(/How far ahead the shopping list looks/)).not.toBeInTheDocument();
    expect(screen.getAllByRole('button', { name: 'Change' })).toHaveLength(1);

    fireEvent.click(screen.getByRole('button', { name: 'Change' }));
    expect(await screen.findByText('Months of supply')).toBeInTheDocument();
    expect(screen.getByText('Fixed amount')).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: 'Save' }));

    await waitFor(() => expect(fetchMock).toHaveBeenCalledWith(
      '/api/restock/household',
      expect.objectContaining({ method: 'PUT', body: JSON.stringify({ kind: 'months', value: 3 }) }),
    ));
    expect(fetchMock.mock.calls.some((call) => String(call[0]) === '/api/settings/supply' && call[1]?.method === 'PUT')).toBe(false);
  });

  it('requires the exact confirmation phrase before wiping inventory', async () => {
    const fetchMock = vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      const method = init?.method ?? 'GET';
      if (url === '/api/inventory/wipe' && method === 'POST') {
        return Promise.resolve(jsonResponse({}));
      }
      if (url === '/api/restock') {
        return Promise.resolve(jsonResponse(restock));
      }
      if (url === '/api/settings/supply') {
        return Promise.resolve(jsonResponse(settings));
      }
      throw new Error(`Unexpected request: ${method} ${url}`);
    });
    vi.stubGlobal('fetch', fetchMock);

    render(<MantineProvider><SettingsPage /></MantineProvider>);
    await screen.findAllByText('Household default');

    fireEvent.click(screen.getByRole('button', { name: 'Wipe inventory' }));
    const confirmButton = await screen.findByRole('button', { name: 'Confirm wipe' });
    expect(confirmButton).toBeDisabled();

    fireEvent.change(screen.getByLabelText('Type WIPE INVENTORY to confirm'), {
      target: { value: 'wipe inventory' },
    });
    expect(confirmButton).toBeDisabled();

    fireEvent.change(screen.getByLabelText('Type WIPE INVENTORY to confirm'), {
      target: { value: 'WIPE INVENTORY' },
    });
    expect(confirmButton).toBeEnabled();
    fireEvent.click(confirmButton);

    await waitFor(() => expect(fetchMock).toHaveBeenCalledWith('/api/inventory/wipe', expect.objectContaining({
      method: 'POST',
      body: JSON.stringify({ confirmation: 'WIPE INVENTORY' }),
    })));
  });

  it('keeps the page up when a wipe is rejected', async () => {
    const fetchMock = vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      const method = init?.method ?? 'GET';
      if (url === '/api/inventory/wipe' && method === 'POST') {
        return Promise.resolve(jsonResponse({ error: 'Type WIPE INVENTORY to confirm wiping the inventory.' }, 400));
      }
      if (url === '/api/restock') {
        return Promise.resolve(jsonResponse(restock));
      }
      if (url === '/api/settings/supply') {
        return Promise.resolve(jsonResponse(settings));
      }
      throw new Error(`Unexpected request: ${method} ${url}`);
    });
    vi.stubGlobal('fetch', fetchMock);

    render(<MantineProvider><SettingsPage /></MantineProvider>);
    await screen.findAllByText('Household default');

    fireEvent.click(screen.getByRole('button', { name: 'Wipe inventory' }));
    fireEvent.change(await screen.findByLabelText('Type WIPE INVENTORY to confirm'), {
      target: { value: 'WIPE INVENTORY' },
    });
    fireEvent.click(screen.getByRole('button', { name: 'Confirm wipe' }));

    expect(await screen.findByText('Type WIPE INVENTORY to confirm wiping the inventory.')).toBeInTheDocument();
    expect(screen.getAllByText('Household default').length).toBeGreaterThan(0);
  });
});
