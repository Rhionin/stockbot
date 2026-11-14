import type { ReactNode } from 'react';
import { fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { MantineProvider } from '@mantine/core';
import { describe, expect, it, vi } from 'vitest';
import type { ShoppingListGroupMember } from '@stockbot/shared/types';
import { TripBuyPicker } from './TripBuyPicker';
import { TRIP_BUY_SWAP_ERROR } from './tripBuyCopy';
import { lastBoughtDiffers, pickDifferentiatorField } from './tripBuyDifferentiator';

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
        errorMessage={TRIP_BUY_SWAP_ERROR}
        onSelect={() => {}}
      />,
    );
    expect(screen.getByRole('alert')).toHaveTextContent(TRIP_BUY_SWAP_ERROR);
  });

  it('uses decorative alt on option photos when imageUrl is present', () => {
    const withPhotos: ShoppingListGroupMember[] = [
      { ...members[0]!, imageUrl: 'https://example.test/664-a.jpg' },
      { ...members[1]!, imageUrl: 'https://example.test/664-b.jpg' },
    ];
    const { container } = wrap(<TripBuyPicker members={withPhotos} selectedItemId="item-a" onSelect={() => {}} />);
    const imgs = [...container.querySelectorAll('img')];
    expect(imgs.length).toBe(2);
    expect(imgs.every((img) => img.getAttribute('alt') === '')).toBe(true);
    expect(screen.queryByText('14.5 oz')).not.toBeInTheDocument();
  });

  it('replaces a failed image with a fallback tile and no error text', () => {
    const withPhoto: ShoppingListGroupMember[] = [
      { ...members[0]!, imageUrl: 'https://example.test/broken.jpg' },
      {
        itemId: 'item-c',
        productId: 'prod-c',
        name: 'Other Beans',
        imageUrl: 'https://example.test/other.jpg',
        onHand: 0,
      },
    ];
    const { container } = wrap(<TripBuyPicker members={withPhoto} selectedItemId="item-a" onSelect={() => {}} />);
    const brokenOption = container.querySelector('label[for$="-item-a"] .shelf-photo');
    const img = brokenOption?.querySelector('img');
    expect(img).not.toBeNull();
    fireEvent.error(img!);
    expect(screen.queryByRole('alert')).not.toBeInTheDocument();
    expect(brokenOption?.querySelector('img')).toBeNull();
    expect(brokenOption?.querySelector('.shelf-photo-fallback')).toBeTruthy();
  });

  it('calls onSelect when another option is chosen', async () => {
    const onSelect = vi.fn();
    wrap(<TripBuyPicker members={members} selectedItemId="item-a" onSelect={onSelect} />);
    fireEvent.click(screen.getByRole('radio', { name: 'Kroger Cut Green Beans, 8 oz' }));
    await waitFor(() => expect(onSelect).toHaveBeenCalledWith('item-b'));
  });

  it('leads each no-image option with a 48×48 fallback tile before the name', () => {
    const { container } = wrap(
      <TripBuyPicker members={members} selectedItemId="item-a" onSelect={() => {}} />,
    );
    const options = container.querySelectorAll('.trip-buy-picker__option');
    expect(options.length).toBe(2);
    for (const option of options) {
      const photo = option.querySelector('.shelf-photo');
      expect(photo?.querySelector('.shelf-photo-fallback')).toBeTruthy();
      const fallback = option.querySelector('.shelf-photo-fallback');
      expect(fallback).toBeTruthy();
      expect(option.querySelector('.shelf-photo-fallback')?.compareDocumentPosition(option.querySelector('.trip-buy-picker__name')!))
        .toBe(Node.DOCUMENT_POSITION_FOLLOWING);
    }
  });

  it('does not call onSelect while busy', () => {
    const onSelect = vi.fn();
    wrap(<TripBuyPicker members={members} selectedItemId="item-a" busy onSelect={onSelect} />);
    fireEvent.click(screen.getByRole('radio', { name: 'Kroger Cut Green Beans, 8 oz' }));
    expect(onSelect).not.toHaveBeenCalled();
  });

  it('sets aria-busy on the fieldset while saving', () => {
    const { container } = wrap(
      <TripBuyPicker members={members} selectedItemId="item-a" busy onSelect={() => {}} />,
    );
    expect(container.querySelector('fieldset')).toHaveAttribute('aria-busy', 'true');
  });

  it('arrow keys move focus without selecting; Enter selects', () => {
    const onSelect = vi.fn();
    const { container } = wrap(
      <TripBuyPicker members={members} selectedItemId="item-a" onSelect={onSelect} />,
    );
    const first = screen.getByRole('radio', { name: 'Kroger Cut Green Beans, 14.5 oz' });
    const second = screen.getByRole('radio', { name: 'Kroger Cut Green Beans, 8 oz' });
    first.focus();
    fireEvent.keyDown(first, { key: 'ArrowRight' });
    expect(second).toHaveFocus();
    expect(onSelect).not.toHaveBeenCalled();
    fireEvent.keyDown(second, { key: 'Enter' });
    expect(onSelect).toHaveBeenCalledWith('item-b');
    expect(container.querySelectorAll('.trip-buy-picker__diff')).toHaveLength(2);
  });

  it('shows size differentiators after both images fail to load', () => {
    const withUrls: ShoppingListGroupMember[] = [
      { ...members[0]!, imageUrl: 'https://example.test/a.jpg' },
      { ...members[1]!, imageUrl: 'https://example.test/b.jpg' },
    ];
    const { container } = wrap(
      <TripBuyPicker members={withUrls} selectedItemId="item-a" onSelect={() => {}} />,
    );
    expect(screen.getAllByRole('radio', { name: 'Kroger Cut Green Beans' })).toHaveLength(2);
    const imgs = container.querySelectorAll('img');
    fireEvent.error(imgs[0]!);
    fireEvent.error(imgs[1]!);
    expect(screen.getByRole('radio', { name: 'Kroger Cut Green Beans, 14.5 oz' })).toBeInTheDocument();
    expect(screen.getByRole('radio', { name: 'Kroger Cut Green Beans, 8 oz' })).toBeInTheDocument();
  });

  it('never shows 0 on hand when onHand is missing on a member', () => {
    const colliding: ShoppingListGroupMember[] = [
      { itemId: 'a', productId: 'p-a', name: 'SAME', onHand: 0 },
      { itemId: 'b', productId: 'p-b', name: 'same' } as ShoppingListGroupMember,
    ];
    const { container } = wrap(
      <TripBuyPicker members={colliding} selectedItemId="item-a" onSelect={() => {}} />,
    );
    const picker = container.querySelector('.trip-buy-picker');
    expect(picker).toBeTruthy();
    expect(within(picker as HTMLElement).queryByText('0 on hand')).not.toBeInTheDocument();
    expect((picker as HTMLElement).textContent).not.toMatch(/(?<![\d.])\b0\b(?![\d.])/);
  });

  it('shows on-hand differentiators for three colliding members when one onHand is omitted', () => {
    const membersThree: ShoppingListGroupMember[] = [
      {
        itemId: 'on-a',
        productId: 'p-on-a',
        name: 'Colliding Pantry Item',
        imageUrl: 'https://example.test/664-sdet-onhand.jpg',
        onHand: 2,
      },
      {
        itemId: 'on-b',
        productId: 'p-on-b',
        name: 'colliding pantry item',
        imageUrl: 'https://example.test/664-sdet-onhand.jpg',
        onHand: 5,
      },
      {
        itemId: 'on-c',
        productId: 'p-on-c',
        name: 'COLLIDING PANTRY ITEM',
        imageUrl: 'https://example.test/664-sdet-onhand.jpg',
      } as ShoppingListGroupMember,
    ];
    expect(pickDifferentiatorField(membersThree)).toBe('onHand');
    const { container } = wrap(
      <TripBuyPicker members={membersThree} selectedItemId="on-a" onSelect={() => {}} />,
    );
    expect(screen.getByText('2 on hand')).toBeInTheDocument();
    expect(screen.getByText('5 on hand')).toBeInTheDocument();
    expect(screen.getByRole('radio', { name: 'Colliding Pantry Item, 2 on hand' })).toBeInTheDocument();
    expect(screen.getByRole('radio', { name: 'Colliding Pantry Item, 5 on hand' })).toBeInTheDocument();
    const omittedOption = screen.getByDisplayValue('on-c').closest('.trip-buy-picker__option');
    expect(omittedOption).toBeTruthy();
    const omittedCopy = within(omittedOption as HTMLElement);
    expect(omittedCopy.queryByText('0 on hand')).not.toBeInTheDocument();
    expect((omittedOption as HTMLElement).textContent).not.toMatch(/(?<![\d.])\b0\b(?![\d.])/);
    expect(screen.getByRole('radio', { name: 'Colliding Pantry Item' })).toBeInTheDocument();
  });

  it('picks last bought when the UTC calendar day matches but Denver days differ', () => {
    const sameUtcDayDenverSplit: ShoppingListGroupMember[] = [
      {
        itemId: 'lb-a',
        productId: 'p-lb-a',
        name: 'Shared Shelf Item',
        imageUrl: 'https://example.test/664-sdet-lb.jpg',
        onHand: 0,
        lastStockedAt: '2026-11-14T06:00:00Z',
      },
      {
        itemId: 'lb-b',
        productId: 'p-lb-b',
        name: 'shared shelf item',
        imageUrl: 'https://example.test/664-sdet-lb.jpg',
        onHand: 0,
        lastStockedAt: '2026-11-14T20:00:00Z',
      },
    ];
    expect(lastBoughtDiffers(sameUtcDayDenverSplit)).toBe(true);
    expect(pickDifferentiatorField(sameUtcDayDenverSplit)).toBe('lastBought');
    wrap(<TripBuyPicker members={sameUtcDayDenverSplit} selectedItemId="lb-a" onSelect={() => {}} />);
    expect(screen.getByRole('radio', { name: 'Shared Shelf Item, Bought Nov 13' })).toBeInTheDocument();
    expect(screen.getByRole('radio', { name: 'Shared Shelf Item, Bought Nov 14' })).toBeInTheDocument();
  });
});