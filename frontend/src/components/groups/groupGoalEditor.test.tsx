import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { MantineProvider } from '@mantine/core';
import { describe, expect, it, vi } from 'vitest';
import type { ProductGroup } from '@stockbot/shared/types';
import { GroupGoalEditor } from './GroupGoalEditor';
import { resolveGroupGoal } from './groupGoalModel';
import { ApiError } from '../../api/client';
import { groupGoalCopy } from './groupGoalCopy';

const juiceGroup: ProductGroup = {
  id: 'g-juice',
  name: 'Juice',
  rule: 'same_as_ran_out',
  ruleConfirmed: true,
  members: [
    { productId: 'apple', name: 'Apple juice 64 fl oz', onHand: 1, sizeKind: 'volume', size: { amount: 64, unit: 'fl oz' } },
    { productId: 'grape', name: 'Grape juice 64 fl oz', onHand: 1, sizeKind: 'volume', size: { amount: 64, unit: 'fl oz' } },
  ],
  runningLow: false,
  target: null,
  amountAvailable: true,
  amountUnit: 'fl oz',
  defaultKind: 'months',
  defaultMonths: 3,
  version: 'v-juice',
};

function mount(group: ProductGroup, onSaveRequest = vi.fn(async () => group)) {
  const onReload = vi.fn(async () => group);
  const onSaved = vi.fn();
  render(
    <MantineProvider>
      <GroupGoalEditor
        group={group}
        resolved={resolveGroupGoal(group)}
        onCancel={() => {}}
        onSaved={onSaved}
        onSaveRequest={onSaveRequest}
        onReload={onReload}
      />
    </MantineProvider>,
  );
  return { onSaveRequest, onSaved, onReload };
}

describe('GroupGoalEditor', () => {
  it('switches kinds without converting drafts', async () => {
    mount(juiceGroup);
    fireEvent.click(screen.getByRole('radio', { name: /Number of items/ }));
    const itemsField = screen.getByRole('textbox', { name: groupGoalCopy.e4 });
    fireEvent.change(itemsField, { target: { value: '2' } });
    fireEvent.click(screen.getByRole('radio', { name: /Months of supply/ }));
    fireEvent.click(screen.getByRole('radio', { name: /Number of items/ }));
    expect(screen.getByRole('textbox', { name: groupGoalCopy.e4 })).toHaveValue('2');
  });

  it('maps 409 conflict beside Save with Refresh', async () => {
    const onSaveRequest = vi.fn(async () => {
      throw new ApiError(409, 'stale', 'conflict');
    });
    mount(juiceGroup, onSaveRequest);
    fireEvent.click(screen.getByRole('button', { name: groupGoalCopy.save }));
    expect(await screen.findByRole('alert')).toHaveTextContent(groupGoalCopy.h3);
    expect(screen.getByRole('button', { name: groupGoalCopy.e9 })).toBeVisible();
  });

  it('shows amount_unavailable blockers after reload', async () => {
    const withBlockers: ProductGroup = {
      ...juiceGroup,
      amountAvailable: false,
      amountBlockers: [{ productId: 'lemon', name: 'Frozen lemonade', reason: 'size_missing' }],
    };
    const onSaveRequest = vi.fn(async () => {
      throw new ApiError(
        422,
        'blocked',
        'amount_unavailable',
        [],
        [],
        '',
        undefined,
        '',
        '',
        [{ productId: 'lemon', name: 'Frozen lemonade', reason: 'size_missing' }],
      );
    });
    const onReload = vi.fn(async () => withBlockers);
    const onSaved = vi.fn();
    render(
      <MantineProvider>
        <GroupGoalEditor
          group={juiceGroup}
          resolved={resolveGroupGoal(juiceGroup)}
          onCancel={() => {}}
          onSaved={onSaved}
          onSaveRequest={onSaveRequest}
          onReload={onReload}
        />
      </MantineProvider>,
    );
    fireEvent.click(screen.getByRole('radio', { name: /Total fluid ounces/ }));
    fireEvent.change(screen.getByRole('textbox', { name: /Fluid ounces/ }), { target: { value: '64' } });
    fireEvent.click(screen.getByRole('button', { name: groupGoalCopy.save }));
    await waitFor(() => expect(onSaveRequest).toHaveBeenCalled());
    expect(await screen.findByText('Frozen lemonade: no size')).toBeVisible();
  });

  it('validates items range with E6', async () => {
    mount(juiceGroup);
    fireEvent.click(screen.getByRole('radio', { name: /Number of items/ }));
    const itemsField = screen.getByRole('textbox', { name: groupGoalCopy.e4 });
    fireEvent.change(itemsField, { target: { value: '0' } });
    fireEvent.click(screen.getByRole('button', { name: groupGoalCopy.save }));
    expect(await screen.findByText(groupGoalCopy.e6)).toBeVisible();
  });
});