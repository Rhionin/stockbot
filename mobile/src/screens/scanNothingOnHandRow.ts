import { scanReportsNothingOnHand, type ScanListEntry } from '@stockbot/shared/contract';

/** Mobile queue amber (stock_out + productId); pin precedes GET /api/scans onHand. */
export function mobileNothingOnHandCue(entry: ScanListEntry, pinned: boolean): boolean {
  if (entry.direction !== 'stock_out' || entry.productId === null) return false;
  if (pinned) return true;
  return scanReportsNothingOnHand(entry);
}

/** Web queue amber (pending + stock_out + productId); preserved baseline for parity tests. */
export function webNothingOnHandCue(entry: ScanListEntry, pinned: boolean): boolean {
  if (entry.status !== 'pending' || entry.direction !== 'stock_out' || entry.productId === null) return false;
  if (pinned) return true;
  return scanReportsNothingOnHand(entry);
}
