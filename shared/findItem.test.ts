import { describe, expect, it } from 'vitest';
import { addItemSubmitLabel, findItemView, hitSubtitle, onHandLabel, runeLength } from './findItem';
import type { ProductSearchHit } from './types';

const hit = (name: string, extra: Partial<ProductSearchHit> = {}): ProductSearchHit => ({
  productId: `p-${name}`,
  itemId: null,
  name,
  onHand: 0,
  ...extra,
});

describe('onHandLabel', () => {
  it('names none, one and many', () => {
    expect(onHandLabel(0)).toBe('None on hand');
    expect(onHandLabel(1)).toBe('1 on hand');
    expect(onHandLabel(4)).toBe('4 on hand');
  });
});

describe('runeLength', () => {
  it('counts characters, not UTF-16 units', () => {
    expect(runeLength('ab')).toBe(2);
    expect(runeLength('\u{1F34E}')).toBe(1);
  });
});

describe('hitSubtitle', () => {
  it('joins the brand and size that exist', () => {
    expect(hitSubtitle(hit('a', { brand: 'Pantry', size: '16 oz' }))).toBe('Pantry · 16 oz');
    expect(hitSubtitle(hit('a', { brand: '', size: '16 oz' }))).toBe('16 oz');
    expect(hitSubtitle(hit('a'))).toBe('');
  });
});

describe('findItemView', () => {
  it('shows nothing and no error before anything is typed', () => {
    expect(findItemView('', '', [hit('Jam')])).toMatchObject({ tooShort: false, searchable: false, rows: [], showAddNamed: false });
  });

  it('flags one letter and offers no rows or add button', () => {
    expect(findItemView(' j ', 'j', [hit('Jam')])).toMatchObject({ tooShort: true, searchable: false, rows: [], showAddNamed: false });
  });

  it('keeps the rows of the debounced query and offers to add the typed name', () => {
    const view = findItemView('jam', 'jam', [hit('Jam jar')]);
    expect(view.rows).toHaveLength(1);
    expect(view.showAddNamed).toBe(true);
    expect(view.trimmed).toBe('jam');
  });

  it('drops the add button when a result is named exactly what was typed, in any case', () => {
    expect(findItemView('JAM', 'jam', [hit('Jam')]).showAddNamed).toBe(false);
  });

  it('clears rows when the debounced text falls below the minimum', () => {
    expect(findItemView('jam', 'j', [hit('Jam')]).rows).toEqual([]);
  });
});

describe('addItemSubmitLabel', () => {
  it('names what saving does in each mode', () => {
    expect(addItemSubmitLabel).toEqual({
      inventory: 'Add item',
      stock_in: 'Add and stock in',
      stock_out: 'Add it and take it out',
    });
  });
});
