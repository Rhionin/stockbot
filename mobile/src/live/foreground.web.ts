import type { ForegroundSource } from './foregroundSource';

export const foregroundSource: ForegroundSource = {
  isActive: () => document.visibilityState !== 'hidden',
  subscribe(listener) {
    const onChange = () => listener(document.visibilityState !== 'hidden');
    document.addEventListener('visibilitychange', onChange);
    return () => document.removeEventListener('visibilitychange', onChange);
  },
};
