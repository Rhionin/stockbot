import { describe, expect, it } from 'vitest';
import type { MemberPackage } from './copy';
import { clampMonth, formatDuration, seesawView, stockMeter } from './seesaw';

const lemon: MemberPackage = {
  productId: 'lemon',
  name: 'Gatorade Lemon-Lime',
  onHand: 1,
  unitOfMeasure: 'canister',
  netAmount: 18.3,
  netUnit: 'oz',
};

describe('seesawView', () => {
  it('pins the amount and infers weeks from the rate', () => {
    const view = seesawView(
      { quantity: 48, dimension: 'mass', usage: { perMonth: 16, unit: 'oz' } },
      [lemon],
      3,
      null,
    );

    expect(view.caption).toBe('18.3 oz on hand · ≈ 5 wks');
    expect(view.rateLabel).toBe('at 16 oz / mo');
    expect(view.amount).toMatchObject({
      label: '48 oz',
      pinned: true,
      isDefault: false,
      labelText: '48 oz, Keep on hand amount, pinned, 48 ounces',
    });
    expect(view.time).toMatchObject({
      label: '≈ 3 mo',
      pinned: false,
      labelText: '≈ 3 mo, Keep on hand time, about 3 months',
    });
    expect(view.fillQuantity).toBe(48);
    expect(view.unknownHint).toBe('');
    expect(formatDuration(18.3 / 16)).toBe('5 wks');
  });

  it('infers two and a half months from 40 ounces at 16 a month', () => {
    const view = seesawView(
      { quantity: 40, dimension: 'mass', usage: { perMonth: 16, unit: 'oz' } },
      [lemon],
      3,
      null,
    );
    expect(formatDuration(40 / 16)).toBe('2.5 mo');
    expect(view.time).toMatchObject({
      label: '≈ 2.5 mo',
      labelText: '≈ 2.5 mo, Keep on hand time, about 2.5 months',
      pinned: false,
    });
    expect(view.rateLabel).toBe('at 16 oz / mo');
    expect(view.amount.label).toBe('40 oz');
  });

  it('leaves the other end blank until usage is known', () => {
    const amount = seesawView({ quantity: 48, dimension: 'mass' }, [lemon], 3, null);
    expect(amount.caption).toBe('18.3 oz on hand');
    expect(amount.time).toMatchObject({ label: 'Set time', labelText: 'Set time, Keep on hand time, not set' });
    expect(amount.unknownHint).toBe('Fills in once usage is known');
    expect(amount.fillQuantity).toBe(48);

    const time = seesawView({ windowMonths: 6 }, [lemon], 3, null);
    expect(time.time).toMatchObject({
      label: '6 mo',
      pinned: true,
      isDefault: false,
      labelText: '6 mo, Keep on hand time, pinned, 6 months',
    });
    expect(time.amount).toMatchObject({ label: 'Set amount', labelText: 'Set amount, Keep on hand amount, not set' });
    expect(time.fillQuantity).toBeUndefined();
  });

  it('marks the household window as the pinned default', () => {
    const view = seesawView({}, [lemon], 3, null);
    expect(view.time).toMatchObject({
      label: '3 mo',
      pinned: true,
      isDefault: true,
      labelText: '3 mo default, Keep on hand time, pinned, 3 months, household default',
    });
    expect(view.amount).toMatchObject({ label: 'Set amount', labelText: 'Set amount, Keep on hand amount, not set' });
    expect(view.unknownHint).toBe('Set an amount or time');

    const unknown = seesawView({}, [], undefined, null);
    expect(unknown.time).toMatchObject({ label: 'Set time', labelText: 'Set time, Keep on hand time, not set' });
    expect(unknown.amount).toMatchObject({ label: 'Set amount', labelText: 'Set amount, Keep on hand amount, not set' });
    expect(unknown.unknownHint).toBe('Set an amount or time');
  });

  it('recomputes the free end while a draft is open', () => {
    const group = { quantity: 48, dimension: 'mass' as const, usage: { perMonth: 16, unit: 'oz' } };
    const edited = seesawView(group, [lemon], 3, { pin: 'time', value: 6 });
    expect(edited.time).toMatchObject({ label: '6 mo', pinned: true });
    expect(edited.amount).toMatchObject({
      label: '≈ 96 oz',
      labelText: '≈ 96 oz, Keep on hand amount, about 96 ounces',
    });
    expect(edited.fillQuantity).toBe(96);
    expect(clampMonth(18.3 / 16)).toBe(1);
  });

  it('uses fluid ounces when the group is volume', () => {
    const view = seesawView(
      { windowMonths: 2, dimension: 'volume', usage: { perMonth: 8, unit: 'fl oz' } },
      [],
      3,
      null,
    );
    expect(view.amount.label).toBe('≈ 16 fl oz');
    expect(view.amount.labelText).toBe('≈ 16 fl oz, Keep on hand amount, about 16 fluid ounces');
    expect(view.rateLabel).toBe('at 8 fl oz / mo');
    expect(view.time.label).toBe('2 mo');
    expect(view.time.labelText).toBe('2 mo, Keep on hand time, pinned, 2 months');
  });

  it('reads stock against the ounce target and names an empty bin', () => {
    const filled = seesawView({ quantity: 48, dimension: 'mass' }, [lemon], 3, null);
    expect(stockMeter(filled, 18.3)).toEqual({ now: 18.3, max: 48, text: '18.3 oz on hand' });
    expect(stockMeter(filled, 90)).toEqual({ now: 48, max: 48, text: '18.3 oz on hand' });

    const unsized: MemberPackage = {
      productId: 'powder',
      name: 'Gatorade powder',
      onHand: 2,
      unitOfMeasure: 'canister',
    };
    const empty = seesawView({}, [unsized], undefined, null);
    expect(empty.caption).toBe('');
    expect(stockMeter(empty, null)).toEqual({ now: 0, max: 1, text: 'No sizes listed' });
  });
});