import { describe, expect, it } from 'vitest';
import { onHandByProduct, pairUndoBody, pairUndoCopy, pairUndoHideLine } from './contract';

const rows = [
  { itemName: 'Sour cream', onHand: 0, hide: true },
  { itemName: 'Sour cream', onHand: 2, hide: false },
  { itemName: 'Milk', onHand: 1, hide: false },
  { itemName: 'Oats', onHand: 4, hide: false },
  { itemName: 'Yogurt', onHand: 0, hide: true },
];

describe('pair undo confirmation', () => {
  it.each(rows)('uses the settled sentences for $itemName at $onHand on hand', ({ itemName, onHand, hide }) => {
    const copy = pairUndoCopy(itemName, onHand);
    expect(copy.title).toBe('Undo "Add it and take it out"?');
    expect(copy.body).toBe(`This removes both the add and the take-out for ${itemName}, as if neither happened.`);
    expect(copy.body).toBe(pairUndoBody(itemName));
    expect(copy.hideLine).toBe(hide ? `${itemName} will be hidden from your inventory again, since none are on hand.` : '');
    if (hide) expect(copy.hideLine).toBe(pairUndoHideLine(itemName));
    expect(copy.confirm).toBe('Undo');
    expect(copy.cancel).toBe('Cancel');
    expect(`${copy.title} ${copy.body} ${copy.hideLine}`).not.toMatch(/pace|history|unit/i);
    expect(copy.body).not.toMatch(/\d/);
  });

  it('reads live units from inventory onHand, then instanceCount', () => {
    const counts = onHandByProduct([
      { item: { productId: 'prod-empty' }, instanceCount: 0 },
      { item: { productId: 'prod-full' }, instanceCount: 2 },
      {
        item: { productId: 'prod-member' },
        instanceCount: 0,
        group: { members: [{ productId: 'prod-member', onHand: 3 }] },
      },
      {
        item: { productId: 'prod-wiped' },
        instanceCount: 1,
        group: { members: [{ productId: 'prod-other', onHand: 1 }, { productId: 'prod-wiped', onHand: 0 }] },
      },
    ]);
    expect(counts.get('prod-empty')).toBe(0);
    expect(counts.get('prod-full')).toBe(2);
    expect(counts.get('prod-member')).toBe(3);
    expect(counts.get('prod-wiped')).toBe(0);
    expect(counts.get('prod-missing')).toBeUndefined();
    expect(onHandByProduct(undefined as never).size).toBe(0);
  });
});
