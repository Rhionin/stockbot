/**
 * Shopping-list Remove (trip skip) copy shared by web and mobile.
 * Field names match the W1 staged_cart_skips columns (actorUserId, removedAt);
 * confirm against the backend PR when it merges.
 */

/** Optional actor/time on a one-trip staged_cart_skips row (#571 W1). */
export type StagedCartSkipRemovalFields = {
  actorUserId?: string | null;
  /** When the API sends a display name; omit for legacy rows. */
  actorDisplayName?: string | null;
  /** Household membership role for the actor; owner is shown as "admin" in copy. */
  actorRole?: 'owner' | 'member' | null;
  removedAt?: string | null;
};

export const TRIP_SKIP_REMOVAL_LEGACY_LINE = 'removed for this trip';

/** Visible label and accessible name for the shopping-list Remove control. */
export function shoppingListRemoveActionLabel(productName: string): string {
  const name = productName.trim();
  return name === '' ? 'Remove for this trip' : `Remove ${name} for this trip`;
}

/** Describes what Remove does; use on tooltips and help text. */
export const shoppingListRemoveHelp =
  'Keeps this item off the list for this shopping trip only. To stop restocking it, set the restock target to 0.';

export type FormatStagedCartSkipRemovalOptions = {
  formatInstant?: (iso: string) => string;
};

function removalActorPhrase(fields: StagedCartSkipRemovalFields): string | null {
  const name = fields.actorDisplayName?.trim();
  if (name) return name;
  if (fields.actorUserId) {
    if (fields.actorRole === 'owner') return 'a household admin';
    return 'someone in your household';
  }
  return null;
}

function isLegacyTripSkipRemoval(fields: StagedCartSkipRemovalFields): boolean {
  const hasActor = fields.actorUserId != null && fields.actorUserId !== '';
  const hasTime = fields.removedAt != null && fields.removedAt !== '';
  const hasName = fields.actorDisplayName != null && fields.actorDisplayName.trim() !== '';
  return !hasActor && !hasTime && !hasName;
}

/** Line shown when an item was taken off the list for the current trip (legacy or with who/when). */
export function formatStagedCartSkipRemovalLine(
  fields: StagedCartSkipRemovalFields,
  options?: FormatStagedCartSkipRemovalOptions,
): string {
  if (isLegacyTripSkipRemoval(fields)) {
    return TRIP_SKIP_REMOVAL_LEGACY_LINE;
  }

  const who = removalActorPhrase(fields);
  const when =
    fields.removedAt != null && fields.removedAt !== ''
      ? (options?.formatInstant?.(fields.removedAt) ?? fields.removedAt)
      : null;

  if (who && when) return `${TRIP_SKIP_REMOVAL_LEGACY_LINE} by ${who} on ${when}`;
  if (who) return `${TRIP_SKIP_REMOVAL_LEGACY_LINE} by ${who}`;
  if (when) return `${TRIP_SKIP_REMOVAL_LEGACY_LINE} on ${when}`;
  return TRIP_SKIP_REMOVAL_LEGACY_LINE;
}

export function collectShoppingListCopyStrings(): string[] {
  return [shoppingListRemoveHelp, TRIP_SKIP_REMOVAL_LEGACY_LINE];
}
