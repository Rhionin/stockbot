import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { MantineProvider } from '@mantine/core';
import { describe, expect, it, vi } from 'vitest';
import type { ScanEntry } from '../../types';
import { QrScanCard } from './QrScanCard';

const entry = (barcode: string): ScanEntry => ({
  id: 'qr-1',
  userId: 'user-1',
  barcode,
  scannedAt: '2026-10-14T12:00:00Z',
  direction: 'stock_in',
  unitCount: 1,
  expiresAt: null,
  status: 'qr',
  productId: null,
  committedAt: null,
  createdAt: '2026-10-14T12:00:00Z',
});

const renderCard = (barcode: string, onRescan = vi.fn(), onChanged = vi.fn()) =>
  render(
    <MantineProvider>
      <QrScanCard entry={entry(barcode)} onRescan={onRescan} onChanged={onChanged} />
    </MantineProvider>,
  );

describe('QrScanCard', () => {
  it('shows the domain of a URL and offers rescan or dismiss', async () => {
    const onRescan = vi.fn();
    const onChanged = vi.fn();
    const fetchMock = vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      expect(String(input)).toBe('/api/scans/qr-1');
      expect(init?.method).toBe('PATCH');
      expect(JSON.parse(String(init?.body))).toEqual({ status: 'cancelled' });
      return Promise.resolve(new Response(JSON.stringify({ ...entry('https://example.com/groceries/milk'), status: 'cancelled' }), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      }));
    });
    vi.stubGlobal('fetch', fetchMock);

    renderCard('https://example.com/groceries/milk', onRescan, onChanged);

    expect(screen.getByText('This was a QR code, not the barcode')).toBeInTheDocument();
    expect(screen.getByText('example.com')).toBeInTheDocument();
    expect(screen.queryByText('https://example.com/groceries/milk')).not.toBeInTheDocument();

    fireEvent.click(screen.getByRole('button', { name: 'Rescan the barcode' }));
    expect(onRescan).toHaveBeenCalledOnce();

    fireEvent.click(screen.getByRole('button', { name: 'Dismiss' }));
    await waitFor(() => expect(onChanged).toHaveBeenCalledOnce());
  });

  it('shows short text in full', () => {
    renderCard('buy milk');
    expect(screen.getByText('buy milk')).toBeInTheDocument();
  });
});
