import { clearOfflineBanner, UnreachableError } from '../../api/contract';

export function clearOfflineNotice(): void {
  clearOfflineBanner();
}

export function isUnreachable(error: unknown): error is UnreachableError {
  return error instanceof UnreachableError;
}
