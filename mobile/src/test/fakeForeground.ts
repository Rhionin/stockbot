import type { ForegroundSource } from '../live/foregroundSource';

let active = true;
const listeners = new Set<(active: boolean) => void>();

export const foregroundSource: ForegroundSource = {
  isActive: () => active,
  subscribe(listener) {
    listeners.add(listener);
    return () => {
      listeners.delete(listener);
    };
  },
};

export function setForeground(next: boolean): void {
  active = next;
  for (const listener of [...listeners]) listener(next);
}
