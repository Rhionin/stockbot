import { Pressable, View } from 'react-native';
import {
  amberDeep,
  amberScale,
  brandTealDeep,
  mobileChrome,
  tealScale,
} from '@stockbot/shared/brandTokens';
import { formatReviewCount } from '@stockbot/shared/queueUtils';
import {
  DIRECTION_HINT,
  SCANNER_CONNECTED,
  SCANNER_DISCONNECTED,
  directionTabName,
} from '@stockbot/shared/queueHeader';
import type { ScanDirection } from '@stockbot/shared/types';
import { Text } from '@tamagui/core';
import { XStack, YStack } from '@tamagui/stacks';
import { lineHeightPx } from './lineHeight';

const DIRECTION_LOOK = {
  stock_in: { ink: tealScale[9], wash: tealScale[0], edge: brandTealDeep, hint: brandTealDeep },
  stock_out: { ink: amberScale[9], wash: amberScale[0], edge: amberDeep, hint: amberDeep },
} as const;

export const DIRECTION_TAB_TEST_ID_PREFIX = 'direction-tab-';

export type DirectionTab = {
  value: ScanDirection;
  label: string;
  count: number;
};

export type DirectionTabsProps = {
  tabs: DirectionTab[];
  value: ScanDirection;
  label: string;
  fontBold: string;
  onChange: (value: ScanDirection) => void;
};

export function DirectionTabs({ tabs, value, label, fontBold, onChange }: DirectionTabsProps) {
  return (
    <XStack role="tablist" aria-label={label} width="100%">
      {tabs.map((tab) => {
        const selected = tab.value === value;
        const look = DIRECTION_LOOK[tab.value];
        return (
          <Pressable
            key={tab.value}
            testID={`${DIRECTION_TAB_TEST_ID_PREFIX}${tab.value}`}
            role="tab"
            aria-selected={selected}
            accessibilityState={{ selected }}
            accessibilityLabel={directionTabName(tab.label, tab.count)}
            onPress={() => onChange(tab.value)}
            style={{
              flex: 1,
              minHeight: 48,
              flexDirection: 'row',
              alignItems: 'center',
              justifyContent: 'center',
              gap: 8,
              paddingHorizontal: 8,
              backgroundColor: selected ? look.wash : 'transparent',
              borderBottomWidth: selected ? 4 : 1,
              borderBottomColor: selected ? look.edge : mobileChrome.rule,
            }}
          >
            <Text
              fontFamily={fontBold}
              fontSize={selected ? 18 : 17}
              lineHeight={lineHeightPx(24)}
              color={selected ? look.ink : mobileChrome.inkMuted}
            >
              {tab.count > 0 ? `${tab.label} ` : tab.label}
            </Text>
            {tab.count > 0 ? (
              <View testID={`${DIRECTION_TAB_TEST_ID_PREFIX}${tab.value}-badge`} aria-hidden accessibilityElementsHidden importantForAccessibility="no-hide-descendants">
                <YStack
                  testID={`${DIRECTION_TAB_TEST_ID_PREFIX}${tab.value}-count`}
                  minWidth={26}
                  height={24}
                  paddingHorizontal={7}
                  borderRadius={12}
                  alignItems="center"
                  justifyContent="center"
                  backgroundColor={selected ? look.edge : mobileChrome.rule}
                >
                  <Text
                    fontFamily={fontBold}
                    fontSize={14}
                    lineHeight={lineHeightPx(18)}
                    color={selected ? mobileChrome.white : mobileChrome.ink}
                  >
                    {formatReviewCount(tab.count)}
                  </Text>
                </YStack>
              </View>
            ) : null}
          </Pressable>
        );
      })}
    </XStack>
  );
}

export type DirectionHintProps = {
  mode: ScanDirection;
  fontBold: string;
};

export function DirectionHint({ mode, fontBold }: DirectionHintProps) {
  return (
    <Text
      testID="direction-hint"
      role="status"
      accessibilityLiveRegion="polite"
      fontFamily={fontBold}
      fontSize={17}
      lineHeight={lineHeightPx(24)}
      color={DIRECTION_LOOK[mode].hint}
    >
      {DIRECTION_HINT[mode]}
    </Text>
  );
}

export type ScannerStatusProps = {
  connected: boolean | null;
  fontRegular: string;
};

export function ScannerStatus({ connected, fontRegular }: ScannerStatusProps) {
  if (connected === null) return null;
  return (
    <Text
      testID="scanner-status"
      role="status"
      accessibilityLiveRegion="polite"
      fontFamily={fontRegular}
      fontSize={15}
      lineHeight={lineHeightPx(21)}
      color={connected ? brandTealDeep : mobileChrome.danger}
    >
      {connected ? SCANNER_CONNECTED : SCANNER_DISCONNECTED}
    </Text>
  );
}
