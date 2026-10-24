import type { ScanDirection } from '@stockbot/shared/types';

// The server publishes scanner_mode as a JSON string ("stock_in"), which is
// what encoding a ScanDirection produces. Older callers and tests also send
// an object with a mode field. Both name the same direction.
export function scannerModeFromEventData(data: string): ScanDirection | null {
  let parsed: unknown;
  try {
    parsed = JSON.parse(data);
  } catch {
    return null;
  }
  if (parsed === 'stock_in' || parsed === 'stock_out') return parsed;
  if (parsed !== null && typeof parsed === 'object' && 'mode' in parsed) {
    const mode = (parsed as { mode?: unknown }).mode;
    if (mode === 'stock_in' || mode === 'stock_out') return mode;
  }
  return null;
}
