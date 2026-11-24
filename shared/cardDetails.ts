import type { ItemInstanceWithStatus } from './types';
import { expiryDateToISOString, formatExpiryDate, isValidUnitCount } from './queueUtils';

// A zero timestamp clears expires_at. JSON null leaves the stored date unchanged.
export const CLEARED_EXPIRY_ISO = '0001-01-01T00:00:00.000Z';

// A burst of plus or minus taps updates the number immediately and saves the
// latest count once the taps pause.
export const UNIT_COUNT_SAVE_DELAY_MS = 200;

export const UNIT_COUNT_LABEL = 'Unit count';
export const UNIT_COUNT_DECREASE_LABEL = 'Decrease unit count';
export const UNIT_COUNT_INCREASE_LABEL = 'Increase unit count';
export const UNIT_COUNT_SAVE_FAILURE = 'Unable to update unit count.';
export const UNIT_COUNT_INVALID = 'Enter a whole number, 1 or more.';

export const EXPIRY_LABEL = 'Expiration date';
export const EXPIRY_ADD_LABEL = 'Add expiration';
export const EXPIRY_CHANGE_LABEL = 'Change expiration';
export const EXPIRY_CLEAR_LABEL = 'Clear expiration';
export const EXPIRY_CANCEL_LABEL = 'Cancel expiration';
export const EXPIRY_SAVE_FAILURE = 'Unable to update expiration date.';
export const EXPIRY_INVALID = 'Enter a real date as YYYY-MM-DD.';

export const INSTANCE_PICKER_LABEL = 'Instance to remove';
export const INSTANCE_PICKER_HINT = 'Instances are ordered by earliest expiration date.';
export const INSTANCE_AUTOMATIC_LABEL = 'Use oldest available automatically';
export const INSTANCES_LOADING = 'Loading item instances';
export const INSTANCES_NONE = 'No available instances.';
export const INSTANCES_LOAD_FAILURE = 'Unable to load instances.';

export const expiryDatePart = (expiresAt: string | null): string =>
  expiresAt === null ? '' : expiresAt.substring(0, 10);

export const formatExpirySummary = (datePart: string): string =>
  `Expires ${formatExpiryDate(`${datePart}T00:00:00.000Z`)}`;

export const formatInstanceLabel = (instance: ItemInstanceWithStatus): string =>
  instance.expiresAt === null ? 'No expiration date' : `Expires ${formatExpiryDate(instance.expiresAt)}`;

// A refresh can echo a count the user has already moved past. Applying that
// echo snaps the stepper backward and shifts the control under their finger.
export const shouldApplyServerUnitCount = (
  draft: number,
  confirmed: number,
  incoming: number,
): boolean => !(draft !== confirmed && incoming !== draft);

// Whole number of 1 or more. Typed text such as "2.5", "1e1" or " " is not a count.
export const parseUnitCountText = (text: string): number | null => {
  if (!/^\d+$/.test(text.trim())) return null;
  const value = Number(text.trim());
  return isValidUnitCount(value) ? value : null;
};

// Only real calendar days. Date alone would roll 2026-02-30 into March.
export const isRealExpiryDate = (text: string): boolean => {
  const match = /^(\d{4})-(\d{2})-(\d{2})$/.exec(text);
  if (match === null) return false;
  const [year, month, day] = [Number(match[1]), Number(match[2]), Number(match[3])];
  if (year < 1) return false;
  const parsed = new Date(Date.UTC(year, month - 1, day));
  return parsed.getUTCFullYear() === year && parsed.getUTCMonth() === month - 1 && parsed.getUTCDate() === day;
};

export type ExpiryParse = { kind: 'clear' } | { kind: 'date'; iso: string } | { kind: 'invalid' };

// The PATCH value for an expiry draft. Empty clears. Never null.
export const parseExpiryDraft = (draft: string): ExpiryParse => {
  const text = draft.trim();
  if (text === '') return { kind: 'clear' };
  if (!isRealExpiryDate(text)) return { kind: 'invalid' };
  const iso = expiryDateToISOString(text);
  return iso === undefined ? { kind: 'invalid' } : { kind: 'date', iso };
};
