import { describe, expect, it } from 'vitest';
import {
  emptyNetSizeDraft,
  sizeUnits,
  sizeWrite,
  type NetSizeDraft,
} from './netSize';

const draft = (overrides: Partial<NetSizeDraft> = {}): NetSizeDraft => ({
  amount: '16',
  unit: 'oz',
  packageWord: 'can',
  packCount: '6',
  ...overrides,
});

describe('sizeWrite', () => {
  describe('untouched size and pack', () => {
    it('sends only unitOfMeasure with no net or pack keys', () => {
      const result = sizeWrite(draft(), { size: false, pack: false });

      expect(result).toEqual({ unitOfMeasure: 'can' });
      expect('netAmount' in result).toBe(false);
      expect('netUnit' in result).toBe(false);
      expect('packCount' in result).toBe(false);
    });

    it('ignores draft amount, unit, and packCount when nothing was touched', () => {
      const result = sizeWrite(
        draft({ amount: '99', unit: 'kg', packCount: '12', packageWord: 'jar' }),
        { size: false, pack: false },
      );

      expect(result).toEqual({ unitOfMeasure: 'jar' });
      expect('netAmount' in result).toBe(false);
      expect('netUnit' in result).toBe(false);
      expect('packCount' in result).toBe(false);
    });
  });

  describe('touched size', () => {
    it('sends a numeric netAmount and netUnit for a trimmed integer amount', () => {
      const result = sizeWrite(
        draft({ amount: '  12  ', unit: 'fl oz' }),
        { size: true, pack: false },
      );

      expect(result.unitOfMeasure).toBe('can');
      expect(result.netAmount).toBe(12);
      expect(result.netUnit).toBe('fl oz');
      expect('packCount' in result).toBe(false);
    });

    it('parses decimal amounts', () => {
      const result = sizeWrite(
        draft({ amount: '15.5', unit: 'L' }),
        { size: true, pack: false },
      );

      expect(result.netAmount).toBe(15.5);
      expect(result.netUnit).toBe('L');
    });

    it('sends netAmount null and still includes netUnit for an empty amount', () => {
      const result = sizeWrite(
        draft({ amount: '', unit: 'g' }),
        { size: true, pack: false },
      );

      expect(result.netAmount).toBe(null);
      expect(result.netUnit).toBe('g');
      expect('netUnit' in result).toBe(true);
    });

    it('sends netAmount null and netUnit for whitespace-only amount', () => {
      const result = sizeWrite(
        draft({ amount: '   ', unit: 'ml' }),
        { size: true, pack: false },
      );

      expect(result.netAmount).toBe(null);
      expect(result.netUnit).toBe('ml');
    });

    it('includes netUnit as an empty string when the draft unit is blank', () => {
      const result = sizeWrite(
        draft({ amount: '', unit: '' }),
        { size: true, pack: false },
      );

      expect(result.netAmount).toBe(null);
      expect(result.netUnit).toBe('');
    });
  });

  describe('touched pack', () => {
    it('sends a numeric packCount for a trimmed count', () => {
      const result = sizeWrite(
        draft({ packCount: '  4  ' }),
        { size: false, pack: true },
      );

      expect(result).toEqual({ unitOfMeasure: 'can', packCount: 4 });
      expect('netAmount' in result).toBe(false);
      expect('netUnit' in result).toBe(false);
    });

    it('sends packCount null for an empty pack count', () => {
      const result = sizeWrite(
        draft({ packCount: '' }),
        { size: false, pack: true },
      );

      expect(result.packCount).toBe(null);
      expect('netAmount' in result).toBe(false);
    });

    it('sends packCount null for whitespace-only pack count', () => {
      const result = sizeWrite(
        draft({ packCount: '  \t  ' }),
        { size: false, pack: true },
      );

      expect(result.packCount).toBe(null);
    });
  });

  describe('independent and combined touch flags', () => {
    it('includes only size fields when sizeTouched and not packTouched', () => {
      const result = sizeWrite(
        draft({ amount: '8', unit: 'oz', packCount: '99' }),
        { size: true, pack: false },
      );

      expect(result.netAmount).toBe(8);
      expect(result.netUnit).toBe('oz');
      expect('packCount' in result).toBe(false);
    });

    it('includes only packCount when packTouched and not sizeTouched', () => {
      const result = sizeWrite(
        draft({ amount: '8', unit: 'oz', packCount: '3' }),
        { size: false, pack: true },
      );

      expect(result.packCount).toBe(3);
      expect('netAmount' in result).toBe(false);
      expect('netUnit' in result).toBe(false);
    });

    it('includes size and pack fields when both are touched', () => {
      const result = sizeWrite(
        draft({ amount: '500', unit: 'ml', packCount: '2', packageWord: 'bottle' }),
        { size: true, pack: true },
      );

      expect(result).toEqual({
        unitOfMeasure: 'bottle',
        netAmount: 500,
        netUnit: 'ml',
        packCount: 2,
      });
    });
  });

  describe('non-numeric input', () => {
    it('produces NaN netAmount when size is touched with non-numeric text', () => {
      const result = sizeWrite(
        draft({ amount: 'abc', unit: 'oz' }),
        { size: true, pack: false },
      );

      expect(result.netAmount).toBeNaN();
      expect(result.netUnit).toBe('oz');
    });

    it('produces NaN packCount when pack is touched with non-numeric text', () => {
      const result = sizeWrite(
        draft({ packCount: 'abc' }),
        { size: false, pack: true },
      );

      expect(result.packCount).toBeNaN();
    });
  });
});

describe('emptyNetSizeDraft', () => {
  it('returns the expected initial field values', () => {
    expect(emptyNetSizeDraft()).toEqual({
      amount: '',
      unit: '',
      packageWord: '',
      packCount: '',
    });
  });

  it('returns a fresh object on each call', () => {
    const first = emptyNetSizeDraft();
    const second = emptyNetSizeDraft();

    expect(first).not.toBe(second);
    expect(first).toEqual(second);
  });

  it('does not leak mutations into the next call', () => {
    const first = emptyNetSizeDraft();
    first.amount = 'mutated';
    first.packageWord = 'box';

    expect(emptyNetSizeDraft()).toEqual({
      amount: '',
      unit: '',
      packageWord: '',
      packCount: '',
    });
  });
});

describe('sizeUnits', () => {
  it('exposes the unit options NetSizeFields uses in the Select', () => {
    expect(sizeUnits.map((entry) => entry.value)).toEqual([
      'oz',
      'fl oz',
      'lb',
      'g',
      'kg',
      'ml',
      'L',
    ]);
  });
});
