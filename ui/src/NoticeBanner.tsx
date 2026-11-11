import { useSyncExternalStore } from 'react';
import { Button, Text, XStack } from 'tamagui';
import { serverNoticeText, subscribeOfflineBanner } from '@stockbot/shared/contract';
import {
  NOTICE_BANNER_EDGE,
  NOTICE_BANNER_RADIUS,
  NOTICE_BANNER_TEXT,
  NOTICE_BANNER_TINT,
  NOTICE_SCREEN_PADDING_INLINE,
} from '@stockbot/shared/noticeBanner';

export const NOTICE_BANNER_TEST_ID = 'server-notice-banner';
export const NOTICE_SCREEN_INSET = NOTICE_SCREEN_PADDING_INLINE;

export const NOTICE_DEMO_ACTION_LABEL = 'Try again';

export type NoticeBannerProps = {
  topInset?: number;
  text?: string;
  fontRegular?: string;
  fontBold?: string;
  /** Harness-only optional action for Jest press coverage. */
  onDemoAction?: () => void;
  demoActionLabel?: string;
};

function NoticeIcon({ fontBold }: { fontBold?: string }) {
  return (
    <XStack
      width={20}
      height={20}
      marginTop={1}
      borderRadius={10}
      borderWidth={1.5}
      borderColor={NOTICE_BANNER_EDGE}
      alignItems="center"
      justifyContent="center"
      accessibilityElementsHidden
      importantForAccessibility="no-hide-descendants"
    >
      <Text
        color={NOTICE_BANNER_EDGE}
        fontSize={13}
        lineHeight={16}
        fontWeight="700"
        fontFamily={fontBold}
      >
        !
      </Text>
    </XStack>
  );
}

export function NoticeBanner({
  topInset = 0,
  text = '',
  fontRegular,
  fontBold,
  onDemoAction,
  demoActionLabel = NOTICE_DEMO_ACTION_LABEL,
}: NoticeBannerProps) {
  const notice = useSyncExternalStore(subscribeOfflineBanner, serverNoticeText, () => '');
  const shown = notice !== '' ? notice : text;
  if (shown === '') return null;

  let demoAction = null;
  if (onDemoAction) {
    demoAction = (
      <Button
        accessibilityRole="button"
        role="button"
        aria-label={demoActionLabel}
        onPress={onDemoAction}
        backgroundColor={NOTICE_BANNER_TINT}
        borderWidth={1}
        borderColor={NOTICE_BANNER_EDGE}
        borderRadius={NOTICE_BANNER_RADIUS}
        color={NOTICE_BANNER_TEXT}
        paddingHorizontal={12}
        paddingVertical={4}
        fontFamily={fontBold}
      >
        {demoActionLabel}
      </Button>
    );
  }

  return (
    <XStack
      testID={NOTICE_BANNER_TEST_ID}
      accessibilityLiveRegion="polite"
      marginTop={topInset}
      marginHorizontal={NOTICE_SCREEN_INSET}
      marginBottom={12}
      backgroundColor={NOTICE_BANNER_TINT}
      borderWidth={1}
      borderColor={NOTICE_BANNER_EDGE}
      borderRadius={NOTICE_BANNER_RADIUS}
      paddingVertical={4}
      paddingHorizontal={12}
      gap={10}
      alignItems="flex-start"
      maxWidth="100%"
    >
      <NoticeIcon fontBold={fontBold} />
      <Text flex={1} color={NOTICE_BANNER_TEXT} fontSize={16} lineHeight={22} fontFamily={fontRegular}>
        {shown}
      </Text>
      {demoAction}
    </XStack>
  );
}
