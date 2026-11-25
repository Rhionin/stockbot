import { AppState } from 'react-native';
import type { ForegroundSource } from './foregroundSource';

export const foregroundSource: ForegroundSource = {
  isActive: () => AppState.currentState === 'active',
  subscribe(listener) {
    const subscription = AppState.addEventListener('change', (state) => listener(state === 'active'));
    return () => subscription.remove();
  },
};
