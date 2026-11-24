import type { ScanDirection } from './types';
import { formatReviewCount } from './queueUtils';

export const QUEUE_TITLE = 'Scan queue';

export const DIRECTION_TABS_LABEL = 'Scan direction';

export const DIRECTION_TAB_LABEL: Record<ScanDirection, string> = {
  stock_in: 'Stock in',
  stock_out: 'Stock out',
};

export const DIRECTION_HINT: Record<ScanDirection, string> = {
  stock_in: 'Scans add to stock',
  stock_out: 'Scans remove from stock',
};

export const SCANNER_CONNECTED = 'Scanner connected';
export const SCANNER_DISCONNECTED = "Scanner disconnected. You can scan with this device's camera instead.";

export const SCAN_MODE_FAILURE = 'The scan mode did not change.';

// The visible count is a compact badge. Assistive tech reads it as part of the
// tab's name, and the name keeps the label and the badge text in the order the
// eye reads them so it still contains what is on screen.
export function directionTabName(label: string, count: number): string {
  return count <= 0 ? label : `${label} ${formatReviewCount(count)} to review`;
}
