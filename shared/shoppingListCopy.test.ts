import { describe, expect, it } from 'vitest';
import {
  TRIP_SKIP_REMOVAL_LEGACY_LINE,
  collectShoppingListCopyStrings,
  formatStagedCartSkipRemovalLine,
  shoppingListRemoveActionLabel,
  shoppingListRemoveHelp,
  type StagedCartSkipRemovalFields,
} from './shoppingListCopy';

describe('shoppingListRemoveActionLabel', () => {
  it('names the product and says the removal is for this trip only', () => {
    expect(shoppingListRemoveActionLabel('Plain rice')).toBe('Remove Plain rice for this trip');
    expect(shoppingListRemoveActionLabel('  ')).toBe('Remove for this trip');
  });
});

describe('shoppingListRemoveHelp', () => {
  it('mentions this trip and restock target 0', () => {
    expect(shoppingListRemoveHelp).toMatch(/this shopping trip/i);
    expect(shoppingListRemoveHelp).toMatch(/restock target.*0/i);
    expect(collectShoppingListCopyStrings()).toContain(shoppingListRemoveHelp);
  });
});

describe('formatStagedCartSkipRemovalLine', () => {
  const legacy: StagedCartSkipRemovalFields = {};

  it('shows legacy text with no who or when', () => {
    expect(formatStagedCartSkipRemovalLine(legacy)).toBe(TRIP_SKIP_REMOVAL_LEGACY_LINE);
    expect(formatStagedCartSkipRemovalLine({ actorUserId: null, removedAt: null })).toBe(
      TRIP_SKIP_REMOVAL_LEGACY_LINE,
    );
  });

  it('adds who and when when the API sends them', () => {
    expect(
      formatStagedCartSkipRemovalLine({
        actorUserId: 'user-1',
        actorDisplayName: 'Alex',
        removedAt: '2026-11-09T15:00:00Z',
      }, { formatInstant: () => 'Nov 9, 2026' }),
    ).toBe(`${TRIP_SKIP_REMOVAL_LEGACY_LINE} by Alex on Nov 9, 2026`);
  });

  it('maps owner role to household admin wording, not operator', () => {
    const line = formatStagedCartSkipRemovalLine({
      actorUserId: 'user-owner',
      actorRole: 'owner',
      removedAt: '2026-11-09T15:00:00Z',
    }, { formatInstant: () => 'Nov 9, 2026' });
    expect(line).toContain('household admin');
    expect(line).not.toMatch(/operator/i);
  });

  it('shows only who or only when when one field is missing', () => {
    expect(formatStagedCartSkipRemovalLine({ actorDisplayName: 'Sam' })).toBe(
      `${TRIP_SKIP_REMOVAL_LEGACY_LINE} by Sam`,
    );
    expect(
      formatStagedCartSkipRemovalLine({ removedAt: '2026-11-09T15:00:00Z' }, { formatInstant: () => 'Nov 9' }),
    ).toBe(`${TRIP_SKIP_REMOVAL_LEGACY_LINE} on Nov 9`);
  });
});
