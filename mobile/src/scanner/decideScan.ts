import { RESCAN_COOLDOWN_MS, shouldAcceptScan, type PriorScan } from '../../../frontend/src/components/scanner/scanTiming';
import type { ScanDirection, ScannerConfig } from '../../../frontend/src/types';

export type ScanDecision =
  | { kind: 'ignore' }
  | { kind: 'wait' }
  | { kind: 'mode'; mode: ScanDirection; barcode: string }
  | { kind: 'product'; barcode: string };

// Control barcodes switch the shared scanner mode, matching the web queue.
// Anything else is a product scan. Repeated reads of the same code are dropped
// until the shared cooldown elapses.
export function decideScan(
  raw: string,
  config: ScannerConfig | null,
  last: PriorScan | null,
  now: number,
): ScanDecision {
  const barcode = raw.trim();
  if (barcode === '') return { kind: 'ignore' };
  if (config === null) return { kind: 'wait' };
  if (!shouldAcceptScan(last, barcode, now, RESCAN_COOLDOWN_MS)) return { kind: 'ignore' };
  if (barcode === config.stockInBarcode) return { kind: 'mode', mode: 'stock_in', barcode };
  if (barcode === config.stockOutBarcode) return { kind: 'mode', mode: 'stock_out', barcode };
  return { kind: 'product', barcode };
}
