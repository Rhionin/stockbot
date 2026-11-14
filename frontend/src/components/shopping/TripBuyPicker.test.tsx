import type { ReactNode } from 'react';
import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { MantineProvider } from '@mantine/core';
import { describe, expect, it, vi } from 'vitest';
import type { ShoppingListGroupMember } from '@stockbot/shared/types';
import { TripBuyPicker } from './TripBuyPicker';

const wrap = (ui: ReactNode) => render(<MantineProvider>{ui}</MantineProvider>);

const members: ShoppingListGroupMember[] = [
  {
    itemId: 'item-a',
    productId: 'prod-a',
    name: 'KROGER CUT GREEN BEANS',
    netAmount: 14.5,
    netUnit: 'oz',
    onHand: 3,
    lastStockedAt: '2026-01-01T00:00:00Z',
  },
  {
    itemId: 'item-b',
    productId: 'prod-b',
    name: 'Kroger Cut Green Beans',
    netAmount: 8,
    netUnit: 'oz',
    onHand: 1,
    lastStockedAt: '2026-02-01T00:00:00Z',
  },
];

describe('TripBuyPicker', () => {
  it('exposes radiogroup labels with one differentiator and decorative photos', () => {
    wrap(
      <TripBuyPicker members={members} selectedItemId="item-a" onSelect={() => {}} />,
    );
    expect(screen.getByRole('radiogroup', { name: 'This trip, buy' })).toBeInTheDocument();
    expect(screen.getByRole('radio', { name: 'Kroger Cut Green Beans, 14.5 oz' })).toBeChecked();
    expect(screen.getByRole('radio', { name: 'Kroger Cut Green Beans, 8 oz' })).not.toBeChecked();
    expect(screen.queryByText('3 on hand')).not.toBeInTheDocument();
    expect(screen.queryByText(/Bought /)).not.toBeInTheDocument();
  });

  it('shows inline swap error with alert role', () => {
    wrap(
      <TripBuyPicker
        members={members}
        selectedItemId="item-a"
        errorMessage="Couldn't change what to buy. Try again."
        onSelect={() => {}}
      />,
    );
    expect(screen.getByRole('alert')).toHaveTextContent("Couldn't change what to buy. Try again.");
  });

  it('calls onSelect when another option is chosen', async () => {
    const onSelect = vi.fn();
    wrap(<TripBuyPicker members={members} selectedItemId="item-a" onSelect={onSelect} />);
    fireEvent.click(screen.getByRole('radio', { name: 'Kroger Cut Green Beans, 8 oz' }));
    await waitFor(() => expect(onSelect).toHaveBeenCalledWith('item-b'));
  });
});