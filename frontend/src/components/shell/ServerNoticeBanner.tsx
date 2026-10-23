import { useSyncExternalStore } from 'react';
import { Alert } from '@mantine/core';
import { serverNoticeText, subscribeOfflineBanner } from '../../api/contract';

// One red banner. A server notice (offline, sign-in) wins over the queue's
// busy or out-of-space sentence, so that spot never shows two banners.
export function ServerNoticeBanner({ text = '', partial = '' }: { text?: string; partial?: string }) {
  const notice = useSyncExternalStore(subscribeOfflineBanner, serverNoticeText, () => '');
  const shown = notice !== '' ? notice : text;
  if (shown === '') return null;
  const detail = notice === '' ? partial : '';
  return (
    <div data-testid="server-notice-banner">
      <Alert color="red" py={4} className="scan-batch-banner" style={{ flexShrink: 0 }}>
        <div>{shown}</div>
        {detail !== '' && <p className="scan-batch-partial" style={{ flexShrink: 0 }}>{detail}</p>}
      </Alert>
    </div>
  );
}
