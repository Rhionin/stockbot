import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { MantineProvider } from '@mantine/core';
import { describe, expect, it, vi } from 'vitest';
import type { ProductGroup } from '@stockbot/shared/types';
import { GroupMigrationNoticeCard } from './GroupMigrationNotice';

vi.mock('../../api/client', () => ({
  dismissGroupMigrationNotice: vi.fn(async () => ({
    id: 'g1',
    name: 'Oats',
    rule: 'same_as_ran_out',
    ruleConfirmed: true,
    members: [],
    runningLow: false,
    migrationNotice: null,
    target: { kind: 'items', value: 3, unit: 'items', status: 'ok' },
  })),
}));

const group: ProductGroup = {
  id: 'g1',
  name: 'Oats',
  rule: 'same_as_ran_out',
  ruleConfirmed: true,
  members: [],
  runningLow: false,
  migrationNotice: { case: 3, products: [{ productId: 'p1', name: 'Rolled oats 42 oz' }] },
  target: { kind: 'items', value: 3, unit: 'items', status: 'ok' },
};

describe('F migration notice', () => {
  it('F1 shows items copy and F4 dismisses', async () => {
    const onUpdated = vi.fn();
    render(
      <MantineProvider>
        <GroupMigrationNoticeCard group={group} onUpdated={onUpdated} />
      </MantineProvider>,
    );
    expect(screen.getByText(/keeps 3 items on hand/)).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: 'Dismiss this note' }));
    await waitFor(() => expect(onUpdated).toHaveBeenCalled());
  });
});