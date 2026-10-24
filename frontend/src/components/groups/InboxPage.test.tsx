import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { MantineProvider } from '@mantine/core';
import { MemoryRouter } from 'react-router-dom';
import { describe, expect, it, vi } from 'vitest';
import type { GroupSuggestion } from '@stockbot/shared/types';
import { InboxPage } from './InboxPage';

const json = (body: unknown, status = 200) =>
  new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } });

const card: GroupSuggestion = {
  id: 'sug',
  kind: 'looks_alike',
  title: 'Peanut butter',
  status: 'open',
  members: [
    { productId: 'plain', name: 'Great Value Peanut Butter', included: true, caution: '' },
    { productId: 'crunchy', name: 'Great Value Crunchy Peanut Butter', included: false, caution: 'Crunchy. Probably not the same.' },
  ],
};

const renderInbox = (handler: (url: string, init?: RequestInit) => Response | Promise<Response>) => {
  vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL, init?: RequestInit) => Promise.resolve(handler(String(input), init))));
  return render(
    <MantineProvider>
      <MemoryRouter>
        <InboxPage />
      </MemoryRouter>
    </MantineProvider>,
  );
};

describe('InboxPage', () => {
  it('leaves the caution row unchecked, groups the checked products, and dismisses the card', async () => {
    const calls: { url: string; body?: string }[] = [];
    let open = [card];
    renderInbox((url, init) => {
      calls.push({ url, body: init?.body as string | undefined });
      if (url === '/api/group-suggestions' && (init?.method ?? 'GET') === 'GET') return json(open);
      if (url.endsWith('/accept')) {
        open = [];
        return json({ id: 'g', name: 'Peanut butter', rule: 'same_as_ran_out', ruleConfirmed: false, members: [], runningLow: false });
      }
      if (url.endsWith('/dismiss')) {
        open = [];
        return new Response(null, { status: 204 });
      }
      throw new Error(`Unexpected ${init?.method} ${url}`);
    });

    expect(await screen.findByLabelText('Great Value Crunchy Peanut Butter')).not.toBeChecked();
    expect(screen.getByText('Crunchy. Probably not the same.')).toBeInTheDocument();
    expect(screen.getByLabelText('Great Value Peanut Butter')).toBeChecked();

    fireEvent.click(screen.getByRole('button', { name: 'Group these' }));
    await waitFor(() => expect(calls.some((call) => call.url.endsWith('/accept'))).toBe(true));
    const accept = calls.find((call) => call.url.endsWith('/accept'));
    expect(JSON.parse(accept?.body ?? '{}')).toEqual({ productIds: ['plain'] });
    expect(await screen.findByText('Nothing to review.')).toBeInTheDocument();
  });

  it('dismisses the card and waits when members already have a target', async () => {
    const calls: string[] = [];
    renderInbox((url, init) => {
      calls.push(`${init?.method ?? 'GET'} ${url}`);
      if (url === '/api/group-suggestions') return json([card]);
      if (url.endsWith('/accept')) {
        return json({
          error: 'Choose what this group should keep on hand. These products already have their own supply setting.',
          code: 'target_decision_required',
          members: [{ productId: 'plain', name: 'Great Value Peanut Butter', quantity: 4 }],
        }, 409);
      }
      if (url.endsWith('/dismiss')) return new Response(null, { status: 204 });
      throw new Error(`Unexpected ${url}`);
    });

    fireEvent.click(await screen.findByRole('button', { name: 'Group these' }));
    expect(await screen.findByText('Great Value Peanut Butter: 4 on hand')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Use the household default' })).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Use the account window' })).not.toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Group these' })).toBeInTheDocument();

    fireEvent.click(screen.getByRole('button', { name: 'Not the same' }));
    await waitFor(() => expect(calls.some((call) => call.startsWith('POST') && call.endsWith('/dismiss'))).toBe(true));
  });

  it('shows the picture, brand, size, and barcode, and sends an edited group name', async () => {
    const calls: { url: string; body?: string }[] = [];
    const relish: GroupSuggestion = {
      id: 'relish',
      kind: 'looks_alike',
      title: 'Sweet relish',
      status: 'open',
      members: [
        {
          productId: 'olive',
          name: 'Mt. Olive Sweet Relish, 10 FL OZ',
          included: true,
          caution: '',
          imageUrl: 'https://images.openfoodfacts.org/images/relish.jpg',
          brand: 'Mt. Olive',
          category: 'Condiments',
          unitOfMeasure: 'jar',
          netAmount: 10,
          netUnit: 'fl oz',
          barcodes: ['0009300000444'],
        },
        {
          productId: 'vlasic',
          name: 'Vlasic Sweet Relish, 10 FL OZ',
          included: true,
          caution: '',
          brand: 'Vlasic',
          barcodes: ['0054100018700'],
        },
        {
          productId: 'crunchy',
          name: 'Great Value Crunchy Peanut Butter',
          included: false,
          caution: 'Crunchy. Probably not the same.',
          brand: 'Great Value',
          variety: 'Crunchy',
          netAmount: 16,
          netUnit: 'oz',
          packCount: 2,
        },
      ],
    };
    renderInbox((url, init) => {
      calls.push({ url, body: init?.body as string | undefined });
      if (url === '/api/group-suggestions') return json([relish]);
      if (url.endsWith('/accept')) {
        return json({ id: 'g', name: 'Sandwich relish', rule: 'same_as_ran_out', ruleConfirmed: false, members: [], runningLow: false });
      }
      throw new Error(`Unexpected ${url}`);
    });

    const photo = await screen.findByText('Mt. Olive Sweet Relish, 10 FL OZ');
    expect(photo.closest('article')?.querySelector('img')).toHaveAttribute('src', 'https://images.openfoodfacts.org/images/relish.jpg');
    expect(screen.getAllByText('No photo')).toHaveLength(2);
    expect(screen.getByText('Mt. Olive')).toBeInTheDocument();
    expect(screen.getByText('Vlasic')).toBeInTheDocument();
    expect(screen.getByText('10 fl oz')).toBeInTheDocument();
    expect(screen.getByText('2 x 16 oz')).toBeInTheDocument();
    expect(screen.getAllByText('Not listed').length).toBeGreaterThan(0);
    expect(screen.getByText('0009300000444')).toBeInTheDocument();
    expect(screen.getByText('Crunchy')).toBeInTheDocument();
    expect(screen.getByText('jar')).toBeInTheDocument();
    expect(screen.getByLabelText('Group name')).toHaveValue('Sweet relish');
    expect(screen.queryByRole('button', { name: 'Edit' })).not.toBeInTheDocument();

    fireEvent.change(screen.getByLabelText('Group name'), { target: { value: 'Sandwich relish' } });
    fireEvent.click(screen.getByRole('button', { name: 'Group these' }));
    await waitFor(() => expect(calls.some((call) => call.url.endsWith('/accept'))).toBe(true));
    const accept = calls.find((call) => call.url.endsWith('/accept'));
    expect(JSON.parse(accept?.body ?? '{}')).toEqual({
      productIds: ['olive', 'vlasic'],
      name: 'Sandwich relish',
    });
  });

  it('looks for more groups and says when none are new', async () => {
    const calls: string[] = [];
    renderInbox((url, init) => {
      calls.push(`${init?.method ?? 'GET'} ${url}`);
      if (url === '/api/group-suggestions' && (init?.method ?? 'GET') === 'GET') return json([]);
      if (url === '/api/group-suggestions/rescan') return json([]);
      throw new Error(`Unexpected ${init?.method} ${url}`);
    });

    fireEvent.click(await screen.findByRole('button', { name: 'Look for more groups' }));
    expect(await screen.findByText('No new groups found')).toBeInTheDocument();
    expect(screen.getByText('Nothing to review.')).toBeInTheDocument();
    expect(calls).toContain('POST /api/group-suggestions/rescan');
  });

  it('shows a group the rescan just found', async () => {
    let open: GroupSuggestion[] = [];
    renderInbox((url, init) => {
      if (url === '/api/group-suggestions' && (init?.method ?? 'GET') === 'GET') return json(open);
      if (url === '/api/group-suggestions/rescan') {
        open = [card];
        return json([card]);
      }
      throw new Error(`Unexpected ${init?.method} ${url}`);
    });

    expect(await screen.findByText('Nothing to review.')).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: 'Look for more groups' }));
    expect(await screen.findByLabelText('Great Value Peanut Butter')).toBeChecked();
    expect(screen.queryByText('No new groups found')).not.toBeInTheDocument();
  });
});
