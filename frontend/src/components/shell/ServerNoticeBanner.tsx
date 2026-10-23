import { useSyncExternalStore } from 'react';
import { Alert } from '@mantine/core';
import { serverNoticeText, subscribeOfflineBanner } from '../../api/contract';
import {
  NOTICE_BANNER_EDGE,
  NOTICE_BANNER_RADIUS,
  NOTICE_BANNER_TEXT,
  NOTICE_BANNER_TINT,
  NOTICE_SCREEN_PADDING_INLINE,
  NOTICE_SCREEN_PADDING_TOP,
} from '../../noticeBanner';

const bannerStyle = {
  flexShrink: 0,
  backgroundColor: NOTICE_BANNER_TINT,
  color: NOTICE_BANNER_TEXT,
  border: `1px solid ${NOTICE_BANNER_EDGE}`,
  borderRadius: NOTICE_BANNER_RADIUS,
} as const;

function NoticeIcon() {
  return (
    <svg className="server-notice-icon" viewBox="0 0 20 20" width="20" height="20" aria-hidden="true">
      <circle cx="10" cy="10" r="7.25" fill="none" stroke={NOTICE_BANNER_EDGE} strokeWidth="1.5" />
      <path d="M10 5.6v5" stroke={NOTICE_BANNER_EDGE} strokeWidth="1.5" strokeLinecap="round" />
      <circle cx="10" cy="14.15" r="0.95" fill={NOTICE_BANNER_EDGE} />
    </svg>
  );
}

// One banner. A server notice (offline, sign-in) wins over the queue's
// busy or out-of-space sentence, so that spot never shows two banners.
export function ServerNoticeBanner({
  text = '',
  partial = '',
  inset = false,
}: {
  text?: string;
  partial?: string;
  inset?: boolean;
}) {
  const notice = useSyncExternalStore(subscribeOfflineBanner, serverNoticeText, () => '');
  const shown = notice !== '' ? notice : text;
  if (shown === '') return null;
  const detail = notice === '' ? partial : '';
  const screenInset = inset
    ? {
        marginTop: NOTICE_SCREEN_PADDING_TOP,
        marginRight: NOTICE_SCREEN_PADDING_INLINE,
        marginLeft: NOTICE_SCREEN_PADDING_INLINE,
      }
    : undefined;
  return (
    <div data-testid="server-notice-banner" className={inset ? 'server-notice-inset' : undefined} style={screenInset}>
      <Alert
        color="red"
        variant="light"
        py={4}
        icon={<NoticeIcon />}
        className="scan-batch-banner"
        style={bannerStyle}
      >
        <div style={{ color: NOTICE_BANNER_TEXT }}>{shown}</div>
        {detail !== '' && <p className="scan-batch-partial" style={{ flexShrink: 0, color: NOTICE_BANNER_TEXT }}>{detail}</p>}
      </Alert>
    </div>
  );
}
