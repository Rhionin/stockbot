import {
  RESTOCK_DONT_UNDO_DETAIL,
  SHOPPING_LIST_DONT_RESTOCK_MENU_HINT,
  SHOPPING_LIST_REMOVE_VISIBLE_LABEL,
  SHOPPING_LIST_START_NEW_CART_NOTE,
  formatStagedCartSkipRemovalLine,
  shoppingListAfterRemoveProductMessage,
  shoppingListRemoveGroupAriaLabel,
  shoppingListRemoveProductAriaLabel,
} from '@stockbot/shared/shoppingListCopy';

describe('shopping list remove copy parity (W1)', () => {
  it('matches the web Remove control strings', () => {
    expect(SHOPPING_LIST_REMOVE_VISIBLE_LABEL).toBe('Remove for this trip');
    expect(shoppingListRemoveProductAriaLabel('Oats')).toBe('Remove Oats for this trip');
    expect(shoppingListRemoveGroupAriaLabel('Soda')).toBe('Remove the Soda group for this trip');
  });

  it('matches ancillary and status copy', () => {
    expect(SHOPPING_LIST_START_NEW_CART_NOTE).toMatch(/removed for this trip/);
    expect(SHOPPING_LIST_DONT_RESTOCK_MENU_HINT).toMatch(/restock target/);
    expect(RESTOCK_DONT_UNDO_DETAIL).toBe('Its restock target is now 0.');
    expect(shoppingListAfterRemoveProductMessage('Oats')).toMatch(/comes back next trip/);
    expect(formatStagedCartSkipRemovalLine({})).toBe(
      'Removed for this trip. It comes back next trip.',
    );
  });
});
