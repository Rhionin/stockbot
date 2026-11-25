import { View } from 'react-native';
import { brandTealDeep, mobileChrome } from '@stockbot/shared/brandTokens';
import {
  QR_CARD_TITLE,
  QR_DISMISS_LABEL,
  QR_RESCAN_LABEL,
  qrCardLabel,
} from '@stockbot/shared/queueCards';
import { Text } from '@tamagui/core';
import { XStack, YStack } from '@tamagui/stacks';
import { ActionButton } from './ActionButton';
import { lineHeightPx } from './lineHeight';

export const QR_CARD_TEST_ID_PREFIX = 'qr-scan-';

// The barcode that stops short: heights in points, one per bar.
const RAIL = [
  { width: 2, height: 72 },
  { width: 1, height: 72 },
  { width: 2, height: 51 },
  { width: 2, height: 27 },
  { width: 3, height: 64 },
  { width: 2, height: 72 },
  { width: 2, height: 46 },
] as const;

export type QrCardProps = {
  entryId: string;
  preview: string;
  // The code was scanned a moment ago.
  justCaptured?: boolean;
  dismissing: boolean;
  error: string;
  fontRegular: string;
  fontBold: string;
  onRescan: () => void;
  onDismiss: () => void;
};

export function QrCard({
  entryId,
  preview,
  justCaptured = false,
  dismissing,
  error,
  fontRegular,
  fontBold,
  onRescan,
  onDismiss,
}: QrCardProps) {
  const rule = justCaptured ? brandTealDeep : mobileChrome.rule;
  return (
    <YStack
      testID={`${QR_CARD_TEST_ID_PREFIX}${entryId}`}
      role="article"
      aria-label={qrCardLabel(preview)}
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
      gap={12}
    >
      <XStack gap={14} alignItems="center">
        <View
          accessibilityElementsHidden
          importantForAccessibility="no-hide-descendants"
          aria-hidden
          style={{ flexDirection: 'row', alignItems: 'flex-end', justifyContent: 'space-between', gap: 3, height: 72 }}
        >
          {RAIL.map((bar, index) => (
            <View key={index} style={{ width: bar.width, height: bar.height, backgroundColor: brandTealDeep }} />
          ))}
        </View>
        <YStack flex={1} minWidth={0}>
          <Text fontFamily={fontBold} fontSize={18} lineHeight={lineHeightPx(24)} color={mobileChrome.ink}>
            {QR_CARD_TITLE}
          </Text>
          {preview !== '' ? (
            <Text
              fontFamily={fontRegular}
              fontSize={15}
              lineHeight={lineHeightPx(21)}
              color={mobileChrome.inkMuted}
              marginTop={4}
              numberOfLines={1}
            >
              {preview}
            </Text>
          ) : null}
        </YStack>
      </XStack>
      <XStack gap={10} flexWrap="wrap" alignItems="center">
        <ActionButton label={QR_RESCAN_LABEL} fontBold={fontBold} disabled={dismissing} onPress={onRescan} />
        <ActionButton
          label={QR_DISMISS_LABEL}
          variant="secondary"
          fontBold={fontBold}
          busy={dismissing}
          onPress={onDismiss}
        />
      </XStack>
      {error !== '' ? (
        <Text
          role="alert"
          accessibilityRole="alert"
          fontFamily={fontRegular}
          fontSize={15}
          lineHeight={lineHeightPx(21)}
          color={mobileChrome.danger}
        >
          {error}
        </Text>
      ) : null}
    </YStack>
  );
}
