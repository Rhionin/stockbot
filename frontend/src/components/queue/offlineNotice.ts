import { notifications } from '@mantine/notifications';
import { OFFLINE_SENTENCE, subscribeApiSuccess, UnreachableError } from '../../api/contract';
import { showUndo } from '../restock/undo';

export const OFFLINE_NOTICE_ID = 'stockbot-offline';

const clears = new Set<() => void>();

export function subscribeOfflineClear(listener: () => void): () => void {
  clears.add(listener);
  return () => {
    clears.delete(listener);
  };
}

// A later successful request drops the sentence, the same way a confirm drops a busy notice.
export function clearOfflineNotice(): void {
  notifications.hide(OFFLINE_NOTICE_ID);
  for (const listener of clears) listener();
}

export function showOfflineNotice(): void {
  showUndo({
    id: OFFLINE_NOTICE_ID,
    detail: OFFLINE_SENTENCE,
    color: 'red',
    autoClose: false,
  });
}

export function isUnreachable(error: unknown): error is UnreachableError {
  return error instanceof UnreachableError;
}

// Any successful API response, including an inventory or product-list load.
subscribeApiSuccess(clearOfflineNotice);
