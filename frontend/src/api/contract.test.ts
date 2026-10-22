import { describe, expect, it } from 'vitest';
import {
  backfillFailureKind,
  CODE_DIRECTION_UNSET,
  CODE_INTERNAL,
  CODE_NONE_ON_HAND,
  CODE_NOT_PENDING,
  CODE_NOT_STOCK_OUT,
  CODE_PRODUCT_UNRESOLVED,
  CODE_SCAN_NOT_FOUND,
  CODE_UNITS_ON_HAND,
  pairStockInMoveId,
  pairUndoBody,
  pairUndoCopy,
  pairUndoHideLine,
  previewReshelves,
  type UndoPreview,
} from './contract';

const hideLine = 'Sour cream will be hidden from your inventory again, since none are on hand.';
const at = '2026-10-18T16:40:00.000Z';

const effect = (reshelvesOnUndo: boolean, stockInMoveId = 'move-cream-in'): UndoPreview['effects'][number] => ({
  itemId: 'item-sour-cream',
  productId: 'prod-sour-cream',
  name: 'Sour cream',
  stockInMoveId,
  stockOutMoveId: 'move-cream-out',
  onHand: 0,
  reshelvesOnUndo,
});

const previewRows: { name: string; preview: UndoPreview; hide: boolean }[] = [
  { name: 'true', preview: { effects: [effect(true)] }, hide: true },
  { name: 'false', preview: { effects: [effect(false)] }, hide: false },
  { name: 'no matching effect', preview: { effects: [effect(true, 'move-other')] }, hide: false },
];

describe('pair undo confirmation', () => {
  it.each(previewRows)('shows the hide line only when the preview effect is $name', ({ preview, hide }) => {
    expect(previewReshelves(preview, 'move-cream-in')).toBe(hide);
    const copy = pairUndoCopy('Sour cream', hide ? true : undefined);
    expect(copy.title).toBe('Undo "Add it and take it out"?');
    expect(copy.body).toBe('This removes both the add and the take-out for Sour cream, as if neither happened.');
    expect(copy.body).toBe(pairUndoBody('Sour cream'));
    expect(copy.hideLine).toBe(hide ? hideLine : '');
    if (hide) expect(copy.hideLine).toBe(pairUndoHideLine('Sour cream'));
    expect(copy.confirm).toBe('Undo');
    expect(copy.cancel).toBe('Cancel');
    expect(copy.body).not.toMatch(/\d/);
    expect(`${copy.body} ${copy.hideLine}`).not.toMatch(/pace|history|unit/i);
  });

  it('leaves the hide line out when the preview never arrives', () => {
    expect(previewReshelves(undefined, 'move-cream-in')).toBe(false);
    expect(pairUndoCopy('Sour cream', undefined).hideLine).toBe('');
  });

  const stockIn = { id: 'move-cream-in', source: 'backfill', at, productId: 'prod-sour' };
  const stockOut = { id: 'move-cream-out', source: 'scan', at, productId: 'prod-sour' };
  const pairMoves = [stockOut, stockIn];

  it.each([
    { name: 'the stock-in row', move: stockIn, moves: pairMoves, id: 'move-cream-in' },
    { name: 'the paired stock-out', move: stockOut, moves: pairMoves, id: 'move-cream-in' },
    {
      name: 'a different product',
      move: { id: 'move-other-out', source: 'scan', at, productId: 'prod-other' },
      moves: pairMoves,
      id: 'move-other-out',
    },
    {
      name: 'a different time',
      move: { id: 'move-later-out', source: 'scan', at: '2026-10-19T16:40:00.000Z', productId: 'prod-sour' },
      moves: pairMoves,
      id: 'move-later-out',
    },
  ])('previews the backfill stock-in id for $name', ({ move, moves, id }) => {
    expect(pairStockInMoveId(move, moves)).toBe(id);
  });

  it.each([
    {
      name: 'the stock-out id of this move',
      effectId: { stockInMoveId: 'move-other', stockOutMoveId: 'move-cream-in' },
      reshelvesOnUndo: true,
      hide: true,
    },
    {
      name: 'a different move',
      effectId: { stockInMoveId: 'move-other', stockOutMoveId: 'move-else' },
      reshelvesOnUndo: true,
      hide: false,
    },
    {
      name: 'this move when the undo does not re-shelve',
      effectId: { stockInMoveId: 'move-cream-in', stockOutMoveId: 'move-cream-out' },
      reshelvesOnUndo: false,
      hide: false,
    },
  ])('matches the preview effect for $name', ({ effectId, reshelvesOnUndo, hide }) => {
    expect(previewReshelves({
      effects: [{
        itemId: 'item-sour-cream',
        productId: 'prod-sour-cream',
        name: 'Sour cream',
        onHand: 0,
        reshelvesOnUndo,
        ...effectId,
      }],
    }, 'move-cream-in')).toBe(hide);
  });
});

const failureKinds: { code: string | undefined; kind: string }[] = [
  { code: CODE_PRODUCT_UNRESOLVED, kind: 'define' },
  { code: CODE_UNITS_ON_HAND, kind: 'stock-out' },
  { code: CODE_NOT_PENDING, kind: 'reload' },
  { code: CODE_SCAN_NOT_FOUND, kind: 'reload' },
  { code: CODE_NOT_STOCK_OUT, kind: 'sentence' },
  { code: CODE_DIRECTION_UNSET, kind: 'sentence' },
  { code: CODE_NONE_ON_HAND, kind: 'sentence' },
  { code: CODE_INTERNAL, kind: 'sentence' },
  { code: 'anything-else', kind: 'sentence' },
  { code: undefined, kind: 'sentence' },
];

describe('backfill failure kind', () => {
  it.each(failureKinds)('$code is $kind', ({ code, kind }) => {
    expect(backfillFailureKind(code)).toBe(kind);
  });
});
