import type { OperatorHouseholdView, RestockMode } from '@stockbot/shared/contract';

export const HOUSEHOLD_RESTOCK_CHOICES: { value: RestockMode; label: string }[] = [
  { value: 'auto', label: 'New items restock automatically' },
  { value: 'ask_restock', label: 'Ask before restocking new items' },
  { value: 'ask_hold', label: 'Ask before restocking, and hold until you answer' },
];

const restockLabelByMode = Object.fromEntries(
  HOUSEHOLD_RESTOCK_CHOICES.map((choice) => [choice.value, choice.label]),
) as Record<RestockMode, string>;

export function formatOperatorHouseholdRestockLabel(mode: RestockMode | string): string {
  if (mode in restockLabelByMode) return restockLabelByMode[mode as RestockMode];
  return String(mode).replaceAll('_', ' ');
}

export type OperatorHouseholdStatsInput = Pick<
  OperatorHouseholdView,
  | 'memberCount'
  | 'productCount'
  | 'itemsOnHand'
  | 'pendingScans'
  | 'scannersOnline'
  | 'scannersOffline'
  | 'scansThisWeek'
  | 'lastActivityAt'
>;

function countLabel(count: number, singular: string, plural: string): string {
  return count === 1 ? `1 ${singular}` : `${count} ${plural}`;
}

export function formatOperatorHouseholdInventoryLine(input: OperatorHouseholdStatsInput): string {
  return [
    countLabel(input.memberCount, 'member', 'members'),
    countLabel(input.productCount, 'product', 'products'),
    countLabel(input.itemsOnHand, 'item on hand', 'items on hand'),
  ].join(' · ');
}

/** "online" and "offline" are never pluralized. */
export function formatOperatorHouseholdScannersLine(input: OperatorHouseholdStatsInput): string {
  return `Scanners: ${input.scannersOnline} online · ${input.scannersOffline} offline`;
}

export function formatOperatorHouseholdScansLine(input: OperatorHouseholdStatsInput): string {
  return [
    countLabel(input.scansThisWeek, 'scan this week', 'scans this week'),
    countLabel(input.pendingScans, 'pending scan', 'pending scans'),
  ].join(' · ');
}

export function formatOperatorHouseholdShortDate(date: Date, now: Date, withTime: boolean): string {
  const sameYear = date.getFullYear() === now.getFullYear();
  return date.toLocaleString(undefined, {
    month: 'short',
    day: 'numeric',
    ...(withTime ? { hour: 'numeric', minute: '2-digit' } : {}),
    ...(sameYear ? {} : { year: 'numeric' }),
  });
}

export function formatOperatorHouseholdCreated(at: string, now: Date = new Date()): string {
  const date = new Date(at);
  return `Created ${formatOperatorHouseholdShortDate(date, now, false)}`;
}

export function formatOperatorHouseholdLastActivity(
  at: string | undefined,
  now: Date = new Date(),
): string {
  if (at === undefined || at === '') return 'No activity yet';
  const date = new Date(at);
  return `Last activity ${formatOperatorHouseholdShortDate(date, now, true)}`;
}

export function formatInviteExpiresAt(at: string, now: Date = new Date()): string {
  const date = new Date(at);
  return `Expires ${formatOperatorHouseholdShortDate(date, now, true)}`;
}

export type OperatorHouseholdStatLineTexts = {
  inventory: string;
  scanners: string;
  scans: string;
  lastActivity: string;
};

export function formatOperatorHouseholdStatLines(
  input: OperatorHouseholdStatsInput,
  now: Date = new Date(),
): OperatorHouseholdStatLineTexts {
  return {
    inventory: formatOperatorHouseholdInventoryLine(input),
    scanners: formatOperatorHouseholdScannersLine(input),
    scans: formatOperatorHouseholdScansLine(input),
    lastActivity: formatOperatorHouseholdLastActivity(input.lastActivityAt, now),
  };
}

/** Split a stat line on middle dots for nowrap segments. */
export function splitOperatorStatLine(line: string): string[] {
  return line.split(' · ');
}
