import type { ReactNode } from 'react';
import { NOTHING_ON_HAND_BODY, NOTHING_ON_HAND_TITLE } from '@stockbot/shared/contract';
import {
  amberDeep,
  brandTeal,
  brandTealDeep,
  mobileChrome,
  nothingOnHandLight,
} from '@stockbot/shared/brandTokens';
import { Button, Text, XStack, YStack } from 'tamagui';

export const QUEUE_CARD_TEST_ID_PREFIX = 'scan-';

export type QueueCardProps = {
  entryId: string;
  label: string;
  barcode: string;
  metaLine: string;
  stockIn: boolean;
  stockOut: boolean;
  blocked: boolean;
  busy: boolean;
  busyThis: boolean;
  pinLive?: boolean;
  note?: string;
  fontRegular: string;
  fontBold: string;
  onConfirm: () => void;
  onRemove: () => void;
  footer?: ReactNode;
};

export function QueueCard({
  entryId,
  label,
  barcode,
  metaLine,
  stockIn,
  stockOut,
  blocked,
  busy,
  busyThis,
  pinLive,
  note,
  fontRegular,
  fontBold,
  onConfirm,
  onRemove,
  footer,
}: QueueCardProps) {
  const borderLeftColor = blocked
    ? nothingOnHandLight.border
    : stockOut
      ? amberDeep
      : stockIn
        ? brandTeal
        : mobileChrome.rule;

  const confirmLabel = busyThis ? 'Confirming' : 'Confirm';
  const removeLabel = busyThis ? 'Removing' : 'Remove';

  return (
    <YStack
      testID={`${QUEUE_CARD_TEST_ID_PREFIX}${entryId}`}
      borderLeftWidth={4}
      borderLeftColor={borderLeftColor}
      paddingLeft={12}
      paddingVertical={14}
      borderBottomWidth={1}
      borderBottomColor={mobileChrome.rule}
      backgroundColor={blocked ? nothingOnHandLight.wash : undefined}
      borderWidth={blocked ? 1 : 0}
      borderColor={blocked ? nothingOnHandLight.border : undefined}
      paddingRight={blocked ? 12 : 0}
      marginBottom={blocked ? 10 : 0}
    >
      <XStack alignItems="center" gap={12}>
        <YStack flex={1}>
          <Text fontFamily={fontBold} fontSize={18} lineHeight={24} color={mobileChrome.ink}>
            {label}
          </Text>
          <Text
            fontFamily={fontRegular}
            fontSize={15}
            lineHeight={21}
            color={mobileChrome.inkMuted}
            marginTop={2}
          >
            {metaLine}
          </Text>
        </YStack>
        {blocked ? (
          <Button
            accessibilityRole="button"
            accessibilityLabel={`Remove ${label}`}
            disabled={busy}
            onPress={onRemove}
            backgroundColor={mobileChrome.removeWash}
            borderRadius={0}
            minHeight={44}
            minWidth={88}
            paddingHorizontal={12}
            pressStyle={{ opacity: 0.9 }}
          >
            <Text fontFamily={fontBold} fontSize={15} color={mobileChrome.danger}>
              {removeLabel}
            </Text>
          </Button>
        ) : (
          <Button
            accessibilityRole="button"
            accessibilityLabel={`Confirm ${label}`}
            disabled={busy}
            onPress={onConfirm}
            backgroundColor={brandTealDeep}
            borderRadius={0}
            minHeight={44}
            minWidth={88}
            paddingHorizontal={12}
            pressStyle={{ opacity: 0.9 }}
          >
            <Text fontFamily={fontBold} fontSize={15} color={mobileChrome.white}>
              {confirmLabel}
            </Text>
          </Button>
        )}
      </XStack>
      {blocked && (
        <YStack marginTop={10}>
          <XStack alignItems="flex-start" gap={8}>
            <YStack
              width={22}
              height={22}
              borderRadius={11}
              backgroundColor={nothingOnHandLight.ink}
              alignItems="center"
              justifyContent="center"
              marginTop={1}
            >
              <Text fontFamily={fontBold} fontSize={14} lineHeight={18} color={nothingOnHandLight.wash}>
                !
              </Text>
            </YStack>
            <Text
              flex={1}
              fontFamily={fontBold}
              fontSize={16}
              lineHeight={22}
              color={nothingOnHandLight.ink}
              accessibilityLabel={`Scan ${barcode}, ${NOTHING_ON_HAND_TITLE}`}
              accessibilityHint={NOTHING_ON_HAND_BODY}
              accessibilityLiveRegion={pinLive ? 'polite' : undefined}
            >
              {NOTHING_ON_HAND_TITLE}
            </Text>
          </XStack>
          <Text
            fontFamily={fontRegular}
            fontSize={16}
            lineHeight={22}
            color={mobileChrome.ink}
            marginTop={4}
          >
            {NOTHING_ON_HAND_BODY}
          </Text>
        </YStack>
      )}
      {note ? (
        <Text fontFamily={fontRegular} fontSize={15} lineHeight={21} color={mobileChrome.inkMuted} marginTop={8}>
          {note}
        </Text>
      ) : null}
      {footer}
    </YStack>
  );
}
