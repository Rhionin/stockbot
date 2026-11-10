import type { ReactNode } from 'react';
import { Button, Text, XStack } from 'tamagui';
import { OFFLINE_SENTENCE } from '@stockbot/shared/contract';
import {
  NOTICE_BANNER_EDGE,
  NOTICE_BANNER_RADIUS,
  NOTICE_BANNER_TEXT,
  NOTICE_BANNER_TINT,
} from '@stockbot/shared/noticeBanner';

export const TAMAGUI_NOTICE_TEST_ID = 'tamagui-notice-banner';
export const TAMAGUI_NOTICE_ACTION_LABEL = 'Try again';

export type TamaguiNoticeBannerProps = {
  message?: string;
  actionLabel?: string;
  onAction?: () => void;
};

function NoticeIcon() {
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
      aria-hidden
    >
      <Text color={NOTICE_BANNER_EDGE} fontSize={13} lineHeight={16} fontWeight="700">
        !
      </Text>
    </XStack>
  );
}

export function TamaguiNoticeBanner({
  message = OFFLINE_SENTENCE,
  actionLabel = TAMAGUI_NOTICE_ACTION_LABEL,
  onAction,
}: TamaguiNoticeBannerProps) {
  let action: ReactNode = null;
  if (onAction) {
    action = (
      <Button
        role="button"
        accessibilityRole="button"
        aria-label={actionLabel}
        onPress={onAction}
        borderRadius={NOTICE_BANNER_RADIUS}
        backgroundColor={NOTICE_BANNER_TINT}
        color={NOTICE_BANNER_TEXT}
        borderWidth={1}
        borderColor={NOTICE_BANNER_EDGE}
        paddingHorizontal={12}
        paddingVertical={4}
        tabIndex={0}
        outlineColor="$actionFocusRing"
        outlineWidth={2}
        outlineStyle="solid"
      >
        {actionLabel}
      </Button>
    );
  }
  return (
    <XStack
      testID={TAMAGUI_NOTICE_TEST_ID}
      data-testid={TAMAGUI_NOTICE_TEST_ID}
      accessibilityLiveRegion="polite"
      role="alert"
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
      <NoticeIcon />
      <Text flex={1} color={NOTICE_BANNER_TEXT} fontSize={16} lineHeight={22}>
        {message}
      </Text>
      {action}
    </XStack>
  );
}
