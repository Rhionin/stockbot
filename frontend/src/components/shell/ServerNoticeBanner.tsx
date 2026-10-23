import { useSyncExternalStore } from 'react';
import { Alert } from '@mantine/core';
import { serverNoticeText, subscribeOfflineBanner } from '../../api/contract';

// The same red banner the queue uses for a batch failure. Offline, and the
// other server-state notices, share this one instance above the routes.
export function ServerNoticeBanner() {
  const text = useSyncExternalStore(subscribeOfflineBanner, serverNoticeText, () => '');
  if (text === '') return null;
  return (
    <div data-testid="server-notice-banner">
      <Alert color="red" py={4} className="scan-batch-banner" style={{ flexShrink: 0 }}>
        <div>{text}</div>
      </Alert>
    </div>
  );
}
