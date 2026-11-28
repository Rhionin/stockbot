import { describe, expect, it } from 'vitest';
import { binView, conflictLine, detailStockSentence, listSummary, memberLine, ruleChoices, rulePillLabel, ruleSentence, targetChoices } from './copy';
import { goalTarget, syntheticGroup } from './syntheticGroup';

describe('targetChoices', () => {
  it('keeps the group window and one choice per distinct member setting', () => {
    const choices = targetChoices(
      { target: goalTarget({ kind: 'months', value: 6, unit: 'months', status: 'ok' }) },
      [
        { productId: 'glacier', name: 'Gatorade Glacier Freeze', quantity: 4, amount: 48, dimension: 'mass' },
        { productId: 'lemon', name: 'Lemonade powder', windowMonths: 3 },
        { productId: 'other', name: 'Other powder', quantity: 4, amount: 48, dimension: 'mass' },
        { productId: 'tea', name: 'Loose tea', quantity: 4 },
      ],
    );

    expect(choices.map((choice) => choice.label)).toEqual([
      "Keep the group's 6 months",
      'Use 48 ounces (from Gatorade Glacier Freeze, Other powder)',
      'Use 3 months (from Lemonade powder)',
      'Keep 4 items (from Loose tea)',
    ]);
    expect(choices[0]?.target).toEqual({ kind: 'months', value: 6 });
    expect(choices[1]?.target).toEqual({ kind: 'amount', value: 48 });
    expect(choices[2]?.target).toEqual({ kind: 'months', value: 3 });
    expect(conflictLine({ productId: 'glacier', name: 'Gatorade Glacier Freeze', quantity: 4, amount: 48, dimension: 'mass' }))
      .toBe('Gatorade Glacier Freeze: 4 on hand (48 ounces)');
    expect(conflictLine({ productId: 'tea', name: 'Loose tea', quantity: 4 }))
      .toBe('Loose tea: 4 items on its own');
    expect(conflictLine({ productId: 'lemon', name: 'Lemonade powder', windowMonths: 3 }))
      .toBe('Lemonade powder: 3 months');
  });

  it('uses the household default only when the group has no target', () => {
    const choices = targetChoices(null, [
      { productId: 'glacier', name: 'Gatorade Glacier Freeze', quantity: 4, amount: 12, dimension: 'volume' },
    ]);

    expect(choices[0]).toEqual({ label: 'Keep the household default', target: { clear: true } });
    expect(choices[1]).toEqual({
      label: 'Use 12 fluid ounces (from Gatorade Glacier Freeze)',
      target: { kind: 'amount', value: 12 },
    });
  });

  it('sends the group quantity and its dimension', () => {
    const choices = targetChoices({
      target: goalTarget({ kind: 'amount', value: 24, unit: 'fl oz', status: 'ok' }),
    }, []);
    expect(choices).toEqual([
      { label: "Keep the group's 24 fl oz", target: { kind: 'amount', value: 24 } },
    ]);
  });
});

const lemon = {
  productId: 'lemon',
  name: 'Gatorade Lemon-Lime',
  onHand: 1,
  unitOfMeasure: 'canister',
  netAmount: 18.3,
  netUnit: 'oz',
};
const glacier = {
  productId: 'glacier',
  name: 'Gatorade Glacier Freeze',
  onHand: 0,
  unitOfMeasure: 'canister',
  netAmount: 50.9,
  netUnit: 'oz',
};

describe('bin sentences', () => {
  it('fills an ounce bin and says what is in it', () => {
    const group = { quantity: 48, dimension: 'mass' as const, rule: 'same_as_ran_out', ruleConfirmed: true, members: [] };
    const view = binView(group, [lemon, glacier]);
    expect(view.corner).toBe('48 oz');
    expect(view.level).toBe('18.3 oz');
    expect(view.segments).toEqual([{ productId: 'lemon', fraction: 18.3 / 48 }]);
    expect(detailStockSentence(group, [lemon, glacier])).toBe(
      '18.3 ounces of Gatorade Lemon-Lime are in the bin. Keep 48 ounces on hand.',
    );
    expect(ruleSentence(group, [lemon], 'detail')).toBe('Rebuy the exact product you used last.');
    expect(ruleSentence(group, [lemon], 'brief')).toBe('Rebuy the exact product you used last.');
    expect(memberLine(glacier)).toBe('50.9 oz canister, none on hand');
  });

  it('leaves the bin empty when the target is months or the household default', () => {
    const months = binView({ windowMonths: 3 }, [{ ...lemon, onHand: 2, unitOfMeasure: 'jar' }]);
    expect(months.segments).toEqual([]);
    expect(months.corner).toBe('3 months');
    expect(detailStockSentence({ windowMonths: 3 }, [{ ...lemon, onHand: 2, unitOfMeasure: 'jar' }])).toBe(
      '2 jars on hand. Keep 3 months.',
    );

    const household = binView({}, [{ productId: 'oat', name: 'Oat milk', onHand: 1, unitOfMeasure: 'carton' }]);
    expect(household.segments).toEqual([]);
    expect(household.corner).toBe('Default');
    expect(listSummary(
      syntheticGroup({
        id: 'oat',
        name: 'Oat milk',
        members: [],
      }),
      [{ productId: 'oat', name: 'Oat milk', onHand: 1, unitOfMeasure: 'carton' }],
      3,
    )).toBe('1 carton on hand. Household default, 3 months. Still using Same product until you pick a rule.');
  });

  it('names the pinned product in the rule sentence', () => {
    const members = [{ productId: 'creamy', name: 'Creamy peanut butter' }];
    expect(ruleSentence({ rule: 'favorite', ruleConfirmed: true, pinnedProductId: 'creamy' }, members, 'detail'))
      .toBe('Always buy Creamy peanut butter.');
    expect(ruleSentence({ rule: 'best_deal', ruleConfirmed: true, pinnedProductId: 'creamy' }, members, 'brief'))
      .toBe('Lowest price per ounce, or Creamy peanut butter if no price is known.');
    expect(ruleSentence({ rule: 'best_deal', ruleConfirmed: true }, [], 'detail'))
      .toBe('Lowest price per ounce.');
    expect(ruleSentence({ rule: 'favor_variety', ruleConfirmed: true }, [], 'detail'))
      .toBe("Rotate to the product you've had least recently.");
    expect(ruleSentence({ rule: 'favor_variety', ruleConfirmed: true }, [], 'brief'))
      .toBe("Rotate to the product you've had least recently.");
  });

  it('matches the restock picker to the pill words', () => {
    expect(ruleChoices).toEqual([
      { id: 'same_as_ran_out', label: 'Same product', hint: 'Rebuy the exact product you used last.' },
      { id: 'favorite', label: 'Favorite', hint: 'Always buy one product.' },
      { id: 'best_deal', label: 'Best deal', hint: 'Lowest price per ounce.' },
      { id: 'favor_variety', label: 'Favor variety', hint: "Rotate to the product you've had least recently." },
    ]);
    expect(rulePillLabel({ rule: 'same_as_ran_out', ruleConfirmed: true }, [])).toBe(ruleChoices[0].label);
    expect(rulePillLabel({ rule: 'favorite', ruleConfirmed: true }, [])).toBe(ruleChoices[1].label);
    expect(rulePillLabel({ rule: 'best_deal', ruleConfirmed: true }, [])).toBe(ruleChoices[2].label);
    expect(rulePillLabel({ rule: 'favor_variety', ruleConfirmed: true }, [])).toBe('Favor variety');
  });

  it('does not fill the bin when a stocked package has no size', () => {
    const members = [{ productId: 'lemon', name: 'Gatorade Lemon-Lime', onHand: 1, unitOfMeasure: 'canister' }];
    const view = binView({ quantity: 48, dimension: 'mass' }, members);
    expect(view.segments).toEqual([]);
    expect(detailStockSentence({ quantity: 48, dimension: 'mass' }, members)).toBe(
      '1 canister on hand. Keep 48 ounces on hand. Package sizes are not listed, so the bin stays empty.',
    );
  });
});
