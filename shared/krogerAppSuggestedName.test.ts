import { describe, expect, it } from 'vitest';
import { householdPantrySuffix, suggestedKrogerAppName } from './krogerAppSuggestedName';

describe('T51 suggested Kroger app name', () => {
  it('T51 strips Kroger from the household name and adds a stable suffix', async () => {
    const suffix = await householdPantrySuffix('household-alpha');
    expect(suffix).toHaveLength(4);
    expect(suffix).toMatch(/^[23456789ABCDEFGHJKLMNPQRSTUVWXYZ]{4}$/);
    const name = await suggestedKrogerAppName('Kroger Family', 'household-alpha');
    expect(name.toLowerCase()).not.toContain('kroger');
    expect(name).toContain(suffix);
  });

  it('T51 falls back to Our pantry when the name is only Kroger', async () => {
    const name = await suggestedKrogerAppName('Kroger', 'hh-empty');
    expect(name).toMatch(/^Our pantry [23456789ABCDEFGHJKLMNPQRSTUVWXYZ]{4}$/);
  });
});
