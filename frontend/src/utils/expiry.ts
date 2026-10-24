// Pure function mirroring internal/inventory/expiry.go's ComputeExpiryStatus.
import type { ExpiryStatus } from '@stockbot/shared/types';

const ONE_DAY_MS = 24 * 60 * 60 * 1000;

export function computeExpiryStatus(
  expiresAt: string | null,
  now: Date,
  warningDays = 7,
): ExpiryStatus {
  if (expiresAt === null) {
    return 'ok';
  }

  const daysUntilExpiry = (new Date(expiresAt).getTime() - now.getTime()) / ONE_DAY_MS;

  if (daysUntilExpiry < 0) {
    return 'expired';
  }
  if (daysUntilExpiry <= warningDays) {
    return 'near_expiry';
  }
  return 'ok';
}
