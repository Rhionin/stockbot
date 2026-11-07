import { stockColors as sharedStockColors } from '@stockbot/shared/brandTokens';

// Full is the calm teal. Amber and red show up only when stock needs attention.
export type StockState = 'full' | 'low' | 'out';

export const stockColors = sharedStockColors;

// Out wins over low: an empty shelf is empty, even when the buy rule would also flag it.
// A group with no members is not an empty product, so callers pass memberCount.
export function stockState(onHand: number, runningLow = false): StockState {
  if (onHand <= 0) return 'out';
  if (runningLow) return 'low';
  return 'full';
}

export function groupStock(onHand: number, runningLow: boolean, memberCount: number): StockState {
  if (memberCount <= 0) return 'full';
  return stockState(onHand, runningLow);
}
