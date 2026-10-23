import { useSyncExternalStore } from 'react';
import { Alert } from '@mantine/core';
import { OFFLINE_SENTENCE, offlineBannerActive, subscribeOfflineBanner } from '../../api/contract';

// The same red banner the queue uses for a batch failure. Offline is that
// notice for whichever screen is open, so this one instance sits above the routes.
export function ServerNoticeBanner() {
  const active = useSyncExternalStore(subscribeOfflineBanner, offlineBannerActive, () => false);
  if (!active) return null;
  return (
    <div data-testid="server-notice-banner">
      <Alert color="red" py={4} className="scan-batch-banner" style={{ flexShrink: 0 }}>
        <div>{OFFLINE_SENTENCE}</div>
      </Alert>
    </div>
  );
}
