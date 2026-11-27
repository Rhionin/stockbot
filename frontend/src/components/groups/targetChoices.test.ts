import { describe, expect, it } from 'vitest';
import { targetChoices } from './copy';

describe('K1 join prompt choices', () => {
  it('offers Keep n items instead of ounce conversion', () => {
    const choices = targetChoices(
      null,
      [{ productId: 'a', name: 'Cracker A', quantity: 4 }],
    );
    expect(choices.some((row) => row.label === 'Keep 4 items (from Cracker A)')).toBe(true);
    expect(choices.find((row) => row.label === 'Keep 4 items (from Cracker A)')?.target).toEqual({
      kind: 'items',
      value: 4,
    });
  });
});