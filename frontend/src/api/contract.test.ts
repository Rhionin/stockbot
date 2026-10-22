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
  pairReshelves,
  pairUndoBody,
  pairUndoCopy,
  pairUndoHideLine,
  type PairUndoMove,
} from './contract';

const hideLine = 'Sour cream will be hidden from your inventory again, since none are on hand.';
const at = '2026-10-18T16:40:00.000Z';
const stockIn: PairUndoMove = {
  source: 'backfill',
  at,
  productId: 'prod-sour',
  reshelvesOnUndo: true,
};

const reshelveRows: { name: string; move: PairUndoMove; moves: PairUndoMove[]; hide: boolean }[] = [
  { name: 'true', move: { reshelvesOnUndo: true }, moves: [], hide: true },
  { name: 'false', move: { reshelvesOnUndo: false }, moves: [], hide: false },
  { name: 'absent', move: {}, moves: [], hide: false },
  {
    name: 'true on the paired stock-in',
    move: { source: 'scan', at, productId: 'prod-sour' },
    moves: [stockIn],
    hide: true,
  },
];

describe('pair undo confirmation', () => {
  it.each(reshelveRows)('shows the hide line only for $name', ({ move, moves, hide }) => {
    const reshelves = pairReshelves(move, moves);
    const copy = pairUndoCopy('Sour cream', reshelves ? true : undefined);
    expect(copy.title).toBe('Undo "Add it and take it out"?');
    expect(copy.body).toBe('This removes both the add and the take-out for Sour cream, as if neither happened.');
    expect(copy.body).toBe(pairUndoBody('Sour cream'));
    expect(copy.hideLine).toBe(hide ? hideLine : '');
    if (hide) expect(copy.hideLine).toBe(pairUndoHideLine('Sour cream'));
    expect(copy.confirm).toBe('Undo');
    expect(copy.cancel).toBe('Cancel');
    expect(`${copy.body} ${copy.hideLine}`).not.toMatch(/pace|history|unit/i);
  });

  it('leaves the hide line out when the flag was never loaded', () => {
    expect(pairReshelves({}, [])).toBe(false);
    expect(pairUndoCopy('Sour cream', undefined).hideLine).toBe('');
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
