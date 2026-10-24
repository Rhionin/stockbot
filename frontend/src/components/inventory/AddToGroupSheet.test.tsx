import type { ComponentProps } from 'react';
import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { MantineProvider } from '@mantine/core';
import { MemoryRouter, Route, Routes, useLocation } from 'react-router-dom';
import { describe, expect, it, vi } from 'vitest';
import type { GroupSuggestion, ProductGroup } from '@stockbot/shared/types';
import { AddToGroupSheet } from './AddToGroupSheet';

const json = (body: unknown, status = 200) =>
  new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } });

const group = (extra: Partial<ProductGroup> = {}): ProductGroup => ({
  id: 'g1',
  name: 'Peanut butter',
  rule: 'same_as_ran_out',
  ruleConfirmed: false,
  members: [],
  runningLow: false,
  ...extra,
});

const suggestion = (extra: Partial<GroupSuggestion> = {}): GroupSuggestion => ({
  id: 'sug',
  kind: 'from_old_plan',
  title: 'Spreads',
  status: 'open',
  existingGroupId: 'g1',
  members: [{ productId: 'oat', name: 'Oat milk', included: true, caution: '' }],
  ...extra,
});

const Pathname = () => {
  const { pathname } = useLocation();
  return <p>Path {pathname}</p>;
};

const renderSheet = (props: Partial<ComponentProps<typeof AddToGroupSheet>> = {}) => {
  const onChanged = vi.fn();
  render(
    <MantineProvider>
      <MemoryRouter>
        <Pathname />
        <Routes>
          <Route path="/groups/:id" element={<h1>Group</h1>} />
        </Routes>
        <AddToGroupSheet
          opened
          productId="oat"
          productName="Oat milk"
          groups={[group(), group({ id: 'soup', name: 'Soup' })]}
          suggestions={[suggestion()]}
          onClose={() => {}}
          onChanged={onChanged}
          {...props}
        />
      </MemoryRouter>
    </MantineProvider>,
  );
  return onChanged;
};

describe('AddToGroupSheet', () => {
  it('adds to a suggested group and opens a new group', async () => {
    const posts: { url: string; body: unknown }[] = [];
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      posts.push({ url, body: JSON.parse(String(init?.body)) });
      if (url === '/api/groups' && init?.method === 'POST') {
        return Promise.resolve(json(group({ id: 'g-new', name: 'Oat milk' })));
      }
      return Promise.resolve(json(group()));
    }));

    renderSheet();
    const suggested = await screen.findByRole('button', { name: /Peanut butter/ });
    expect(suggested).toHaveTextContent('Suggested · same need');
    fireEvent.click(screen.getByRole('button', { name: 'Soup' }));
    await waitFor(() => expect(posts[0]).toEqual({
      url: '/api/groups/soup/members',
      body: { productIds: ['oat'] },
    }));

    fireEvent.click(screen.getByRole('button', { name: 'New group "Oat milk"' }));
    expect(await screen.findByText('Path /groups/g-new')).toBeInTheDocument();
  });

  it('asks which supply setting the group should keep', async () => {
    const posts: unknown[] = [];
    vi.stubGlobal('fetch', vi.fn((_input: RequestInfo | URL, init?: RequestInit) => {
      const body = JSON.parse(String(init?.body)) as { target?: unknown };
      posts.push(body);
      if (!body.target) {
        return Promise.resolve(json({
          error: 'Choose what this group should keep on hand.',
          code: 'target_decision_required',
          members: [{ productId: 'oat', name: 'Oat milk', quantity: 4, amount: 48, dimension: 'mass' }],
        }, 409));
      }
      return Promise.resolve(json(group({ windowMonths: 6 })));
    }));

    const onChanged = renderSheet({
      groups: [group({ windowMonths: 6 })],
      suggestions: [],
    });
    fireEvent.click(screen.getByRole('button', { name: 'Peanut butter' }));
    fireEvent.click(await screen.findByRole('button', { name: 'Use 48 ounces (from Oat milk)' }));
    await waitFor(() => expect(posts[1]).toEqual({
      productIds: ['oat'],
      target: { quantity: 48, dimension: 'mass' },
    }));
    expect(onChanged).toHaveBeenCalled();
  });

  it('filters the group list', () => {
    const rows = Array.from({ length: 9 }, (_, index) => group({
      id: `g${index}`,
      name: index === 8 ? 'Zucchini' : `Pantry ${index}`,
    }));
    renderSheet({ groups: rows, suggestions: [] });
    fireEvent.change(screen.getByLabelText('Search groups'), { target: { value: 'zuc' } });
    expect(screen.getByRole('button', { name: 'Zucchini' })).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Pantry 0' })).not.toBeInTheDocument();
    fireEvent.change(screen.getByLabelText('Search groups'), { target: { value: 'missing' } });
    expect(screen.getByText('No matching groups.')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'New group "Oat milk"' })).toBeInTheDocument();
  });
});
