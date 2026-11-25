import { useEffect, useRef } from 'react';
import { Pressable, ScrollView } from 'react-native';
import { brandTealDeep, mobileChrome, nothingOnHandLight } from '@stockbot/shared/brandTokens';
import { Text } from '@tamagui/core';
import { XStack, YStack } from '@tamagui/stacks';
import { focusOnWeb } from './focusWeb';
import { lineHeightPx } from './lineHeight';

export const BATCH_REVIEW_TEST_ID = 'batch-review';

export type BatchReviewRow = { id: string; label: string; detail?: string };

export type BatchReviewPanelProps = {
  heading: string;
  rows: BatchReviewRow[];
  skipLine?: string | null;
  error?: string;
  busy: boolean;
  confirmLabel: string;
  confirmName: string;
  backLabel: string;
  fontRegular: string;
  fontBold: string;
  onConfirm: () => void;
  onBack: () => void;
};

// Opens with focus on the panel so a keyboard or screen-reader user lands on
// what is about to be confirmed, not on the button that opened it.
export function BatchReviewPanel({
  heading,
  rows,
  skipLine,
  error,
  busy,
  confirmLabel,
  confirmName,
  backLabel,
  fontRegular,
  fontBold,
  onConfirm,
  onBack,
}: BatchReviewPanelProps) {
  const panel = useRef<unknown>(null);
  useEffect(() => {
    focusOnWeb(panel.current);
  }, []);

  return (
    <YStack
      ref={panel as never}
      testID={BATCH_REVIEW_TEST_ID}
      role="region"
      aria-label={heading}
      tabIndex={-1}
      gap={10}
      padding={12}
      marginTop={8}
      marginBottom={12}
      borderWidth={1}
      borderColor={mobileChrome.rule}
      backgroundColor={mobileChrome.white}
    >
      <Text
        role="heading"
        aria-level={2}
        accessibilityRole="header"
        fontFamily={fontBold}
        fontSize={20}
        lineHeight={lineHeightPx(26)}
        color={mobileChrome.ink}
      >
        {heading}
      </Text>
      <ScrollView style={{ maxHeight: 176 }} role="list" aria-label="Scans to confirm">
        {rows.map((row) => (
          <XStack
            key={row.id}
            role="listitem"
            testID={`${BATCH_REVIEW_TEST_ID}-row-${row.id}`}
            alignItems="center"
            gap={12}
            minHeight={32}
            borderBottomWidth={1}
            borderBottomColor={mobileChrome.rule}
          >
            <Text flex={1} fontFamily={fontRegular} fontSize={16} lineHeight={lineHeightPx(22)} color={mobileChrome.ink}>
              {row.label}
            </Text>
            {row.detail ? (
              <Text fontFamily={fontBold} fontSize={15} lineHeight={lineHeightPx(21)} color={mobileChrome.inkMuted}>
                {row.detail}
              </Text>
            ) : null}
          </XStack>
        ))}
      </ScrollView>
      {skipLine ? (
        <Text
          testID={`${BATCH_REVIEW_TEST_ID}-skip`}
          fontFamily={fontRegular}
          fontSize={15}
          lineHeight={lineHeightPx(21)}
          color={nothingOnHandLight.ink}
        >
          {skipLine}
        </Text>
      ) : null}
      {error ? (
        <Text role="alert" accessibilityRole="alert" fontFamily={fontRegular} fontSize={16} lineHeight={lineHeightPx(22)} color={mobileChrome.danger}>
          {error}
        </Text>
      ) : null}
      <XStack gap={12}>
        <Pressable
          role="button"
          accessibilityRole="button"
          accessibilityLabel={confirmName}
          disabled={busy || rows.length === 0}
          onPress={onConfirm}
          style={{
            flex: 1,
            minHeight: 48,
            alignItems: 'center',
            justifyContent: 'center',
            backgroundColor: brandTealDeep,
            opacity: busy ? 0.7 : 1,
          }}
        >
          <Text fontFamily={fontBold} fontSize={17} color={mobileChrome.white}>
            {busy ? 'Confirming' : confirmLabel}
          </Text>
        </Pressable>
        <Pressable
          role="button"
          accessibilityRole="button"
          accessibilityLabel={backLabel}
          disabled={busy}
          onPress={onBack}
          style={{
            minHeight: 48,
            minWidth: 88,
            alignItems: 'center',
            justifyContent: 'center',
            borderWidth: 1,
            borderColor: mobileChrome.inkMuted,
            paddingHorizontal: 12,
          }}
        >
          <Text fontFamily={fontBold} fontSize={17} color={mobileChrome.ink}>
            {backLabel}
          </Text>
        </Pressable>
      </XStack>
    </YStack>
  );
}
