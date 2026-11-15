/**
 * CJ-approved #679 skipped-for-this-trip copy (679-skipped-copy-for-cj.md A1–A6, B1–B3).
 * Do not rephrase; tests lock these literals.
 */
import { formatItemStatusRelativeWhen } from './itemStatusWhen';

export const ZERO_FOR_TRIP_STATUS_LABEL = 'Skipped for this trip';

export const ZERO_FOR_TRIP_COMES_BACK = 'It comes back next trip.';

export const ZERO_FOR_TRIP_INLINE_CONFIRM = 'Skipped for this trip. It comes back next trip.';

export const ZERO_FOR_TRIP_CHANGE_AMOUNT_LABEL = 'Change amount';

export const ZERO_FOR_TRIP_NUDGE_PRODUCT = "Don't want it back next trip?";

export const ZERO_FOR_TRIP_NUDGE_GROUP = (groupName: string) =>
  `Don't want the ${groupName.trim()} group back next trip?`;

export const ZERO_FOR_TRIP_UNNAMED_PRODUCT = 'A product with no name';

export const ZERO_FOR_TRIP_NUDGE_OFFLINE = "Couldn't reach the server. Try again.";

export const ZERO_FOR_TRIP_DONT_RESTOCK_BUTTON = "Don't restock";

export const ZERO_FOR_TRIP_DONT_RESTOCK_ERROR = "Couldn't change the restock target. Try again.";

export const ZERO_FOR_TRIP_DONT_RESTOCK_UNDO_DETAIL = 'Its restock target is now 0.';

export function zeroForTripDisplayName(productName: string | undefined | null): string {
  const name = productName?.trim() ?? '';
  return name === '' ? ZERO_FOR_TRIP_UNNAMED_PRODUCT : name;
}

export function zeroForTripDontRestockAriaLabel(productName: string): string {
  const name = productName.trim();
  return name === '' ? ZERO_FOR_TRIP_DONT_RESTOCK_BUTTON : `Don't restock ${name}`;
}

export function zeroForTripDontRestockGroupAriaLabel(groupName: string): string {
  const name = groupName.trim();
  return name === '' ? ZERO_FOR_TRIP_DONT_RESTOCK_BUTTON : `Don't restock the ${name} group`;
}

/** Wire fields for zeroForTrip on off-list rows. */
export type ZeroForTripCopyFields = {
  actorUserId?: string | null;
  actorDisplayName?: string | null;
  /** Household membership role for the actor, when the client resolved it. */
  actorRole?: 'owner' | 'member' | null;
  setAt?: string | null;
  groupName?: string | null;
};

export type FormatZeroForTripCopyOptions = {
  formatWhen?: (iso: string) => string;
  now?: Date;
};

const COMES_BACK = ZERO_FOR_TRIP_COMES_BACK;

function zeroWhoPhrase(fields: ZeroForTripCopyFields): string | null {
  const name = fields.actorDisplayName?.trim();
  if (name) return name;
  if (fields.actorRole === 'owner') return 'A household admin';
  return null;
}

function hasWhen(fields: ZeroForTripCopyFields): boolean {
  return fields.setAt != null && fields.setAt !== '';
}

function formatWhen(fields: ZeroForTripCopyFields, options?: FormatZeroForTripCopyOptions): string | null {
  if (!hasWhen(fields)) return null;
  const format = options?.formatWhen ?? ((iso) => formatItemStatusRelativeWhen(iso, options?.now));
  return format(fields.setAt!);
}

/** Status sentence (A2–A6 and group variants) for a skipped line. */
export function formatZeroForTripStatusLine(
  fields: ZeroForTripCopyFields,
  options?: FormatZeroForTripCopyOptions,
): string {
  const group = fields.groupName?.trim();
  const who = zeroWhoPhrase(fields);
  const when = formatWhen(fields, options);

  if (group) {
    if (who && when) {
      return `${who} skipped the ${group} group for this trip ${when}. ${COMES_BACK}`;
    }
    if (who) {
      return `${who} skipped the ${group} group for this trip. ${COMES_BACK}`;
    }
    if (when) {
      return `Skipped the ${group} group for this trip ${when}. ${COMES_BACK}`;
    }
    return `Skipped the ${group} group for this trip. ${COMES_BACK}`;
  }

  if (who && when) {
    return `${who} skipped it for this trip ${when}. ${COMES_BACK}`;
  }
  if (who) {
    return `${who} skipped it for this trip. ${COMES_BACK}`;
  }
  if (when) {
    return `Skipped for this trip ${when}. ${COMES_BACK}`;
  }
  return ZERO_FOR_TRIP_INLINE_CONFIRM;
}

/** Tolerant parser when zeroForTrip is missing fields (AC9). */
export function safeFormatZeroForTripStatusLine(
  fields: ZeroForTripCopyFields | null | undefined,
  options?: FormatZeroForTripCopyOptions,
): string {
  if (fields == null || typeof fields !== 'object') {
    return ZERO_FOR_TRIP_INLINE_CONFIRM;
  }
  try {
    return formatZeroForTripStatusLine(fields, options);
  } catch {
    return ZERO_FOR_TRIP_INLINE_CONFIRM;
  }
}