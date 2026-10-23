import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { MantineProvider } from '@mantine/core';
import { notifications, Notifications } from '@mantine/notifications';
import { ScanEntryCard } from './ScanEntryCard';
import { formatExpiryDate } from './queueUtils';
import type { ItemInstanceWithStatus, ScanEntry } from '../../types';

const entry: ScanEntry = {
  id: 'scan-1',
  userId: 'default-user',
  barcode: '123',
  scannedAt: '2026-03-20T10:00:00Z',
  direction: 'stock_out',
  unitCount: 1,
  expiresAt: null,
  status: 'pending',
  productId: 'product-1',
  product: { id: 'product-1', name: 'Milk', category: 'Dairy', unitOfMeasure: 'carton' },
  committedAt: null,
  createdAt: '2026-03-20T10:00:00Z',
};

const instances: ItemInstanceWithStatus[] = [
  { id: 'later', itemId: 'item-1', stockInAt: '2026-03-02T00:00:00Z', expiresAt: '2026-05-01T00:00:00Z', removedAt: null, removalReason: null, createdAt: '2026-03-02T00:00:00Z', expiryStatus: 'ok' },
  { id: 'oldest', itemId: 'item-1', stockInAt: '2026-03-01T00:00:00Z', expiresAt: '2026-04-01T00:00:00Z', removedAt: null, removalReason: null, createdAt: '2026-03-01T00:00:00Z', expiryStatus: 'ok' },
  { id: 'undated', itemId: 'item-1', stockInAt: '2026-03-03T00:00:00Z', expiresAt: null, removedAt: null, removalReason: null, createdAt: '2026-03-03T00:00:00Z', expiryStatus: 'ok' },
];

const jsonResponse = (body: unknown) =>
  new Response(JSON.stringify(body), { status: 200, headers: { 'Content-Type': 'application/json' } });

describe('ScanEntryCard stock-out review', () => {
  it('keeps a pending stock-out row lean and hides the expiration control', () => {
    vi.stubGlobal('fetch', vi.fn(() => Promise.resolve(jsonResponse(instances))));
    render(
      <MantineProvider>
        <ScanEntryCard
          entry={entry}
          itemId="item-1"
          selected={false}
          onSelectedChange={vi.fn()}
          onChanged={vi.fn()}
        />
      </MantineProvider>,
    );

    const row = screen.getByRole('article', { name: 'Scan 123' });
    expect(row).toHaveClass('scan-entry-row');
    expect(row).not.toHaveClass('mantine-Card-root');
    expect(within(row).getByText('Barcode: 123')).toBeInTheDocument();
    expect(within(row).getByRole('button', { name: 'Decrease unit count' })).toBeInTheDocument();
    expect(within(row).getByRole('button', { name: 'Increase unit count' })).toBeInTheDocument();
    expect(screen.queryByLabelText('Expiration date')).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Add expiration' })).not.toBeInTheDocument();
    expect(screen.queryByRole('radio', { name: 'Use oldest available automatically' })).not.toBeInTheDocument();
    expect(screen.queryByText('Nothing on hand to take out.')).not.toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'More', expanded: false })).toBeInTheDocument();
  });

  it('shows the amber nothing-on-hand card with Remove and a disabled checkbox', () => {
    vi.stubGlobal('fetch', vi.fn());
    render(
      <MantineProvider>
        <ScanEntryCard
          entry={entry}
          nothingOnHand
          selected={false}
          onSelectedChange={vi.fn()}
          onChanged={vi.fn()}
        />
      </MantineProvider>,
    );

    const card = screen.getByRole('article', { name: 'Scan 123, Nothing on hand to take out.' });
    expect(card).toHaveClass('scan-entry-card--nothing');
    expect(within(card).getByText('Nothing on hand to take out.')).toBeInTheDocument();
    expect(within(card).getByText("Stockbot thinks you have none, so this can't be confirmed yet. Stock it in first, or remove this scan.")).toBeInTheDocument();
    expect(within(card).getByText('!')).toBeInTheDocument();
    expect(within(card).queryByRole('button', { name: 'Confirm' })).not.toBeInTheDocument();
    expect(within(card).queryByRole('button', { name: 'More' })).not.toBeInTheDocument();
    expect(within(card).getByRole('button', { name: 'Remove' })).toBeInTheDocument();
    const checkbox = within(card).getByRole('checkbox', { name: 'Select scan for batch approval' });
    expect(checkbox).toBeDisabled();
    expect(checkbox).toHaveAttribute('aria-describedby', within(card).getByText("Stockbot thinks you have none, so this can't be confirmed yet. Stock it in first, or remove this scan.").id);
    expect(screen.queryByLabelText('Expiration date')).not.toBeInTheDocument();
  });

  it('does not render the expiration control by default even when the scan has a date', () => {
    vi.stubGlobal('fetch', vi.fn());
    render(
      <MantineProvider>
        <ScanEntryCard
          entry={{ ...entry, expiresAt: '2026-05-15T00:00:00Z' }}
          itemId="item-1"
          selected={false}
          onSelectedChange={vi.fn()}
          onChanged={vi.fn()}
        />
      </MantineProvider>,
    );

    expect(screen.queryByLabelText('Expiration date')).not.toBeInTheDocument();
    expect(screen.queryByText(/Expires /)).not.toBeInTheDocument();
  });

  it('orders instances oldest first and commits the specifically selected instance', async () => {
    const fetchMock = vi.fn((input: RequestInfo | URL, request?: RequestInit) => {
      const url = String(input);
      if (url === '/api/inventory/item-1/instances') return Promise.resolve(jsonResponse(instances));
      if (url === '/api/scans/scan-1/commit') return Promise.resolve(jsonResponse({ ...entry, status: 'committed' }));
      throw new Error(`Unexpected ${request?.method ?? 'GET'} request: ${url}`);
    });
    vi.stubGlobal('fetch', fetchMock);
    const onChanged = vi.fn();
    render(
      <MantineProvider>
        <ScanEntryCard
          entry={entry}
          itemId="item-1"
          selected={false}
          onSelectedChange={vi.fn()}
          onChanged={onChanged}
        />
      </MantineProvider>,
    );

    fireEvent.click(screen.getByRole('button', { name: 'More', expanded: false }));
    const options = await screen.findAllByRole('radio');
    expect(screen.getByRole('button', { name: 'More', expanded: true })).toBeInTheDocument();
    expect(screen.queryByLabelText('Expiration date')).not.toBeInTheDocument();
    expect(options.map((option) => option.getAttribute('value'))).toEqual(['', 'oldest', 'later', 'undated']);
    fireEvent.click(options[2]);
    fireEvent.click(screen.getByRole('button', { name: 'Confirm' }));

    await waitFor(() => expect(onChanged).toHaveBeenCalled());
    const commitCall = fetchMock.mock.calls.find(([url]) => String(url).includes('/commit'));
    expect(JSON.parse(commitCall?.[1]?.body as string)).toEqual({ instanceId: 'later' });
  });
});

describe('ScanEntryCard approve scan rename', () => {
  it('shows the approve button with correct text and aria-label', async () => {
    const fetchMock = vi.fn((input: RequestInfo | URL, request?: RequestInit) => {
      const url = String(input);
      if (url === '/api/inventory/item-1/instances') return Promise.resolve(jsonResponse(instances));
      if (url === '/api/scans/scan-1/commit') return Promise.resolve(jsonResponse({ ...entry, status: 'committed' }));
      throw new Error(`Unexpected ${request?.method ?? 'GET'} request: ${url}`);
    });
    vi.stubGlobal('fetch', fetchMock);

    render(
      <MantineProvider>
        <ScanEntryCard
          entry={{ ...entry, direction: 'stock_out' }}
          itemId="item-1"
          selected={false}
          onSelectedChange={vi.fn()}
          onChanged={vi.fn()}
        />
      </MantineProvider>,
    );

    const button = screen.getByRole('button', { name: 'Confirm' });
    expect(button).toBeInTheDocument();
  });

  it('shows error message on simulated failure', async () => {
    const fetchMock = vi.fn((input: RequestInfo | URL, request?: RequestInit) => {
      const url = String(input);
      if (url === '/api/inventory/item-1/instances') return Promise.resolve(jsonResponse(instances));
      if (url === '/api/scans/scan-1/commit') return Promise.reject(new Error('Network error'));
      throw new Error(`Unexpected ${request?.method ?? 'GET'} request: ${url}`);
    });
    vi.stubGlobal('fetch', fetchMock);

    notifications.clean();
    render(
      <MantineProvider>
        <Notifications />
        <ScanEntryCard
          entry={{ ...entry, direction: 'stock_out' }}
          itemId="item-1"
          selected={false}
          onSelectedChange={vi.fn()}
          onChanged={vi.fn()}
        />
      </MantineProvider>,
    );

    const button = screen.getByRole('button', { name: 'Confirm' });
    fireEvent.click(button);

    await waitFor(() => {
      const leaves = screen.getAllByText('Network error').filter((node) => node.childElementCount === 0);
      expect(leaves).toHaveLength(1);
      expect(leaves[0]?.closest('.restock-toast')).not.toBeNull();
    });
    expect(within(screen.getByRole('article', { name: 'Scan 123' })).queryByRole('alert')).not.toBeInTheDocument();
    notifications.clean();
  });

  it('shows fallback error message "Unable to confirm scan." when error is not an Error instance', async () => {
    const fetchMock = vi.fn((input: RequestInfo | URL, request?: RequestInit) => {
      const url = String(input);
      if (url === '/api/inventory/item-1/instances') return Promise.resolve(jsonResponse(instances));
      if (url === '/api/scans/scan-1/commit') return Promise.reject({ statusCode: 500 });
      throw new Error(`Unexpected ${request?.method ?? 'GET'} request: ${url}`);
    });
    vi.stubGlobal('fetch', fetchMock);

    notifications.clean();
    render(
      <MantineProvider>
        <Notifications />
        <ScanEntryCard
          entry={{ ...entry, direction: 'stock_out' }}
          itemId="item-1"
          selected={false}
          onSelectedChange={vi.fn()}
          onChanged={vi.fn()}
        />
      </MantineProvider>,
    );

    const button = screen.getByRole('button', { name: 'Confirm' });
    fireEvent.click(button);

    await waitFor(() => {
      const leaves = screen.getAllByText('Unable to confirm scan.').filter((node) => node.childElementCount === 0);
      expect(leaves).toHaveLength(1);
      expect(leaves[0]?.closest('.restock-toast')).not.toBeNull();
    });
    expect(within(screen.getByRole('article', { name: 'Scan 123' })).queryByRole('alert')).not.toBeInTheDocument();
    notifications.clean();
  });
});

describe('ScanEntryCard remove control', () => {
  it('shows the remove button for pending entries with direction', async () => {
    const fetchMock = vi.fn();
    vi.stubGlobal('fetch', fetchMock);

    render(
      <MantineProvider>
        <ScanEntryCard
          entry={{ ...entry, status: 'pending', direction: 'stock_out' }}
          itemId="item-1"
          selected={false}
          onSelectedChange={vi.fn()}
          onChanged={vi.fn()}
        />
      </MantineProvider>,
    );

    const button = screen.getByRole('button', { name: 'Remove' });
    expect(button).toBeInTheDocument();
  });

  it('shows the remove button for flagged entries', async () => {
    const fetchMock = vi.fn();
    vi.stubGlobal('fetch', fetchMock);

    render(
      <MantineProvider>
        <ScanEntryCard
          entry={{ ...entry, status: 'flagged', direction: null }}
          itemId="item-1"
          selected={false}
          onSelectedChange={vi.fn()}
          onChanged={vi.fn()}
        />
      </MantineProvider>,
    );

    const button = screen.getByRole('button', { name: 'Remove' });
    expect(button).toBeInTheDocument();
  });

  it('sends the cancelled PATCH on successful removal', async () => {
    const fetchMock = vi.fn((input: RequestInfo | URL, request?: RequestInit) => {
      const url = String(input);
      if (url === '/api/inventory/item-1/instances') return Promise.resolve(jsonResponse(instances));
      if (url === '/api/scans/scan-1') {
        expect(request?.method).toBe('PATCH');
        expect(JSON.parse(request?.body as string)).toEqual({ status: 'cancelled' });
        return Promise.resolve(jsonResponse({ ...entry, status: 'cancelled' }));
      }
      throw new Error(`Unexpected ${request?.method ?? 'GET'} request: ${url}`);
    });
    vi.stubGlobal('fetch', fetchMock);
    const onChanged = vi.fn();

    render(
      <MantineProvider>
        <ScanEntryCard
          entry={{ ...entry, status: 'pending', direction: 'stock_out' }}
          itemId="item-1"
          selected={false}
          onSelectedChange={vi.fn()}
          onChanged={onChanged}
        />
      </MantineProvider>,
    );

    const button = screen.getByRole('button', { name: 'Remove' });
    fireEvent.click(button);

    await waitFor(() => expect(onChanged).toHaveBeenCalled());
  });

  it('shows error message on simulated failure and keeps the card rendered', async () => {
    const fetchMock = vi.fn((input: RequestInfo | URL, request?: RequestInit) => {
      const url = String(input);
      if (url === '/api/inventory/item-1/instances') return Promise.resolve(jsonResponse(instances));
      if (url === '/api/scans/scan-1') return Promise.reject(new Error('Network error'));
      throw new Error(`Unexpected ${request?.method ?? 'GET'} request: ${url}`);
    });
    vi.stubGlobal('fetch', fetchMock);

    render(
      <MantineProvider>
        <ScanEntryCard
          entry={{ ...entry, status: 'pending', direction: 'stock_out' }}
          itemId="item-1"
          selected={false}
          onSelectedChange={vi.fn()}
          onChanged={vi.fn()}
        />
      </MantineProvider>,
    );

    const button = screen.getByRole('button', { name: 'Remove' });
    fireEvent.click(button);

    await waitFor(() => {
      const errorAlert = screen.getByRole('alert');
      expect(errorAlert).toBeInTheDocument();
      expect(errorAlert).toHaveTextContent('Network error');
    });

    const card = screen.getByRole('article', { name: 'Scan 123' });
    expect(card).toBeInTheDocument();
  });

  it('shows fallback error message "Unable to remove scan." when error is not an Error instance', async () => {
    const fetchMock = vi.fn((input: RequestInfo | URL, request?: RequestInit) => {
      const url = String(input);
      if (url === '/api/inventory/item-1/instances') return Promise.resolve(jsonResponse(instances));
      if (url === '/api/scans/scan-1') return Promise.reject({ statusCode: 500 });
      throw new Error(`Unexpected ${request?.method ?? 'GET'} request: ${url}`);
    });
    vi.stubGlobal('fetch', fetchMock);

    render(
      <MantineProvider>
        <ScanEntryCard
          entry={{ ...entry, status: 'pending', direction: 'stock_out' }}
          itemId="item-1"
          selected={false}
          onSelectedChange={vi.fn()}
          onChanged={vi.fn()}
        />
      </MantineProvider>,
    );

    const button = screen.getByRole('button', { name: 'Remove' });
    fireEvent.click(button);

    await waitFor(() => {
      const errorAlert = screen.getByRole('alert');
      expect(errorAlert).toBeInTheDocument();
      expect(errorAlert).toHaveTextContent('Unable to remove scan.');
    });
  });
});

describe('ScanEntryCard unit count controls', () => {
  it('shows unit count input when entry.status is pending', async () => {
    const fetchMock = vi.fn();
    vi.stubGlobal('fetch', fetchMock);

    render(
      <MantineProvider>
        <ScanEntryCard
          entry={{ ...entry, unitCount: 3 }}
          itemId="item-1"
          selected={false}
          onSelectedChange={vi.fn()}
          onChanged={vi.fn()}
        />
      </MantineProvider>,
    );

    expect(screen.getByLabelText('Unit count')).toBeInTheDocument();
  });

  it('does not show unit count input when entry.status is not pending', async () => {
    const fetchMock = vi.fn();
    vi.stubGlobal('fetch', fetchMock);

    render(
      <MantineProvider>
        <ScanEntryCard
          entry={{ ...entry, status: 'flagged' }}
          itemId="item-1"
          selected={false}
          onSelectedChange={vi.fn()}
          onChanged={vi.fn()}
        />
      </MantineProvider>,
    );

    expect(screen.queryByLabelText('Unit count')).not.toBeInTheDocument();
  });
});

describe('ScanEntryCard direct numeric input', () => {
  it('sends expected PATCH for a valid confirmed value', async () => {
    const fetchMock = vi.fn((input: RequestInfo | URL, request?: RequestInit) => {
      const url = String(input);
      if (url === '/api/scans/scan-1') {
        expect(request?.method).toBe('PATCH');
        expect(JSON.parse(request?.body as string)).toEqual({ unitCount: 5 });
        return Promise.resolve(jsonResponse({ ...entry, unitCount: 5 }));
      }
      throw new Error(`Unexpected ${request?.method ?? 'GET'} request: ${url}`);
    });
    vi.stubGlobal('fetch', fetchMock);
    const onChanged = vi.fn();

    render(
      <MantineProvider>
        <ScanEntryCard
          entry={{ ...entry, unitCount: 3 }}
          itemId="item-1"
          selected={false}
          onSelectedChange={vi.fn()}
          onChanged={onChanged}
        />
      </MantineProvider>,
    );

    const input = screen.getByLabelText('Unit count') as HTMLInputElement;
    expect(input).toHaveValue('3');
    fireEvent.change(input, { target: { value: '5' } });
    fireEvent.blur(input);

    await waitFor(() => {
      expect(onChanged).toHaveBeenCalled();
    });
  });

  it('rejects invalid value (< 1) client-side with displayed count unchanged', async () => {
    const fetchMock = vi.fn();
    vi.stubGlobal('fetch', fetchMock);

    render(
      <MantineProvider>
        <ScanEntryCard
          entry={{ ...entry, unitCount: 3 }}
          itemId="item-1"
          selected={false}
          onSelectedChange={vi.fn()}
          onChanged={vi.fn()}
        />
      </MantineProvider>,
    );

    const input = screen.getByLabelText('Unit count') as HTMLInputElement;
    expect(input).toHaveValue('3');
    
    fireEvent.change(input, { target: { value: '0' } });
    fireEvent.blur(input);
    
    expect(input).toHaveValue('3');
    const patchCalls = fetchMock.mock.calls.filter(([url]) => url.includes('/scans/scan-1'));
    expect(patchCalls.length).toBe(0);
  });

  it('rejects invalid value (non-integer) client-side with displayed count unchanged', async () => {
    const fetchMock = vi.fn();
    vi.stubGlobal('fetch', fetchMock);

    render(
      <MantineProvider>
        <ScanEntryCard
          entry={{ ...entry, unitCount: 3 }}
          itemId="item-1"
          selected={false}
          onSelectedChange={vi.fn()}
          onChanged={vi.fn()}
        />
      </MantineProvider>,
    );

    const input = screen.getByLabelText('Unit count') as HTMLInputElement;
    expect(input).toHaveValue('3');
    
    fireEvent.change(input, { target: { value: '2.5' } });
    expect(input).toHaveValue('2');
    
    fireEvent.blur(input);
    
    const patchCalls = fetchMock.mock.calls.filter(([url]) => url.includes('/scans/scan-1'));
    expect(patchCalls.length).toBe(1);
    expect(JSON.parse(patchCalls[0][1]?.body as string)).toEqual({ unitCount: 2 });
  });

  it('shows error and reverts to previously confirmed count on failed PATCH', async () => {
    const fetchMock = vi.fn((input: RequestInfo | URL, request?: RequestInit) => {
      const url = String(input);
      if (url === '/api/scans/scan-1') {
        return Promise.reject(new Error('Network error'));
      }
      throw new Error(`Unexpected ${request?.method ?? 'GET'} request: ${url}`);
    });
    vi.stubGlobal('fetch', fetchMock);

    render(
      <MantineProvider>
        <ScanEntryCard
          entry={{ ...entry, unitCount: 3 }}
          itemId="item-1"
          selected={false}
          onSelectedChange={vi.fn()}
          onChanged={vi.fn()}
        />
      </MantineProvider>,
    );

    const input = screen.getByLabelText('Unit count') as HTMLInputElement;
    expect(input).toHaveValue('3');
    fireEvent.change(input, { target: { value: '5' } });
    fireEvent.blur(input);

    await waitFor(() => {
      const errorAlerts = screen.getAllByRole('alert');
      const unitCountError = errorAlerts.find(alert => 
        alert.textContent?.includes('Unable to update unit count.')
      );
      expect(unitCountError).toBeInTheDocument();
    });

    expect(input).toHaveValue('3');
  });

  it('shows fallback error message when error is not an Error instance', async () => {
    const fetchMock = vi.fn((input: RequestInfo | URL, request?: RequestInit) => {
      const url = String(input);
      if (url === '/api/scans/scan-1') {
        return Promise.reject({ statusCode: 500 });
      }
      throw new Error(`Unexpected ${request?.method ?? 'GET'} request: ${url}`);
    });
    vi.stubGlobal('fetch', fetchMock);

    render(
      <MantineProvider>
        <ScanEntryCard
          entry={{ ...entry, unitCount: 3 }}
          itemId="item-1"
          selected={false}
          onSelectedChange={vi.fn()}
          onChanged={vi.fn()}
        />
      </MantineProvider>,
    );

    const input = screen.getByLabelText('Unit count') as HTMLInputElement;
    expect(input).toHaveValue('3');
    fireEvent.change(input, { target: { value: '5' } });
    fireEvent.blur(input);

    await waitFor(() => {
      const errorAlerts = screen.getAllByRole('alert');
      const unitCountError = errorAlerts.find(alert => 
        alert.textContent?.includes('Unable to update unit count.')
      );
      expect(unitCountError).toBeInTheDocument();
    });
  });

  it('clears error when user starts typing after error', async () => {
    const fetchMock = vi.fn((input: RequestInfo | URL, request?: RequestInit) => {
      const url = String(input);
      if (url === '/api/scans/scan-1') {
        return Promise.reject(new Error('Network error'));
      }
      throw new Error(`Unexpected ${request?.method ?? 'GET'} request: ${url}`);
    });
    vi.stubGlobal('fetch', fetchMock);

    render(
      <MantineProvider>
        <ScanEntryCard
          entry={{ ...entry, unitCount: 3 }}
          itemId="item-1"
          selected={false}
          onSelectedChange={vi.fn()}
          onChanged={vi.fn()}
        />
      </MantineProvider>,
    );

    const input = screen.getByLabelText('Unit count') as HTMLInputElement;
    expect(input).toHaveValue('3');
    fireEvent.change(input, { target: { value: '5' } });
    fireEvent.blur(input);

    await waitFor(() => {
      const errorAlerts = screen.getAllByRole('alert');
      const unitCountError = errorAlerts.find(alert => 
        alert.textContent?.includes('Unable to update unit count.')
      );
      expect(unitCountError).toBeInTheDocument();
    });

    fireEvent.change(input, { target: { value: '4' } });
    
    const errorAlertsAfter = screen.queryAllByRole('alert');
    const unitCountErrorAfter = errorAlertsAfter.find(alert => 
      alert.textContent?.includes('Unable to update unit count.')
    );
    expect(unitCountErrorAfter).toBeUndefined();
  });

  it('sends PATCH on Enter key press', async () => {
    const fetchMock = vi.fn((input: RequestInfo | URL, request?: RequestInit) => {
      const url = String(input);
      if (url === '/api/scans/scan-1') {
        expect(request?.method).toBe('PATCH');
        expect(JSON.parse(request?.body as string)).toEqual({ unitCount: 4 });
        return Promise.resolve(jsonResponse({ ...entry, unitCount: 4 }));
      }
      throw new Error(`Unexpected ${request?.method ?? 'GET'} request: ${url}`);
    });
    vi.stubGlobal('fetch', fetchMock);
    const onChanged = vi.fn();

    render(
      <MantineProvider>
        <ScanEntryCard
          entry={{ ...entry, unitCount: 3 }}
          itemId="item-1"
          selected={false}
          onSelectedChange={vi.fn()}
          onChanged={onChanged}
        />
      </MantineProvider>,
    );

    const input = screen.getByLabelText('Unit count');
    expect(input).toHaveValue('3');
    fireEvent.change(input, { target: { value: '4' } });
    fireEvent.keyDown(input, { key: 'Enter' });

    await waitFor(() => {
      expect(onChanged).toHaveBeenCalled();
    });
  });
});

describe('ScanEntryCard change indicator', () => {
  beforeEach(() => {
    vi.useFakeTimers();
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  it('shows change indicator on mount with animated class by default', () => {
    const fetchMock = vi.fn();
    vi.stubGlobal('fetch', fetchMock);

    render(
      <MantineProvider>
        <ScanEntryCard
          entry={entry}
          itemId="item-1"
          selected={false}
          onSelectedChange={vi.fn()}
          onChanged={vi.fn()}
        />
      </MantineProvider>,
    );

    const card = screen.getByRole('article', { name: 'Scan 123' });
    expect(card).toHaveClass('scan-entry-card--changed-animated');
  });

  it('hides change indicator after 600ms', () => {
    const fetchMock = vi.fn();
    vi.stubGlobal('fetch', fetchMock);

    render(
      <MantineProvider>
        <ScanEntryCard
          entry={entry}
          itemId="item-1"
          selected={false}
          onSelectedChange={vi.fn()}
          onChanged={vi.fn()}
        />
      </MantineProvider>,
    );

    const card = screen.getByRole('article', { name: 'Scan 123' });
    expect(card).toHaveClass('scan-entry-card--changed-animated');

    act(() => {
      vi.advanceTimersByTime(600);
    });

    expect(card).not.toHaveClass('scan-entry-card--changed-animated');
    expect(card).not.toHaveClass('scan-entry-card--changed-static');
  });

  it('shows change indicator when unitCount changes with animated class', async () => {
    const fetchMock = vi.fn();
    vi.stubGlobal('fetch', fetchMock);

    const { rerender } = render(
      <MantineProvider>
        <ScanEntryCard
          entry={entry}
          itemId="item-1"
          selected={false}
          onSelectedChange={vi.fn()}
          onChanged={vi.fn()}
        />
      </MantineProvider>,
    );

    act(() => {
      vi.advanceTimersByTime(600);
    });

    const card = screen.getByRole('article', { name: 'Scan 123' });
    expect(card).not.toHaveClass('scan-entry-card--changed-animated');

    rerender(
      <MantineProvider>
        <ScanEntryCard
          entry={{ ...entry, unitCount: 5 }}
          itemId="item-1"
          selected={false}
          onSelectedChange={vi.fn()}
          onChanged={vi.fn()}
        />
      </MantineProvider>,
    );

    expect(card).toHaveClass('scan-entry-card--changed-animated');
  });

  it('shows change indicator with static class when prefers-reduced-motion is set', () => {
    const fetchMock = vi.fn();
    vi.stubGlobal('fetch', fetchMock);

    vi.stubGlobal(
      'matchMedia',
      vi.fn(() => ({
        matches: true,
        media: '(prefers-reduced-motion: reduce)',
        onchange: null,
        addListener: vi.fn(),
        removeListener: vi.fn(),
        addEventListener: vi.fn(),
        removeEventListener: vi.fn(),
        dispatchEvent: vi.fn(),
      })),
    );

    render(
      <MantineProvider>
        <ScanEntryCard
          entry={entry}
          itemId="item-1"
          selected={false}
          onSelectedChange={vi.fn()}
          onChanged={vi.fn()}
        />
      </MantineProvider>,
    );

    const card = screen.getByRole('article', { name: 'Scan 123' });
    expect(card).toHaveClass('scan-entry-card--changed-static');
    expect(card).not.toHaveClass('scan-entry-card--changed-animated');
  });

  it('hides static change indicator after 600ms', () => {
    const fetchMock = vi.fn();
    vi.stubGlobal('fetch', fetchMock);

    vi.stubGlobal(
      'matchMedia',
      vi.fn(() => ({
        matches: true,
        media: '(prefers-reduced-motion: reduce)',
        onchange: null,
        addListener: vi.fn(),
        removeListener: vi.fn(),
        addEventListener: vi.fn(),
        removeEventListener: vi.fn(),
        dispatchEvent: vi.fn(),
      })),
    );

    render(
      <MantineProvider>
        <ScanEntryCard
          entry={entry}
          itemId="item-1"
          selected={false}
          onSelectedChange={vi.fn()}
          onChanged={vi.fn()}
        />
      </MantineProvider>,
    );

    const card = screen.getByRole('article', { name: 'Scan 123' });
    expect(card).toHaveClass('scan-entry-card--changed-static');

    act(() => {
      vi.advanceTimersByTime(600);
    });

    expect(card).not.toHaveClass('scan-entry-card--changed-static');
    expect(card).not.toHaveClass('scan-entry-card--changed-animated');
  });
});

describe('ScanEntryCard batch selection checkbox', () => {
  it('renders the checkbox for pending entries', async () => {
    const fetchMock = vi.fn();
    vi.stubGlobal('fetch', fetchMock);

    render(
      <MantineProvider>
        <ScanEntryCard
          entry={entry}
          itemId="item-1"
          selected={false}
          onSelectedChange={vi.fn()}
          onChanged={vi.fn()}
        />
      </MantineProvider>,
    );

    const checkbox = screen.getByRole('checkbox', { name: 'Select scan for batch approval' });
    expect(checkbox).toBeInTheDocument();
  });

  it('does not render visible label text for the checkbox', async () => {
    const fetchMock = vi.fn();
    vi.stubGlobal('fetch', fetchMock);

    render(
      <MantineProvider>
        <ScanEntryCard
          entry={entry}
          itemId="item-1"
          selected={false}
          onSelectedChange={vi.fn()}
          onChanged={vi.fn()}
        />
      </MantineProvider>,
    );

    screen.getByRole('checkbox', { name: 'Select scan for batch approval' });

    expect(screen.queryByText('Select scan for batch approval')).not.toBeInTheDocument();
    expect(screen.queryByText(/Select scan/)).not.toBeInTheDocument();
  });

  it('sets aria-label describing batch approval purpose', async () => {
    const fetchMock = vi.fn();
    vi.stubGlobal('fetch', fetchMock);

    render(
      <MantineProvider>
        <ScanEntryCard
          entry={entry}
          itemId="item-1"
          selected={false}
          onSelectedChange={vi.fn()}
          onChanged={vi.fn()}
        />
      </MantineProvider>,
    );

    const checkbox = screen.getByRole('checkbox', { name: 'Select scan for batch approval' });
    expect(checkbox).toHaveAttribute('aria-label', 'Select scan for batch approval');
    expect(checkbox.getAttribute('aria-label')).toContain('batch');
  });

  it('toggles checkbox from unchecked to checked and calls onSelectedChange', async () => {
    const fetchMock = vi.fn();
    vi.stubGlobal('fetch', fetchMock);
    const onSelectedChange = vi.fn();

    render(
      <MantineProvider>
        <ScanEntryCard
          entry={entry}
          itemId="item-1"
          selected={false}
          onSelectedChange={onSelectedChange}
          onChanged={vi.fn()}
        />
      </MantineProvider>,
    );

    const checkbox = screen.getByRole('checkbox', { name: 'Select scan for batch approval' });
    expect(checkbox).not.toBeChecked();

    fireEvent.click(checkbox);

    expect(onSelectedChange).toHaveBeenCalledWith(true);
  });

  it('toggles checkbox from checked to unchecked and calls onSelectedChange', async () => {
    const fetchMock = vi.fn();
    vi.stubGlobal('fetch', fetchMock);
    const onSelectedChange = vi.fn();

    render(
      <MantineProvider>
        <ScanEntryCard
          entry={entry}
          itemId="item-1"
          selected={true}
          onSelectedChange={onSelectedChange}
          onChanged={vi.fn()}
        />
      </MantineProvider>,
    );

    const checkbox = screen.getByRole('checkbox', { name: 'Select scan for batch approval' });
    expect(checkbox).toBeChecked();

    fireEvent.click(checkbox);

    expect(onSelectedChange).toHaveBeenCalledWith(false);
  });

  it('does not render checkbox for flagged entries', async () => {
    const fetchMock = vi.fn();
    vi.stubGlobal('fetch', fetchMock);

    render(
      <MantineProvider>
        <ScanEntryCard
          entry={{ ...entry, status: 'flagged' }}
          itemId="item-1"
          selected={false}
          onSelectedChange={vi.fn()}
          onChanged={vi.fn()}
        />
      </MantineProvider>,
    );

    expect(screen.queryByRole('checkbox', { name: 'Select scan for batch approval' })).not.toBeInTheDocument();
  });

  it('reflects the selected prop state in the checkbox', async () => {
    const fetchMock = vi.fn();
    vi.stubGlobal('fetch', fetchMock);

    const { rerender } = render(
      <MantineProvider>
        <ScanEntryCard
          entry={entry}
          itemId="item-1"
          selected={false}
          onSelectedChange={vi.fn()}
          onChanged={vi.fn()}
        />
      </MantineProvider>,
    );

    const checkbox = screen.getByRole('checkbox', { name: 'Select scan for batch approval' });
    expect(checkbox).not.toBeChecked();

    rerender(
      <MantineProvider>
        <ScanEntryCard
          entry={entry}
          itemId="item-1"
          selected={true}
          onSelectedChange={vi.fn()}
          onChanged={vi.fn()}
        />
      </MantineProvider>,
    );

    expect(checkbox).toBeChecked();
  });
});

describe('ScanEntryCard expiration date input', () => {
  const stockIn: ScanEntry = { ...entry, direction: 'stock_in' };

  const renderStockInExpiry = (overrides: Partial<ScanEntry> = {}) => render(
    <MantineProvider>
      <ScanEntryCard
        entry={{ ...stockIn, ...overrides }}
        itemId="item-1"
        selected={false}
        onSelectedChange={vi.fn()}
        onChanged={vi.fn()}
      />
    </MantineProvider>,
  );

  const revealExpiry = () => {
    const add = screen.queryByRole('button', { name: 'Add expiration' });
    if (add) {
      fireEvent.click(add);
      return;
    }
    fireEvent.click(screen.getByRole('button', { name: 'Change expiration' }));
  };

  it('does not send PATCH when value has not changed', async () => {
    const fetchMock = vi.fn();
    vi.stubGlobal('fetch', fetchMock);

    renderStockInExpiry({ expiresAt: '2026-05-15T00:00:00Z' });
    revealExpiry();

    const input = screen.getByLabelText('Expiration date') as HTMLInputElement;
    expect(input).toHaveValue('2026-05-15');
    fireEvent.blur(input);

    const patchCalls = fetchMock.mock.calls.filter(([url]) => String(url).includes('/scans/scan-1'));
    expect(patchCalls.length).toBe(0);
  });

  it('shows error and resets to previously confirmed date on failed PATCH', async () => {
    const fetchMock = vi.fn((input: RequestInfo | URL, request?: RequestInit) => {
      const url = String(input);
      if (url === '/api/scans/scan-1') {
        return Promise.reject(new Error('Network error'));
      }
      throw new Error(`Unexpected ${request?.method ?? 'GET'} request: ${url}`);
    });
    vi.stubGlobal('fetch', fetchMock);

    renderStockInExpiry({ expiresAt: '2026-05-15T00:00:00Z' });
    revealExpiry();

    const input = screen.getByLabelText('Expiration date') as HTMLInputElement;
    expect(input).toHaveValue('2026-05-15');
    fireEvent.change(input, { target: { value: '2026-06-01' } });
    fireEvent.blur(input);

    await waitFor(() => {
      const errorAlerts = screen.getAllByRole('alert');
      const expiryError = errorAlerts.find(alert =>
        alert.textContent?.includes('Unable to update expiration date.')
      );
      expect(expiryError).toBeInTheDocument();
    });

    expect(input).toHaveValue('2026-05-15');
  });

  it('shows error and resets empty to empty on failed PATCH when original expiresAt was null', async () => {
    const fetchMock = vi.fn((input: RequestInfo | URL, request?: RequestInit) => {
      const url = String(input);
      if (url === '/api/scans/scan-1') {
        return Promise.reject(new Error('Network error'));
      }
      throw new Error(`Unexpected ${request?.method ?? 'GET'} request: ${url}`);
    });
    vi.stubGlobal('fetch', fetchMock);

    renderStockInExpiry({ expiresAt: null });
    revealExpiry();

    const input = screen.getByLabelText('Expiration date') as HTMLInputElement;
    expect(input).toHaveValue('');
    fireEvent.change(input, { target: { value: '2026-06-01' } });
    fireEvent.blur(input);

    await waitFor(() => {
      const errorAlerts = screen.getAllByRole('alert');
      const expiryError = errorAlerts.find(alert =>
        alert.textContent?.includes('Unable to update expiration date.')
      );
      expect(expiryError).toBeInTheDocument();
    });

    expect(input).toHaveValue('');
  });

  it('clears error when user starts typing after error', async () => {
    const fetchMock = vi.fn((input: RequestInfo | URL, request?: RequestInit) => {
      const url = String(input);
      if (url === '/api/scans/scan-1') {
        return Promise.reject(new Error('Network error'));
      }
      throw new Error(`Unexpected ${request?.method ?? 'GET'} request: ${url}`);
    });
    vi.stubGlobal('fetch', fetchMock);

    renderStockInExpiry({ expiresAt: '2026-05-15T00:00:00Z' });
    revealExpiry();

    const input = screen.getByLabelText('Expiration date') as HTMLInputElement;
    fireEvent.change(input, { target: { value: '2026-06-01' } });
    fireEvent.blur(input);

    await waitFor(() => {
      const errorAlerts = screen.getAllByRole('alert');
      const expiryError = errorAlerts.find(alert =>
        alert.textContent?.includes('Unable to update expiration date.')
      );
      expect(expiryError).toBeInTheDocument();
    });

    fireEvent.change(input, { target: { value: '2026-06-02' } });

    await waitFor(() => {
      expect(screen.queryByText('Unable to update expiration date.')).not.toBeInTheDocument();
    });
  });

  it('does not show date input when entry.status is not pending', async () => {
    const fetchMock = vi.fn();
    vi.stubGlobal('fetch', fetchMock);

    render(
      <MantineProvider>
        <ScanEntryCard
          entry={{ ...entry, status: 'flagged' }}
          itemId="item-1"
          selected={false}
          onSelectedChange={vi.fn()}
          onChanged={vi.fn()}
        />
      </MantineProvider>,
    );

    expect(screen.queryByLabelText('Expiration date')).not.toBeInTheDocument();
  });

  it('shows fallback error message when error is not an Error instance', async () => {
    const fetchMock = vi.fn((input: RequestInfo | URL, request?: RequestInit) => {
      const url = String(input);
      if (url === '/api/scans/scan-1') {
        return Promise.reject({ statusCode: 500 });
      }
      throw new Error(`Unexpected ${request?.method ?? 'GET'} request: ${url}`);
    });
    vi.stubGlobal('fetch', fetchMock);

    renderStockInExpiry({ expiresAt: '2026-05-15T00:00:00Z' });
    revealExpiry();

    const input = screen.getByLabelText('Expiration date') as HTMLInputElement;
    fireEvent.change(input, { target: { value: '2026-06-01' } });
    fireEvent.blur(input);

    await waitFor(() => {
      const errorAlerts = screen.getAllByRole('alert');
      const expiryError = errorAlerts.find(alert =>
        alert.textContent?.includes('Unable to update expiration date.')
      );
      expect(expiryError).toBeInTheDocument();
    });
  });
});

describe('ScanEntryCard stock-in confirmation', () => {
  const stockIn: ScanEntry = { ...entry, direction: 'stock_in' };

  const renderStockIn = (overrides: Partial<ScanEntry> = {}) => render(
    <MantineProvider>
      <ScanEntryCard
        entry={{ ...stockIn, ...overrides }}
        itemId="item-1"
        selected={false}
        onSelectedChange={vi.fn()}
        onChanged={vi.fn()}
      />
    </MantineProvider>,
  );

  it('shows a thumb stepper and keeps the expiration field collapsed', () => {
    vi.stubGlobal('fetch', vi.fn());
    renderStockIn();

    const row = screen.getByRole('article', { name: 'Scan 123' });
    expect(row).toHaveClass('scan-entry-row');
    expect(row).not.toHaveClass('mantine-Card-root');
    expect(screen.getByRole('heading', { name: 'Milk' })).toBeInTheDocument();
    const barcode = screen.getByText('Barcode: 123');
    expect(barcode).toHaveClass('copyable-barcode');
    expect(barcode.closest('button')).toBeNull();
    expect(screen.getByRole('button', { name: 'Decrease unit count' })).toBeDisabled();
    expect(screen.getByRole('button', { name: 'Increase unit count' })).toBeEnabled();
    expect(screen.getByLabelText('Unit count')).toHaveValue('1');
    expect(screen.queryByRole('button', { name: 'More' })).not.toBeInTheDocument();
    const addExpiration = screen.getByRole('button', { name: 'Add expiration' });
    expect(addExpiration).toHaveClass('scan-entry-add-expiry');
    expect(screen.queryByLabelText('Expiration date')).not.toBeInTheDocument();
  });

  it('patches the same unit count when the stepper or the field changes it', async () => {
    const bodies: unknown[] = [];
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL, request?: RequestInit) => {
      const url = String(input);
      if (url === '/api/scans/scan-1') {
        bodies.push(JSON.parse(request?.body as string));
        const unitCount = (bodies[bodies.length - 1] as { unitCount: number }).unitCount;
        return Promise.resolve(jsonResponse({ ...stockIn, unitCount }));
      }
      throw new Error(`Unexpected ${request?.method ?? 'GET'} request: ${url}`);
    }));

    renderStockIn();
    fireEvent.click(screen.getByRole('button', { name: 'Increase unit count' }));
    expect(screen.getByLabelText('Unit count')).toHaveValue('2');
    fireEvent.click(screen.getByRole('button', { name: 'Increase unit count' }));
    expect(screen.getByLabelText('Unit count')).toHaveValue('3');
    expect(bodies).toEqual([]);

    await waitFor(() => {
      expect(bodies).toEqual([{ unitCount: 3 }]);
    });

    const input = screen.getByLabelText('Unit count');
    fireEvent.change(input, { target: { value: '5' } });
    fireEvent.blur(input);

    await waitFor(() => {
      expect(bodies).toContainEqual({ unitCount: 5 });
    });
  });

  it('steps the count down without going below one', async () => {
    const bodies: unknown[] = [];
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL, request?: RequestInit) => {
      const url = String(input);
      if (url === '/api/scans/scan-1') {
        bodies.push(JSON.parse(request?.body as string));
        return Promise.resolve(jsonResponse({ ...stockIn, unitCount: 2 }));
      }
      throw new Error(`Unexpected ${request?.method ?? 'GET'} request: ${url}`);
    }));

    renderStockIn({ unitCount: 3 });
    fireEvent.click(screen.getByRole('button', { name: 'Decrease unit count' }));

    await waitFor(() => {
      expect(bodies).toEqual([{ unitCount: 2 }]);
    });

    expect(screen.getByRole('button', { name: 'Decrease unit count' })).toBeEnabled();
  });

  it('reveals the expiration field from Add expiration and saves it', async () => {
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL, request?: RequestInit) => {
      const url = String(input);
      if (url === '/api/scans/scan-1') {
        expect(request?.method).toBe('PATCH');
        expect(JSON.parse(request?.body as string)).toEqual({ expiresAt: '2026-06-01T00:00:00.000Z' });
        return Promise.resolve(jsonResponse({ ...stockIn, expiresAt: '2026-06-01T00:00:00.000Z' }));
      }
      throw new Error(`Unexpected ${request?.method ?? 'GET'} request: ${url}`);
    }));

    renderStockIn();
    fireEvent.click(screen.getByRole('button', { name: 'Add expiration' }));
    const input = screen.getByLabelText('Expiration date');
    expect(input).toHaveValue('');
    fireEvent.change(input, { target: { value: '2026-06-01' } });
    fireEvent.blur(input);

    await waitFor(() => {
      expect(screen.getByText(`Expires ${formatExpiryDate('2026-06-01T00:00:00.000Z')}`)).toBeInTheDocument();
    });
    expect(screen.queryByLabelText('Expiration date')).not.toBeInTheDocument();
  });

  it('shows an existing expiration compactly and can change or clear it', async () => {
    const bodies: unknown[] = [];
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL, request?: RequestInit) => {
      const url = String(input);
      if (url === '/api/scans/scan-1') {
        bodies.push(JSON.parse(request?.body as string));
        return Promise.resolve(jsonResponse({ ...stockIn, expiresAt: null }));
      }
      throw new Error(`Unexpected ${request?.method ?? 'GET'} request: ${url}`);
    }));

    renderStockIn({ expiresAt: '2026-05-15T00:00:00Z' });
    expect(screen.getByText(`Expires ${formatExpiryDate('2026-05-15T00:00:00.000Z')}`)).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Add expiration' })).not.toBeInTheDocument();
    expect(screen.queryByLabelText('Expiration date')).not.toBeInTheDocument();

    fireEvent.click(screen.getByRole('button', { name: 'Change expiration' }));
    const input = screen.getByLabelText('Expiration date') as HTMLInputElement;
    expect(input).toHaveValue('2026-05-15');
    fireEvent.change(input, { target: { value: '2026-07-04' } });
    fireEvent.blur(input);

    await waitFor(() => {
      expect(screen.getByText(`Expires ${formatExpiryDate('2026-07-04T00:00:00.000Z')}`)).toBeInTheDocument();
    });
    expect(bodies[0]).toEqual({ expiresAt: '2026-07-04T00:00:00.000Z' });

    fireEvent.click(screen.getByRole('button', { name: 'Clear expiration' }));
    await waitFor(() => {
      expect(screen.getByRole('button', { name: 'Add expiration' })).toBeInTheDocument();
    });
    expect(bodies[1]).toEqual({ expiresAt: '0001-01-01T00:00:00.000Z' });
    expect(screen.queryByLabelText('Expiration date')).not.toBeInTheDocument();
  });

  it('cancels an opened expiration field without saving', () => {
    const fetchMock = vi.fn();
    vi.stubGlobal('fetch', fetchMock);
    renderStockIn();

    fireEvent.click(screen.getByRole('button', { name: 'Add expiration' }));
    expect(screen.getByLabelText('Expiration date')).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: 'Cancel expiration' }));

    expect(screen.queryByLabelText('Expiration date')).not.toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Add expiration' })).toBeInTheDocument();
    expect(fetchMock).not.toHaveBeenCalled();
  });
});

describe('ScanEntryCard provenance badge', () => {
  it('displays provenance badge when product has externalSource', async () => {
    const fetchMock = vi.fn();
    vi.stubGlobal('fetch', fetchMock);

    render(
      <MantineProvider>
        <ScanEntryCard
          entry={{ ...entry, product: { id: 'product-1', name: 'Milk', category: 'Dairy', unitOfMeasure: 'carton', externalSource: 'openfoodfacts' } }}
          itemId="item-1"
          selected={false}
          onSelectedChange={vi.fn()}
          onChanged={vi.fn()}
        />
      </MantineProvider>,
    );

    const badge = screen.getByLabelText('Product data from Open Food Facts');
    expect(badge).toBeInTheDocument();
    expect(badge).toHaveTextContent('Open Food Facts');
  });

  it('does not display badge when product has no externalSource', async () => {
    const fetchMock = vi.fn();
    vi.stubGlobal('fetch', fetchMock);

    render(
      <MantineProvider>
        <ScanEntryCard
          entry={entry}
          itemId="item-1"
          selected={false}
          onSelectedChange={vi.fn()}
          onChanged={vi.fn()}
        />
      </MantineProvider>,
    );

    const badge = screen.queryByLabelText(/Product data from/);
    expect(badge).not.toBeInTheDocument();
  });

  it('does not display badge when product is null', async () => {
    const fetchMock = vi.fn();
    vi.stubGlobal('fetch', fetchMock);

    render(
      <MantineProvider>
        <ScanEntryCard
          entry={{ ...entry, product: null }}
          itemId="item-1"
          selected={false}
          onSelectedChange={vi.fn()}
          onChanged={vi.fn()}
        />
      </MantineProvider>,
    );

    const badge = screen.queryByLabelText(/Product data from/);
    expect(badge).not.toBeInTheDocument();
  });
});

describe('ScanEntryCard group hint', () => {
  const hinted: ScanEntry = {
    ...entry,
    direction: 'stock_in',
    groupHint: { groupId: 'beans', name: 'Cut green beans' },
  };

  it('hides the group buttons when the scan has no hint', () => {
    vi.stubGlobal('fetch', vi.fn());
    render(
      <MantineProvider>
        <ScanEntryCard
          entry={entry}
          itemId="item-1"
          selected={false}
          onSelectedChange={vi.fn()}
          onChanged={vi.fn()}
        />
      </MantineProvider>,
    );

    expect(screen.queryByRole('button', { name: 'Add to group' })).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Not now' })).not.toBeInTheDocument();
    expect(screen.queryByText(/Looks like/)).not.toBeInTheDocument();
  });

  it('adds the product to the group from the hint button', async () => {
    const fetchMock = vi.fn((input: RequestInfo | URL, request?: RequestInit) => {
      const url = String(input);
      if (url === '/api/groups/beans/members') {
        expect(request?.method).toBe('POST');
        expect(JSON.parse(request?.body as string)).toEqual({ productIds: ['product-1'] });
        return Promise.resolve(jsonResponse({ id: 'beans', name: 'Cut green beans', members: [{ productId: 'product-1' }] }));
      }
      throw new Error(`Unexpected ${request?.method ?? 'GET'} request: ${url}`);
    });
    vi.stubGlobal('fetch', fetchMock);
    const onChanged = vi.fn();
    render(
      <MantineProvider>
        <ScanEntryCard
          entry={hinted}
          selected={false}
          onSelectedChange={vi.fn()}
          onChanged={onChanged}
        />
      </MantineProvider>,
    );

    expect(screen.getByRole('checkbox', { name: 'Select scan for batch approval' })).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: 'Add to group' }));
    await waitFor(() => expect(onChanged).toHaveBeenCalled());
    expect(fetchMock).toHaveBeenCalledWith('/api/groups/beans/members', expect.any(Object));
  });

  it('records one from-scan suggestion when Not now is tapped', async () => {
    const fetchMock = vi.fn((input: RequestInfo | URL, request?: RequestInit) => {
      const url = String(input);
      if (url === '/api/group-suggestions/from-scan') {
        expect(request?.method).toBe('POST');
        expect(JSON.parse(request?.body as string)).toEqual({ productId: 'product-1' });
        return Promise.resolve(new Response(null, { status: 204 }));
      }
      throw new Error(`Unexpected ${request?.method ?? 'GET'} request: ${url}`);
    });
    vi.stubGlobal('fetch', fetchMock);
    const onChanged = vi.fn();
    render(
      <MantineProvider>
        <ScanEntryCard
          entry={hinted}
          selected={false}
          onSelectedChange={vi.fn()}
          onChanged={onChanged}
        />
      </MantineProvider>,
    );

    fireEvent.click(screen.getByRole('button', { name: 'Not now' }));
    await waitFor(() => expect(onChanged).toHaveBeenCalled());
    const posts = fetchMock.mock.calls.filter(([url]) => String(url) === '/api/group-suggestions/from-scan');
    expect(posts).toHaveLength(1);
  });

  it('opens What is it as a sheet instead of the resolver on the row', async () => {
    const fetchMock = vi.fn((input: RequestInfo | URL) => {
      const url = String(input);
      if (url === '/api/products') return Promise.resolve(jsonResponse([]));
      throw new Error(`Unexpected request: ${url}`);
    });
    vi.stubGlobal('fetch', fetchMock);
    render(
      <MantineProvider>
        <ScanEntryCard
          entry={{ ...entry, status: 'flagged', direction: null, product: null, productId: null }}
          selected={false}
          onSelectedChange={vi.fn()}
          onChanged={vi.fn()}
        />
      </MantineProvider>,
    );

    expect(screen.getByRole('button', { name: 'What is it?' })).toBeInTheDocument();
    expect(screen.queryByLabelText('Search products')).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: 'What is it?' }));
    expect(await screen.findByLabelText('Search products')).toBeInTheDocument();
  });
});

const noItemBody = {
  error: 'barcode is not linked',
  code: 'no_item',
  suggestions: [
    { itemId: 'item-pears', productId: 'prod-pears', name: 'Sliced Pears', onHand: 3 },
    { itemId: 'item-slices', productId: 'prod-slices', name: 'Pear Slices', onHand: 1 },
  ],
};

const errorResponse = (body: unknown, status: number) =>
  new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } });

describe('ScanEntryCard unmatched stock-out', () => {
  const renderStockOut = (onChanged = vi.fn()) => {
    render(
      <MantineProvider>
        <ScanEntryCard
          entry={{ ...entry, product: { id: 'product-1', name: 'Lite Pear Slices In Pear Juice', category: 'Fruit', unitOfMeasure: 'can' } }}
          selected={false}
          onSelectedChange={vi.fn()}
          onChanged={onChanged}
        />
      </MantineProvider>,
    );
  };

  it('shows pantry suggestions in rank order and does not commit until one is picked', async () => {
    const fetchMock = vi.fn((input: RequestInfo | URL, request?: RequestInit) => {
      const url = String(input);
      if (url === '/api/products') return Promise.resolve(jsonResponse([]));
      if (url === '/api/scans/scan-1/commit' && request?.method === 'POST') {
        const body = JSON.parse(String(request.body)) as { linkProductId?: string };
        if (body.linkProductId === 'prod-pears') {
          return Promise.resolve(jsonResponse({ ...entry, status: 'committed', productId: 'prod-pears' }));
        }
        return Promise.resolve(errorResponse(noItemBody, 409));
      }
      throw new Error(`Unexpected ${request?.method ?? 'GET'} request: ${url}`);
    });
    vi.stubGlobal('fetch', fetchMock);
    const onChanged = vi.fn();
    renderStockOut(onChanged);

    fireEvent.click(screen.getByRole('button', { name: 'Confirm' }));

    const offer = await screen.findByRole('region', { name: 'Which item is this?' });
    expect(offer).toHaveTextContent('Which item is this?');
    expect(within(offer).getByLabelText('Search products')).toBeInTheDocument();
    expect(within(offer).queryByText('barcode is not linked', { exact: false })).not.toBeInTheDocument();
    expect(within(offer).queryByText('Not in your pantry', { exact: false })).not.toBeInTheDocument();
    const rows = within(offer).getAllByRole('listitem');
    expect(rows.map((row) => row.textContent)).toEqual([
      'Sliced Pears3 in stockLink',
      'Pear Slices1 in stockLink',
    ]);
    expect(within(rows[0]).getByText('3 in stock')).toHaveClass('stock-note--full');
    expect(onChanged).not.toHaveBeenCalled();

    fireEvent.click(within(offer).getByRole('button', { name: 'Link Sliced Pears' }));
    await waitFor(() => expect(onChanged).toHaveBeenCalled());
    const linkCall = fetchMock.mock.calls.find(([, request]) => {
      const body = request?.body ? JSON.parse(String(request.body)) as { linkProductId?: string } : {};
      return body.linkProductId === 'prod-pears';
    });
    expect(linkCall).toBeDefined();
  });

  it('dismisses the suggestions without linking', async () => {
    const fetchMock = vi.fn((input: RequestInfo | URL, request?: RequestInit) => {
      const url = String(input);
      if (url === '/api/products') return Promise.resolve(jsonResponse([]));
      if (url === '/api/scans/scan-1/commit') return Promise.resolve(errorResponse(noItemBody, 409));
      throw new Error(`Unexpected ${request?.method ?? 'GET'} request: ${url}`);
    });
    vi.stubGlobal('fetch', fetchMock);
    const onChanged = vi.fn();
    renderStockOut(onChanged);

    fireEvent.click(screen.getByRole('button', { name: 'Confirm' }));
    const offer = await screen.findByRole('region', { name: 'Which item is this?' });
    fireEvent.click(within(offer).getByRole('button', { name: 'Dismiss' }));

    await waitFor(() => expect(screen.queryByRole('region', { name: 'Which item is this?' })).not.toBeInTheDocument());
    expect(onChanged).not.toHaveBeenCalled();
    expect(fetchMock.mock.calls.filter(([url]) => String(url).endsWith('/commit'))).toHaveLength(1);
    expect(screen.getByRole('article', { name: 'Scan 123' })).toBeInTheDocument();
  });
});

const householdRow = (id: string, name: string) => ({
  item: {
    id: `item-${id}`,
    userId: 'default-user',
    productId: `prod-${id}`,
    product: { id: `prod-${id}`, name, category: 'Grocery', unitOfMeasure: 'box' },
    targetQuantity: null,
    createdAt: '2026-03-20T10:00:00Z',
  },
  instanceCount: 1,
  nearExpiryCount: 0,
  expiredCount: 0,
  needsAttention: false,
});

const staleSuggestion = { itemId: 'item-stale', productId: 'prod-stale', name: 'Stale Oats', onHand: 1 };
const keptSuggestion = { itemId: 'item-kept', productId: 'prod-kept', name: 'Kept Oats', onHand: 2 };

describe('ScanEntryCard gone link target', () => {
  it.each([
    {
      name: 'drops the gone suggestion and reloads the household list',
      suggestions: [staleSuggestion, keptSuggestion],
      remaining: 'Kept Oats',
    },
    {
      name: 'drops the only suggestion and says no close match was found',
      suggestions: [staleSuggestion],
      remaining: '',
    },
  ])('$name', async ({ suggestions, remaining }) => {
    const inventoryGets: { url: string; method: string }[] = [];
    const commitBodies: { linkProductId?: string }[] = [];
    const fetchMock = vi.fn((input: RequestInfo | URL, request?: RequestInit) => {
      const url = String(input);
      if (url === '/api/inventory/item-1/instances') return Promise.resolve(jsonResponse(instances));
      if (url === '/api/inventory') {
        inventoryGets.push({ url, method: request?.method ?? 'GET' });
        return Promise.resolve(jsonResponse([
          householdRow('stale', 'Stale Oats'),
          householdRow('kept', 'Kept Oats'),
        ]));
      }
      if (url === '/api/scans/scan-1/commit' && request?.method === 'POST') {
        const body = JSON.parse(String(request.body)) as { linkProductId?: string };
        commitBodies.push(body);
        if (body.linkProductId === 'prod-stale') {
          return Promise.resolve(errorResponse({
            error: "That item isn't available anymore. Pick another.",
            code: 'link_target_gone',
          }, 409));
        }
        return Promise.resolve(errorResponse({
          error: 'barcode is not linked',
          code: 'no_item',
          suggestions,
        }, 409));
      }
      throw new Error(`Unexpected ${request?.method ?? 'GET'} request: ${url}`);
    });
    vi.stubGlobal('fetch', fetchMock);
    render(
      <MantineProvider>
        <ScanEntryCard
          entry={entry}
          itemId="item-1"
          selected={false}
          onSelectedChange={vi.fn()}
          onChanged={vi.fn()}
        />
      </MantineProvider>,
    );

    fireEvent.click(screen.getByRole('button', { name: 'More', expanded: false }));
    fireEvent.click(screen.getByRole('button', { name: 'Confirm' }));
    fireEvent.click(await screen.findByRole('button', { name: 'Link Stale Oats' }));

    await waitFor(() => {
      expect(inventoryGets).toEqual([
        { url: '/api/inventory', method: 'GET' },
        { url: '/api/inventory', method: 'GET' },
      ]);
    });
    expect(commitBodies).toEqual([{}, { linkProductId: 'prod-stale' }]);
    expect(screen.queryByRole('button', { name: 'Link Stale Oats' })).not.toBeInTheDocument();
    if (remaining === '') {
      expect(screen.getByText('No close match found.')).toBeInTheDocument();
    } else {
      expect(screen.getByRole('button', { name: `Link ${remaining}` })).toBeInTheDocument();
    }
    fireEvent.focus(screen.getByLabelText('Search products'));
    expect(screen.queryByRole('option', { name: 'Stale Oats', hidden: true })).not.toBeInTheDocument();
    if (remaining !== '') {
      expect(await screen.findByRole('option', { name: remaining, hidden: true })).toBeInTheDocument();
    }
    fireEvent.click(screen.getByRole('button', { name: 'Dismiss' }));
    expect(screen.getByRole('button', { name: 'More', expanded: false })).toBeInTheDocument();
  });
});
