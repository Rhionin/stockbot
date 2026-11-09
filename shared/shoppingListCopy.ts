/**
 * Shopping-list Remove (trip skip) copy shared by web and mobile (#571 W1).
 * Field names match staged_cart_skips (actorUserId, removedAt); confirm with backend PR.
 */

/** Optional actor/time on a one-trip staged_cart_skips row (#571 W1). */
export type StagedCartSkipRemovalFields = {
  actorUserId?: string | null;
  /** First name when the API sends one. */
  actorDisplayName?: string | null;
  actorRole?: 'owner' | 'member' | null;
  removedAt?: string | null;
  /** Set when the skip applies to a product-group line. */
  groupName?: string | null;
};

export const SHOPPING_LIST_REMOVE_VISIBLE_LABEL = 'Remove for this trip';

export const SHOPPING_LIST_START_NEW_CART_NOTE =
  'This forgets what was already sent, clears checked-off lines, and brings back items removed for this trip.';

export const SHOPPING_LIST_DONT_RESTOCK_MENU_HINT =
  'Stays off the list until you change its restock target.';

export const RESTOCK_DONT_UNDO_DETAIL = 'Its restock target is now 0.';

const COMES_BACK = 'It comes back next trip.';

/** Accessible name for a product line Remove control. */
export function shoppingListRemoveProductAriaLabel(productName: string): string {
  const name = productName.trim();
  return name === '' ? SHOPPING_LIST_REMOVE_VISIBLE_LABEL : `Remove ${name} for this trip`;
}

/** Accessible name when the staged row is a group line. */
export function shoppingListRemoveGroupAriaLabel(groupName: string): string {
  const name = groupName.trim();
  return name === '' ? SHOPPING_LIST_REMOVE_VISIBLE_LABEL : `Remove the ${name} group for this trip`;
}

export function shoppingListAfterRemoveProductMessage(productName: string): string {
  const name = productName.trim() || 'This item';
  return `${name} is off the list for this trip. ${COMES_BACK}`;
}

export function shoppingListAfterRemoveGroupMessage(groupName: string): string {
  const name = groupName.trim() || 'This group';
  return `The ${name} group is off the list for this trip. ${COMES_BACK}`;
}

function sameCalendarDay(left: Date, right: Date): boolean {
  return left.getFullYear() === right.getFullYear()
    && left.getMonth() === right.getMonth()
    && left.getDate() === right.getDate();
}

/** #570-style relative when for item status (today / yesterday / Tue / Nov 3). */
export function formatItemStatusRelativeWhen(iso: string, now: Date = new Date()): string {
  const at = new Date(iso);
  if (Number.isNaN(at.getTime())) return iso;
  if (sameCalendarDay(at, now)) return 'today';
  const yesterday = new Date(now);
  yesterday.setDate(now.getDate() - 1);
  if (sameCalendarDay(at, yesterday)) return 'yesterday';
  const msPerDay = 86_400_000;
  const diffDays = Math.floor((now.getTime() - at.getTime()) / msPerDay);
  if (diffDays >= 0 && diffDays < 7) {
    return new Intl.DateTimeFormat('en-US', { weekday: 'short' }).format(at);
  }
  if (at.getFullYear() === now.getFullYear()) {
    return new Intl.DateTimeFormat('en-US', { month: 'short', day: 'numeric' }).format(at);
  }
  return new Intl.DateTimeFormat('en-US', { month: 'short', day: 'numeric', year: 'numeric' }).format(at);
}

export type FormatStagedCartSkipRemovalOptions = {
  formatWhen?: (iso: string) => string;
  now?: Date;
};

function skipWhoPhrase(fields: StagedCartSkipRemovalFields): string | null {
  const name = fields.actorDisplayName?.trim();
  if (name) return name;
  if (fields.actorRole === 'owner') return 'A household admin';
  return null;
}

function isLegacyTripSkipRemoval(fields: StagedCartSkipRemovalFields): boolean {
  const hasActor = (fields.actorUserId != null && fields.actorUserId !== '')
    || (fields.actorDisplayName != null && fields.actorDisplayName.trim() !== '');
  const hasTime = fields.removedAt != null && fields.removedAt !== '';
  return !hasActor && !hasTime;
}

function formatWhen(fields: StagedCartSkipRemovalFields, options?: FormatStagedCartSkipRemovalOptions): string | null {
  if (fields.removedAt == null || fields.removedAt === '') return null;
  const format = options?.formatWhen ?? ((iso) => formatItemStatusRelativeWhen(iso, options?.now));
  return format(fields.removedAt);
}

/** Item / group status sentence for an off-list-for-this-trip skip (#570 / W6). */
export function formatStagedCartSkipRemovalLine(
  fields: StagedCartSkipRemovalFields,
  options?: FormatStagedCartSkipRemovalOptions,
): string {
  const group = fields.groupName?.trim();
  const who = skipWhoPhrase(fields);
  const when = formatWhen(fields, options);

  if (group) {
    if (who && when) return `${who} removed the ${group} group for this trip ${when}. ${COMES_BACK}`;
    if (who) return `${who} removed the ${group} group for this trip. ${COMES_BACK}`;
    if (when) return `Removed the ${group} group for this trip ${when}. ${COMES_BACK}`;
    return `Removed the ${group} group for this trip. ${COMES_BACK}`;
  }

  if (isLegacyTripSkipRemoval(fields)) {
    return `Removed for this trip. ${COMES_BACK}`;
  }

  if (who && when) return `${who} removed it for this trip ${when}. ${COMES_BACK}`;
  if (who) return `${who} removed it for this trip. ${COMES_BACK}`;
  if (when) return `Removed for this trip ${when}. ${COMES_BACK}`;
  return `Removed for this trip. ${COMES_BACK}`;
}

export function collectShoppingListCopyStrings(): string[] {
  return [
    SHOPPING_LIST_REMOVE_VISIBLE_LABEL,
    SHOPPING_LIST_START_NEW_CART_NOTE,
    SHOPPING_LIST_DONT_RESTOCK_MENU_HINT,
    RESTOCK_DONT_UNDO_DETAIL,
    shoppingListAfterRemoveProductMessage('Product'),
    shoppingListAfterRemoveGroupMessage('Group'),
    formatStagedCartSkipRemovalLine({}),
  ];
}

/** @deprecated Use shoppingListRemoveProductAriaLabel */
export function shoppingListRemoveActionLabel(productName: string): string {
  return shoppingListRemoveProductAriaLabel(productName);
}
