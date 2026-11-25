import type { ReactNode } from 'react';
import { Pressable, View } from 'react-native';
import { mobileChrome, nothingOnHandLight } from '@stockbot/shared/brandTokens';
import { Text } from '@tamagui/core';
import { XStack, YStack } from '@tamagui/stacks';
import { lineHeightPx } from './lineHeight';
import { SelectCheckbox } from './SelectCheckbox';

export const SESSION_CARD_TEST_ID_PREFIX = 'session-';

export type SessionCardProps = {
  sessionId: string;
  timeRange: string;
  countLabel: string;
  directionLabel: string;
  pill?: string | null;
  expanded: boolean;
  selectAll: {
    label: string;
    visibleLabel: string;
    checked: boolean | 'mixed';
    disabled: boolean;
    onChange: (next: boolean) => void;
  };
  fontRegular: string;
  fontBold: string;
  onToggle: () => void;
  children: ReactNode;
};

export function SessionCard({
  sessionId,
  timeRange,
  countLabel,
  directionLabel,
  pill,
  expanded,
  selectAll,
  fontRegular,
  fontBold,
  onToggle,
  children,
}: SessionCardProps) {
  return (
    <YStack
      testID={`${SESSION_CARD_TEST_ID_PREFIX}${sessionId}`}
      role="region"
      aria-label={`Scan session ${timeRange}`}
      marginBottom={12}
      borderWidth={1}
      borderColor={mobileChrome.rule}
    >
      <Pressable
        role="button"
        aria-expanded={expanded}
        accessibilityRole="button"
        accessibilityState={{ expanded }}
        testID={`${SESSION_CARD_TEST_ID_PREFIX}${sessionId}-toggle`}
        onPress={onToggle}
        style={{
          minHeight: 52,
          flexDirection: 'row',
          alignItems: 'center',
          gap: 12,
          paddingHorizontal: 12,
          paddingVertical: 8,
          backgroundColor: mobileChrome.white,
        }}
      >
        <YStack flex={1} alignItems="flex-start">
          <Text fontFamily={fontBold} fontSize={17} lineHeight={lineHeightPx(23)} color={mobileChrome.ink}>
            {timeRange}
          </Text>
          <Text fontFamily={fontRegular} fontSize={15} lineHeight={lineHeightPx(21)} color={mobileChrome.inkMuted}>
            {`${countLabel} · ${directionLabel}`}
          </Text>
        </YStack>
        {pill ? (
          <Text
            fontFamily={fontBold}
            fontSize={13}
            lineHeight={lineHeightPx(18)}
            color={nothingOnHandLight.ink}
            backgroundColor={nothingOnHandLight.wash}
            borderWidth={1}
            borderColor={nothingOnHandLight.border}
            paddingHorizontal={8}
            paddingVertical={2}
          >
            {pill}
          </Text>
        ) : null}
        <View
          aria-hidden
          style={{
            width: 10,
            height: 10,
            borderRightWidth: 2,
            borderBottomWidth: 2,
            borderColor: mobileChrome.inkMuted,
            transform: [{ rotate: expanded ? '-135deg' : '45deg' }],
            marginRight: 4,
          }}
        />
      </Pressable>
      {expanded ? (
        <YStack borderTopWidth={1} borderTopColor={mobileChrome.rule}>
          <XStack paddingLeft={12}>
            <SelectCheckbox
              testID={`${SESSION_CARD_TEST_ID_PREFIX}${sessionId}-select-all`}
              label={selectAll.label}
              visibleLabel={selectAll.visibleLabel}
              checked={selectAll.checked}
              disabled={selectAll.disabled}
              fontRegular={fontRegular}
              onChange={selectAll.onChange}
            />
          </XStack>
          {children}
        </YStack>
      ) : null}
    </YStack>
  );
}
