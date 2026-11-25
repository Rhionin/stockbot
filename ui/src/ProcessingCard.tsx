import { ActivityIndicator, View } from 'react-native';
import { brandTealDeep, mobileChrome } from '@stockbot/shared/brandTokens';
import {
  PROCESSING_DETAIL,
  PROCESSING_TITLE,
  processingBarcodeLine,
  processingCardLabel,
} from '@stockbot/shared/queueCards';
import { Text } from '@tamagui/core';
import { XStack, YStack } from '@tamagui/stacks';
import { lineHeightPx } from './lineHeight';

export const PROCESSING_CARD_TEST_ID_PREFIX = 'processing-';
export const PROCESSING_STATUS_TEST_ID_PREFIX = 'processing-status-';

export type ProcessingCardProps = {
  noticeId: string;
  barcode: string;
  // The code was scanned a moment ago.
  justCaptured?: boolean;
  fontRegular: string;
  fontBold: string;
};

export function ProcessingCard({ noticeId, barcode, justCaptured = false, fontRegular, fontBold }: ProcessingCardProps) {
  const rule = justCaptured ? brandTealDeep : mobileChrome.rule;
  return (
    <YStack
      testID={`${PROCESSING_CARD_TEST_ID_PREFIX}${noticeId}`}
      role="article"
      aria-label={processingCardLabel(barcode)}
      aria-busy
      marginBottom={12}
      padding={14}
      borderTopWidth={1}
      borderRightWidth={1}
      borderBottomWidth={1}
      borderLeftWidth={1}
      borderTopColor={rule}
      borderRightColor={rule}
      borderBottomColor={rule}
      borderLeftColor={rule}
      backgroundColor={justCaptured ? mobileChrome.successWash : undefined}
    >
      <XStack gap={12} alignItems="center">
        <View importantForAccessibility="no-hide-descendants" aria-hidden>
          <ActivityIndicator accessible={false} color={brandTealDeep} size="small" />
        </View>
        <YStack
          testID={`${PROCESSING_STATUS_TEST_ID_PREFIX}${noticeId}`}
          flex={1}
          minWidth={0}
          role="status"
          accessibilityLiveRegion="polite"
        >
          <Text fontFamily={fontBold} fontSize={18} lineHeight={lineHeightPx(24)} color={mobileChrome.ink}>
            {PROCESSING_TITLE}
          </Text>
          <Text
            fontFamily={fontRegular}
            fontSize={15}
            lineHeight={lineHeightPx(21)}
            color={mobileChrome.inkMuted}
            marginTop={2}
          >
            {processingBarcodeLine(barcode)}
          </Text>
          <Text fontFamily={fontRegular} fontSize={15} lineHeight={lineHeightPx(21)} color={mobileChrome.ink}>
            {PROCESSING_DETAIL}
          </Text>
        </YStack>
      </XStack>
    </YStack>
  );
}
