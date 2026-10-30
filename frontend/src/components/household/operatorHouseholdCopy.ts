import type { OperatorHouseholdView } from '@stockbot/shared/contract';

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

export function formatOperatorHouseholdLastActivity(
  at: string | undefined,
  now: Date = new Date(),
): string {
  if (at === undefined || at === '') return 'No activity yet';
  const date = new Date(at);
  const sameYear = date.getFullYear() === now.getFullYear();
  const when = date.toLocaleString(undefined, {
    month: 'short',
    day: 'numeric',
    hour: 'numeric',
    minute: '2-digit',
    ...(sameYear ? {} : { year: 'numeric' }),
  });
  return `Last activity ${when}`;
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
