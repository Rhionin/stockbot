import type { ScanDirection } from '@stockbot/shared/types';

export function modeLabel(mode: ScanDirection): string {
  return mode === 'stock_in' ? 'Putting away' : 'Using up';
}
