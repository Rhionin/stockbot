import { Pressable, View } from 'react-native';
import { mobileChrome, nothingOnHandLight } from '@stockbot/shared/brandTokens';
import { Text } from '@tamagui/core';
import { XStack, YStack } from '@tamagui/stacks';
import { lineHeightPx } from './lineHeight';

export const SEARCH_RESULT_TEST_ID_PREFIX = 'search-result-';

export type SearchResultRow = {
  id: string;
  title: string;
  detail?: string;
  badge?: string;
  badgeWarn?: boolean;
};

export type SearchResultsProps = {
  label: string;
  rows: SearchResultRow[];
  disabled?: boolean;
  fontRegular: string;
  fontBold: string;
  onPick: (id: string) => void;
};

// One tap picks a row, so each row is a button inside a named list. The
// button's name is its visible text, so what is spoken is what is on screen.
export function SearchResults({ label, rows, disabled = false, fontRegular, fontBold, onPick }: SearchResultsProps) {
  return (
    <YStack role="list" aria-label={label} accessibilityLabel={label}>
      {rows.map((row) => (
        <View key={row.id} role="listitem">
          <Pressable
            testID={`${SEARCH_RESULT_TEST_ID_PREFIX}${row.id}`}
            accessibilityRole="button"
            accessibilityState={{ disabled }}
            disabled={disabled}
            onPress={() => onPick(row.id)}
            style={{
              minHeight: 56,
              justifyContent: 'center',
              paddingVertical: 8,
              paddingHorizontal: 10,
              borderLeftWidth: 4,
              borderLeftColor: mobileChrome.rule,
              borderBottomWidth: 1,
              borderBottomColor: mobileChrome.rule,
              backgroundColor: mobileChrome.white,
              opacity: disabled ? 0.5 : 1,
            }}
          >
            <XStack alignItems="center" gap={12}>
              <YStack flex={1}>
                <Text fontFamily={fontBold} fontSize={17} lineHeight={lineHeightPx(23)} color={mobileChrome.ink} textAlign="left">
                  {row.title}
                </Text>
                {row.detail ? (
                  <Text fontFamily={fontRegular} fontSize={15} lineHeight={lineHeightPx(21)} color={mobileChrome.inkMuted} textAlign="left">
                    {row.detail}
                  </Text>
                ) : null}
              </YStack>
              {row.badge ? (
                <Text
                  fontFamily={fontBold}
                  fontSize={13}
                  lineHeight={lineHeightPx(18)}
                  color={row.badgeWarn ? nothingOnHandLight.ink : mobileChrome.inkMuted}
                  backgroundColor={row.badgeWarn ? nothingOnHandLight.wash : undefined}
                  borderWidth={1}
                  borderColor={row.badgeWarn ? nothingOnHandLight.border : mobileChrome.rule}
                  paddingHorizontal={8}
                  paddingVertical={2}
                >
                  {row.badge}
                </Text>
              ) : null}
            </XStack>
          </Pressable>
        </View>
      ))}
    </YStack>
  );
}
