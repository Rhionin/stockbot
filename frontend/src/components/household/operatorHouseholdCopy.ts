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
>;

function countLabel(count: number, singular: string, plural: string): string {
  return count === 1 ? `1 ${singular}` : `${count} ${plural}`;
}

/** User-visible stat fragments for the admin households list (counts only, no PII). */
export function formatOperatorHouseholdStatsParts(input: OperatorHouseholdStatsInput): string[] {
  return [
    countLabel(input.memberCount, 'member', 'members'),
    countLabel(input.productCount, 'product', 'products'),
    countLabel(input.itemsOnHand, 'item on hand', 'items on hand'),
    countLabel(input.scannersOnline, 'scanner online', 'scanners online'),
    countLabel(input.scannersOffline, 'scanner offline', 'scanners offline'),
    countLabel(input.scansThisWeek, 'scan this week', 'scans this week'),
    countLabel(input.pendingScans, 'pending scan', 'pending scans'),
  ];
}

export function formatOperatorHouseholdStatsLine(input: OperatorHouseholdStatsInput): string {
  return formatOperatorHouseholdStatsParts(input).join(' · ');
}
