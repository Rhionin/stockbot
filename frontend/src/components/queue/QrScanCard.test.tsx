import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { MantineProvider } from '@mantine/core';
import { describe, expect, it, vi } from 'vitest';
import type { ScanEntry } from '@stockbot/shared/types';
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
    expect(screen.getByRole('article')).toHaveAccessibleName('This was a QR code, not the barcode. example.com');
    expect(screen.queryByText('https://example.com/groceries/milk')).not.toBeInTheDocument();

    fireEvent.click(screen.getByRole('button', { name: 'Rescan the barcode' }));
    expect(onRescan).toHaveBeenCalledOnce();

    fireEvent.click(screen.getByRole('button', { name: 'Dismiss' }));
    await waitFor(() => expect(onChanged).toHaveBeenCalledOnce());
  });

  it.each([
    {
      name: 'Wi-Fi',
      barcode: 'WIFI:T:WPA;S:SyntheticNet;P:synthetic-pass-123;;',
      preview: 'Wi-Fi code',
      leaked: ['synthetic-pass-123', 'SyntheticNet', 'WIFI:'],
    },
    {
      name: 'vCard',
      barcode: 'BEGIN:VCARD\nFN:Synthetic Person\nTEL:555-0199\nEND:VCARD',
      preview: 'Contact card',
      leaked: ['Synthetic Person', '555-0199', 'BEGIN:VCARD'],
    },
    {
      name: 'authenticator',
      barcode: 'otpauth://totp/Synthetic:user?secret=SYNTHETICSECRET123&issuer=SyntheticApp',
      preview: 'Authenticator code',
      leaked: ['SYNTHETICSECRET123', 'SyntheticApp', 'otpauth:'],
    },
    {
      name: 'plain text',
      barcode: 'buy milk',
      preview: 'QR code',
      leaked: ['buy milk'],
    },
  ])('hides a $name payload in the row and the accessible name', ({ barcode, preview, leaked }) => {
    renderCard(barcode);
    const article = screen.getByRole('article');
    expect(article).toHaveTextContent(preview);
    expect(article).toHaveAccessibleName(`This was a QR code, not the barcode. ${preview}`);
    const rendered = `${article.textContent ?? ''}\n${article.getAttribute('aria-label') ?? ''}`;
    expect(rendered).not.toContain(barcode);
    for (const secret of leaked) {
      expect(rendered).not.toContain(secret);
    }
  });
});
