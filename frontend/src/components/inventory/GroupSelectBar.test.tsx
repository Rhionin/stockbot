import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { MantineProvider } from '@mantine/core';
import { describe, expect, it, vi } from 'vitest';
import type { ProductGroup } from '@stockbot/shared/types';
import { GroupSelectBar } from './GroupSelectBar';

const json = (body: unknown, status = 200) =>
  new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } });

const group = (extra: Partial<ProductGroup> = {}): ProductGroup => ({
  id: 'g1',
  name: 'Gatorade powder',
  rule: 'same_as_ran_out',
  ruleConfirmed: false,
  members: [],
  runningLow: false,
  ...extra,
});

const conflict = {
  error: 'Choose what this group should keep on hand.',
  code: 'target_decision_required',
  members: [{ productId: 'glacier', name: 'Gatorade Glacier Freeze', quantity: 4, amount: 48, dimension: 'mass' as const }],
};

describe('GroupSelectBar', () => {
  it('offers the existing group target and the member quantity', async () => {
    const posts: unknown[] = [];
    vi.stubGlobal('fetch', vi.fn((_input: RequestInfo | URL, init?: RequestInit) => {
      const body = JSON.parse(String(init?.body)) as { target?: unknown };
      posts.push(body);
      if (!body.target) return Promise.resolve(json(conflict, 409));
      return Promise.resolve(json(group({ windowMonths: 6 })));
    }));

    render(
      <MantineProvider>
        <GroupSelectBar productIds={['glacier']} groups={[group({ windowMonths: 6 })]} onDone={() => {}} />
      </MantineProvider>,
    );

    fireEvent.click(screen.getByRole('button', { name: 'Add to group' }));
    expect(await screen.findByRole('button', { name: "Keep the group's 6 months" })).toBeInTheDocument();
    expect(screen.getByText('Gatorade Glacier Freeze: 4 on hand (48 ounces)')).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Use ounces' })).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Use months' })).not.toBeInTheDocument();

    fireEvent.click(screen.getByRole('button', { name: 'Use 48 ounces (from Gatorade Glacier Freeze)' }));
    await waitFor(() => expect(posts).toHaveLength(2));
    expect(posts[1]).toEqual({ productIds: ['glacier'], target: { quantity: 48, dimension: 'mass' } });
  });

  it('keeps the household default for a new group and cancel sends nothing else', async () => {
    const posts: { target?: { clear?: boolean } }[] = [];
    vi.stubGlobal('fetch', vi.fn((_input: RequestInfo | URL, init?: RequestInit) => {
      const body = JSON.parse(String(init?.body)) as { target?: { clear?: boolean } };
      posts.push(body);
      if (!body.target) return Promise.resolve(json(conflict, 409));
      return Promise.resolve(json(group()));
    }));

    render(
      <MantineProvider>
        <GroupSelectBar productIds={['glacier']} groups={[]} onDone={() => {}} />
      </MantineProvider>,
    );

    fireEvent.change(screen.getByLabelText('New group'), { target: { value: 'Drinks' } });
    fireEvent.click(screen.getByRole('button', { name: 'Group these' }));
    fireEvent.click(await screen.findByRole('button', { name: 'Cancel' }));
    expect(screen.queryByText('These products have their own supply setting. Which should the whole group use?')).not.toBeInTheDocument();
    expect(screen.getByLabelText('New group')).toHaveValue('Drinks');
    expect(posts).toHaveLength(1);

    fireEvent.click(screen.getByRole('button', { name: 'Group these' }));
    fireEvent.click(await screen.findByRole('button', { name: 'Keep the household default' }));
    await waitFor(() => expect(posts).toHaveLength(3));
    expect(posts[2]).toEqual({
      name: 'Drinks',
      productIds: ['glacier'],
      target: { clear: true },
    });
  });
});
