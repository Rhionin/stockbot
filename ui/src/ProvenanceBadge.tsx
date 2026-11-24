import { mobileChrome } from '@stockbot/shared/brandTokens';
import { DATABASE_NAMES } from '@stockbot/shared/databaseNames';
import type { ExternalSource } from '@stockbot/shared/types';
import { Text } from '@tamagui/core';
import { YStack } from '@tamagui/stacks';
import { lineHeightPx } from './lineHeight';

export const PROVENANCE_BADGE_TEST_ID = 'provenance-badge';

export type ProvenanceBadgeProps = {
  externalSource?: ExternalSource;
  // A filled badge reads as a button. Inventory only needs the source named.
  quiet?: boolean;
  fontRegular: string;
  fontBold: string;
};

export function ProvenanceBadge({ externalSource, quiet = false, fontRegular, fontBold }: ProvenanceBadgeProps) {
  if (!externalSource || !(externalSource in DATABASE_NAMES)) return null;
  const name = DATABASE_NAMES[externalSource];
  const label = `Product data from ${name}`;
  if (quiet) {
    return (
      <Text
        testID={PROVENANCE_BADGE_TEST_ID}
        accessibilityLabel={label}
        fontFamily={fontRegular}
        fontSize={15}
        lineHeight={lineHeightPx(21)}
        color={mobileChrome.inkMuted}
      >
        {name}
      </Text>
    );
  }
  return (
    <YStack
      testID={PROVENANCE_BADGE_TEST_ID}
      accessibilityLabel={label}
      alignSelf="flex-start"
      paddingHorizontal={8}
      paddingVertical={2}
      backgroundColor={mobileChrome.successWash}
    >
      <Text fontFamily={fontBold} fontSize={14} lineHeight={lineHeightPx(20)} color={mobileChrome.ink}>
        {name}
      </Text>
    </YStack>
  );
}
