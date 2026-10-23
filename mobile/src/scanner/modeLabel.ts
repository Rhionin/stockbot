import type { ScanDirection } from '../../../frontend/src/types';

export function modeLabel(mode: ScanDirection): string {
  return mode === 'stock_in' ? 'Putting away' : 'Using up';
}
