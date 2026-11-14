import { useId, useRef } from 'react';
import { Text } from '@mantine/core';
import type { ShoppingListGroupMember } from '@stockbot/shared/types';
import { ProductPhoto } from '../inventory/ProductPhoto';
import { buildTripBuyViews } from './tripBuyDifferentiator';
import '../inventory/shelf.css';
import './tripBuyPicker.css';

const SWAP_ERROR = "Couldn't change what to buy. Try again.";

export { SWAP_ERROR };

export function TripBuyPicker({
  members,
  selectedItemId,
  disabled,
  busy,
  errorMessage,
  onSelect,
}: {
  members: ShoppingListGroupMember[];
  selectedItemId: string;
  disabled?: boolean;
  busy?: boolean;
  errorMessage?: string;
  onSelect: (itemId: string) => void;
}) {
  const groupId = useId();
  const errorId = `${groupId}-error`;
  const views = buildTripBuyViews(members);
  const optionRefs = useRef<Record<string, HTMLInputElement | null>>({});

  if (members.length <= 1) return null;

  const moveFocus = (fromIndex: number, direction: 1 | -1) => {
    const nextIndex = fromIndex + direction;
    if (nextIndex < 0 || nextIndex >= members.length) return;
    const nextId = members[nextIndex]?.itemId;
    if (nextId) optionRefs.current[nextId]?.focus();
  };

  return (
    <fieldset
      className="trip-buy-picker"
      aria-busy={busy || undefined}
      aria-describedby={errorMessage ? errorId : undefined}
      disabled={disabled || busy}
    >
      <legend className="trip-buy-picker__legend">This trip, buy</legend>
      <div role="radiogroup" aria-label="This trip, buy" className="trip-buy-picker__list">
        {views.map((view, index) => {
          const inputId = `${groupId}-${view.itemId}`;
          const isSelected = view.itemId === selectedItemId;
          return (
            <label key={view.itemId} className="trip-buy-picker__option" htmlFor={inputId}>
              <input
                ref={(node) => {
                  optionRefs.current[view.itemId] = node;
                }}
                id={inputId}
                type="radio"
                name={groupId}
                className="trip-buy-picker__radio"
                value={view.itemId}
                checked={isSelected}
                aria-label={view.accessibleName}
                onChange={() => {
                  if (view.itemId !== selectedItemId) onSelect(view.itemId);
                }}
                onKeyDown={(event) => {
                  if (event.key === 'ArrowDown') {
                    event.preventDefault();
                    moveFocus(index, 1);
                  } else if (event.key === 'ArrowUp') {
                    event.preventDefault();
                    moveFocus(index, -1);
                  }
                }}
              />
              <ProductPhoto
                src={view.imageUrl}
                name={view.displayName}
                decorative
                showSkeleton
                initialsFallback
              />
              <span className="trip-buy-picker__copy">
                <Text component="span" size="sm" className="trip-buy-picker__name">
                  {view.displayName}
                </Text>
                {view.differentiator !== undefined && (
                  <Text
                    component="span"
                    size="xs"
                    className={
                      view.differentiatorField === 'onHand' && view.differentiator === '0 on hand'
                        ? 'trip-buy-picker__diff stock-note stock-note--out'
                        : 'trip-buy-picker__diff'
                    }
                  >
                    {view.differentiator}
                  </Text>
                )}
              </span>
            </label>
          );
        })}
      </div>
      {errorMessage !== undefined && errorMessage !== '' && (
        <Text id={errorId} role="alert" size="sm" c="red" className="trip-buy-picker__error">
          {errorMessage}
        </Text>
      )}
    </fieldset>
  );
}