import {
  TRIP_SKIP_REMOVAL_LEGACY_LINE,
  formatStagedCartSkipRemovalLine,
  shoppingListRemoveActionLabel,
  shoppingListRemoveHelp,
} from '@stockbot/shared/shoppingListCopy';

describe('shopping list remove copy parity (W1)', () => {
  it('matches the web Remove action label', () => {
    expect(shoppingListRemoveActionLabel('Oats')).toBe('Remove Oats for this trip');
  });

  it('matches the shared help sentence', () => {
    expect(shoppingListRemoveHelp).toMatch(/this shopping trip/i);
  });

  it('matches legacy and present-field trip skip lines', () => {
    expect(formatStagedCartSkipRemovalLine({})).toBe(TRIP_SKIP_REMOVAL_LEGACY_LINE);
    expect(
      formatStagedCartSkipRemovalLine({
        actorDisplayName: 'Sam',
        removedAt: '2026-11-09T12:00:00Z',
      }, { formatInstant: () => 'Nov 9' }),
    ).toBe(`${TRIP_SKIP_REMOVAL_LEGACY_LINE} by Sam on Nov 9`);
  });
});
