// A second read of the same code while it is still in frame is not a new item.
// Shared by the browser scanner and the Expo camera.

export const RESCAN_COOLDOWN_MS = 2000;

export interface PriorScan {
  value: string;
  at: number;
}

export function shouldAcceptScan(last: PriorScan | null, value: string, now: number, cooldownMs: number): boolean {
  if (last === null) return true;
  if (last.value !== value) return true;
  return now - last.at > cooldownMs;
}
