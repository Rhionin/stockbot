import { Platform } from 'react-native';

// Moves keyboard and screen-reader focus on web, where a panel that appears or
// goes away would otherwise leave focus on a node that is gone. Native has no
// focus ring to move, so this does nothing there.
export function focusOnWeb(node: unknown): void {
  if (Platform.OS !== 'web') return;
  (node as { focus?: () => void } | null)?.focus?.();
}
