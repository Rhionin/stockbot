import React from 'react';
import { fireEvent, render, screen } from '@testing-library/react-native';
import { ZERO_FOR_TRIP_DONT_RESTOCK_ERROR } from '@stockbot/shared/zeroForTripCopy';
import { ZeroForTripNudgeMobile } from './ZeroForTripNudge';

const restock = {
  items: [{ itemId: 'item-milk', target: { kind: 'amount' as const, value: 2 } }],
  categories: [],
};

describe('ZeroForTripNudgeMobile', () => {
  it('hides nudge when target is already 0', async () => {
    await render(
      <ZeroForTripNudgeMobile
        line={{ itemId: 'item-milk', productName: 'Milk' }}
        restock={{
          items: [{ itemId: 'item-milk', target: { kind: 'months' as const, value: 0 } }],
          categories: [],
        }}
        onDontRestock={jest.fn()}
      />,
    );
    expect(screen.queryByTestId('zero-for-trip-nudge')).toBeNull();
  });

  it('hides nudge when the viewer cannot change the target', async () => {
    await render(
      <ZeroForTripNudgeMobile
        line={{ itemId: 'item-oats', productName: 'Oats' }}
        restock={restock}
        onDontRestock={jest.fn()}
      />,
    );
    expect(screen.queryByTestId('zero-for-trip-nudge')).toBeNull();
  });

  it('invokes Dont restock', async () => {
    const onDontRestock = jest.fn();
    await render(
      <ZeroForTripNudgeMobile
        line={{ itemId: 'item-milk', productName: 'Milk' }}
        restock={restock}
        onDontRestock={onDontRestock}
      />,
    );
    fireEvent.press(screen.getByTestId('zero-for-trip-nudge-button'));
    expect(onDontRestock).toHaveBeenCalled();
  });

  it('shows a spinner while loading without the ellipsis glyph', async () => {
    await render(
      <ZeroForTripNudgeMobile
        line={{ itemId: 'item-milk', productName: 'Milk' }}
        restock={restock}
        loading
        onDontRestock={jest.fn()}
      />,
    );
    expect(screen.getByTestId('zero-for-trip-nudge-button').props.accessibilityState?.busy).toBe(true);
    expect(screen.queryByText('…')).toBeNull();
  });

  it('shows error copy', async () => {
    await render(
      <ZeroForTripNudgeMobile
        line={{ itemId: 'item-milk', productName: 'Milk' }}
        restock={restock}
        error={ZERO_FOR_TRIP_DONT_RESTOCK_ERROR}
        onDontRestock={jest.fn()}
      />,
    );
    expect(screen.getByText(ZERO_FOR_TRIP_DONT_RESTOCK_ERROR)).toBeTruthy();
  });
});