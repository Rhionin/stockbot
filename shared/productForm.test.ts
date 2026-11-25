import { describe, expect, it } from 'vitest';
import {
  confirmSkipsNeedsName,
  emptyProductDraft,
  hasDraftErrors,
  NET_SIZE_PAIR_REQUIRED,
  NET_SIZE_POSITIVE,
  PACK_COUNT_MINIMUM,
  PACK_COUNT_WHOLE,
  PRODUCT_NAME_REQUIRED,
  productDraftInput,
  shareNoticeFor,
  validateProductDraft,
  withSize,
} from './productForm';
import type { ProductWriteResult } from './types';

const named = () => ({ ...emptyProductDraft(), name: 'Oat flour' });

describe('withSize', () => {
  it('marks size touched only when the amount or unit changed', () => {
    const draft = emptyProductDraft();
    const packOnly = withSize(draft, { ...draft.size, packCount: '6' });
    expect(packOnly.sizeTouched).toBe(false);
    expect(packOnly.packTouched).toBe(true);
    const amount = withSize(draft, { ...draft.size, amount: '16' });
    expect(amount.sizeTouched).toBe(true);
    expect(amount.packTouched).toBe(false);
  });

  it('keeps a touch once it has happened', () => {
    const touched = withSize(emptyProductDraft(), { ...emptyProductDraft().size, amount: '1' });
    const reverted = withSize(touched, { ...touched.size, amount: '' });
    expect(reverted.sizeTouched).toBe(true);
  });
});

describe('validateProductDraft', () => {
  it('needs a name', () => {
    expect(validateProductDraft(emptyProductDraft()).name).toBe(PRODUCT_NAME_REQUIRED);
    expect(validateProductDraft({ ...emptyProductDraft(), name: '   ' }).name).toBe(PRODUCT_NAME_REQUIRED);
    expect(hasDraftErrors(validateProductDraft(named()))).toBe(false);
  });

  it('asks for the size and the unit together', () => {
    const amountOnly = withSize(named(), { ...named().size, amount: '16' });
    expect(validateProductDraft(amountOnly).size).toBe(NET_SIZE_PAIR_REQUIRED);
    const unitOnly = withSize(named(), { ...named().size, unit: 'oz' });
    expect(validateProductDraft(unitOnly).size).toBe(NET_SIZE_PAIR_REQUIRED);
  });

  it.each(['0', '-3', 'abc', '1e3', '1,5', '12 oz'])('rejects the size %s', (amount) => {
    const draft = withSize(named(), { ...named().size, amount, unit: 'oz' });
    expect(validateProductDraft(draft).size).toBe(NET_SIZE_POSITIVE);
  });

  it.each(['16', '14.5', '.5'])('accepts the size %s', (amount) => {
    const draft = withSize(named(), { ...named().size, amount, unit: 'oz' });
    expect(validateProductDraft(draft).size).toBeUndefined();
  });

  it('rejects a pack count that is not a whole number of at least 1', () => {
    const base = named();
    const check = (packCount: string) => validateProductDraft(withSize(base, { ...base.size, packCount })).packCount;
    expect(check('0')).toBe(PACK_COUNT_MINIMUM);
    expect(check('1.5')).toBe(PACK_COUNT_WHOLE);
    expect(check('x')).toBe(PACK_COUNT_WHOLE);
    expect(check('')).toBeUndefined();
    expect(check('6')).toBeUndefined();
  });

  it('does not judge a size nobody touched', () => {
    expect(validateProductDraft(named()).size).toBeUndefined();
  });
});

describe('productDraftInput', () => {
  it('sends the same keys the web resolver sends for a name alone', () => {
    expect(productDraftInput(named(), '0123')).toEqual({
      name: 'Oat flour',
      category: '',
      unitOfMeasure: '',
      barcode: '0123',
    });
  });

  it('adds brand, size, pack and the contribute choice when they were set', () => {
    let draft = { ...named(), brand: ' Mill ' };
    draft = withSize(draft, { amount: '16', unit: 'oz', packageWord: 'bag', packCount: '2' });
    draft = { ...draft, share: { contribute: true, contributeTo: 'openproductsfacts' } };
    expect(productDraftInput(draft, '0123')).toEqual({
      name: 'Oat flour',
      brand: 'Mill',
      category: '',
      unitOfMeasure: 'bag',
      netAmount: 16,
      netUnit: 'oz',
      packCount: 2,
      barcode: '0123',
      contribute: true,
      contributeTo: 'openproductsfacts',
    });
  });

  it('leaves contribute out when it is off', () => {
    const input = productDraftInput({ ...named(), share: { contribute: false, contributeTo: 'openfoodfacts' } }, '1');
    expect('contribute' in input).toBe(false);
    expect('contributeTo' in input).toBe(false);
  });
});

describe('shareNoticeFor', () => {
  const base = { id: 'p', name: 'n', category: '', unitOfMeasure: '', createdAt: '' } as ProductWriteResult;
  it('is null without detail', () => {
    expect(shareNoticeFor(base)).toBeNull();
  });
  it('reads a submitted receipt as success and anything else as a warning', () => {
    expect(shareNoticeFor({ ...base, contribution: { status: 'submitted', detail: 'Sent.' } })).toEqual({
      message: 'Sent.',
      tone: 'submitted',
    });
    expect(shareNoticeFor({ ...base, contribution: { status: 'failed', detail: 'Nope.' } })?.tone).toBe('other');
  });
});

describe('confirmSkipsNeedsName', () => {
  it('counts the scans left for a name and says nothing for none', () => {
    expect(confirmSkipsNeedsName(1)).toBe('Confirm skips the 1 that needs a name.');
    expect(confirmSkipsNeedsName(3)).toBe('Confirm skips the 3 that need a name.');
    expect(confirmSkipsNeedsName(0)).toBeNull();
    expect(confirmSkipsNeedsName(1.5)).toBeNull();
  });
});
