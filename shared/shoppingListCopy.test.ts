import { describe, expect, it } from 'vitest';
import {
  RESTOCK_DONT_UNDO_DETAIL,
  SHOPPING_LIST_DONT_RESTOCK_MENU_HINT,
  SHOPPING_LIST_REMOVE_VISIBLE_LABEL,
  SHOPPING_LIST_UNDO_FAILURE,
  SHOPPING_LIST_START_NEW_CART_NOTE,
  formatItemStatusRelativeWhen,
  formatStagedCartSkipRemovalLine,
  shoppingListAfterRemoveGroupMessage,
  shoppingListAfterRemoveProductMessage,
  shoppingListRemoveGroupAriaLabel,
  shoppingListRemoveProductAriaLabel,
} from './shoppingListCopy';

describe('Remove control labels', () => {
  it('uses a visible label and product-specific accessible name', () => {
    expect(SHOPPING_LIST_REMOVE_VISIBLE_LABEL).toBe('Remove for this trip');
    expect(shoppingListRemoveProductAriaLabel('Plain rice')).toBe('Remove Plain rice for this trip');
    expect(shoppingListRemoveGroupAriaLabel('Cut green beans')).toBe(
      'Remove the Cut green beans group for this trip',
    );
  });
});

describe('after-remove inline status', () => {
  it('names the product or group and promises next trip', () => {
    expect(shoppingListAfterRemoveProductMessage('Plain rice')).toBe(
      'Plain rice is off the list for this trip. It comes back next trip.',
    );
    expect(shoppingListAfterRemoveGroupMessage('Cut green beans')).toBe(
      'The Cut green beans group is off the list for this trip. It comes back next trip.',
    );
  });
});

describe('formatItemStatusRelativeWhen', () => {
  const now = new Date(2026, 10, 9, 15, 0, 0);

  it('formats today, yesterday, weekday, and month day', () => {
    expect(formatItemStatusRelativeWhen(new Date(2026, 10, 9, 9, 0, 0).toISOString(), now)).toBe('today');
    expect(formatItemStatusRelativeWhen(new Date(2026, 10, 8, 9, 0, 0).toISOString(), now)).toBe('yesterday');
    expect(formatItemStatusRelativeWhen(new Date(2026, 10, 3, 9, 0, 0).toISOString(), now)).toBe('Tue');
    expect(formatItemStatusRelativeWhen(new Date(2026, 9, 15, 9, 0, 0).toISOString(), now)).toBe('Oct 15');
  });
});

describe('formatStagedCartSkipRemovalLine', () => {
  const now = new Date(2026, 10, 9, 15, 0, 0);
  const at = new Date(2026, 10, 3, 9, 0, 0).toISOString();

  it('legacy skip has no who or when', () => {
    expect(formatStagedCartSkipRemovalLine({})).toBe(
      'Removed for this trip. It comes back next trip.',
    );
    expect(
      formatStagedCartSkipRemovalLine({ actorDisplayName: 'Sam', actorUserId: 'u-1' }),
    ).toBe('Removed for this trip. It comes back next trip.');
  });

  it('actor and time without on/by in the middle clause', () => {
    expect(
      formatStagedCartSkipRemovalLine(
        { actorDisplayName: 'Alex', removedAt: at },
        { now, formatWhen: () => 'Mon' },
      ),
    ).toBe('Alex removed it for this trip Mon. It comes back next trip.');
    expect(
      formatStagedCartSkipRemovalLine({ actorDisplayName: 'Sam' }),
    ).toBe('Removed for this trip. It comes back next trip.');
    expect(
      formatStagedCartSkipRemovalLine({ removedAt: at }, { now, formatWhen: () => 'Mon' }),
    ).toBe('Removed for this trip Mon. It comes back next trip.');
  });

  it('owner without display name uses household admin', () => {
    const line = formatStagedCartSkipRemovalLine(
      { actorUserId: 'u-owner', actorRole: 'owner', removedAt: at },
      { now, formatWhen: () => 'Mon' },
    );
    expect(line).toBe('A household admin removed it for this trip Mon. It comes back next trip.');
    expect(line).not.toMatch(/operator|owner|someone/i);
  });

  it('member with user id but no name drops who', () => {
    expect(
      formatStagedCartSkipRemovalLine(
        { actorUserId: 'u-member', actorRole: 'member', removedAt: at },
        { now, formatWhen: () => 'Mon' },
      ),
    ).toBe('Removed for this trip Mon. It comes back next trip.');
  });

  it('group variants', () => {
    expect(
      formatStagedCartSkipRemovalLine(
        {
          groupName: 'Soda',
          actorDisplayName: 'Alex',
          removedAt: at,
        },
        { now, formatWhen: () => 'Mon' },
      ),
    ).toBe('Alex removed the Soda group for this trip Mon. It comes back next trip.');
  });
});

describe('other W1 strings', () => {
  it('covers ancillary copy', () => {
    expect(SHOPPING_LIST_UNDO_FAILURE).toBe("Couldn't undo. Try again.");
    expect(SHOPPING_LIST_START_NEW_CART_NOTE).toMatch(/brings back items removed for this trip/);
    expect(SHOPPING_LIST_DONT_RESTOCK_MENU_HINT).toMatch(/restock target/);
    expect(RESTOCK_DONT_UNDO_DETAIL).toBe('Its restock target is now 0.');
  });
});
