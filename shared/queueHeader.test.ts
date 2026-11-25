import { describe, expect, it } from 'vitest';
import { directionTabName } from './queueHeader';

describe('directionTabName', () => {
  it('leaves the label alone when nothing is waiting', () => {
    expect(directionTabName('Stock in', 0)).toBe('Stock in');
  });

  it('puts the count in the name', () => {
    expect(directionTabName('Stock out', 3)).toBe('Stock out 3 to review');
    expect(directionTabName('Stock out', 99)).toBe('Stock out 99 to review');
  });

  it('keeps the capped badge text above 99', () => {
    expect(directionTabName('Stock in', 100)).toBe('Stock in 99+ to review');
  });
});
