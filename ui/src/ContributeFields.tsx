import { Switch } from 'react-native';
import { brandTealDeep, mobileChrome } from '@stockbot/shared/brandTokens';
import { DATABASE_NAMES } from '@stockbot/shared/databaseNames';
import {
  CONTRIBUTE_CHECKBOX_LABEL,
  CONTRIBUTE_CONFIGURED_NOTE,
  CONTRIBUTE_DATABASE_LABEL,
  CONTRIBUTE_OFF_NOTE,
  CONTRIBUTE_SWITCH_LABEL,
  CONTRIBUTE_UNCONFIGURED_NOTE,
  CONTRIBUTE_UPSTREAM_NOTE,
  type ContributeChoice,
} from '@stockbot/shared/productForm';
import type { ExternalSource } from '@stockbot/shared/types';
import { Text } from '@tamagui/core';
import { XStack, YStack } from '@tamagui/stacks';
import { ChoiceList } from './ChoiceList';
import { lineHeightPx } from './lineHeight';
import { SelectCheckbox } from './SelectCheckbox';

export const CONTRIBUTE_SWITCH_TEST_ID = 'contribute-switch';

const DATABASES = (Object.keys(DATABASE_NAMES) as ExternalSource[]).map((value) => ({
  value,
  label: DATABASE_NAMES[value],
}));

export type ContributeFieldsProps = {
  choice: ContributeChoice;
  // The household setting. The product checkbox only shows while it is on.
  enabled: boolean;
  configured: boolean;
  error?: string;
  // Upstream products stay in the pantry, so only the household switch shows.
  allowProductOptIn?: boolean;
  disabled?: boolean;
  fontRegular: string;
  fontBold: string;
  onChoice: (choice: ContributeChoice) => void;
  onToggleEnabled: (next: boolean) => void;
};

export function ContributeFields({
  choice,
  enabled,
  configured,
  error,
  allowProductOptIn = true,
  disabled = false,
  fontRegular,
  fontBold,
  onChoice,
  onToggleEnabled,
}: ContributeFieldsProps) {
  const note = (text: string) => (
    <Text fontFamily={fontRegular} fontSize={15} lineHeight={lineHeightPx(21)} color={mobileChrome.inkMuted}>
      {text}
    </Text>
  );
  return (
    <YStack gap={8}>
      <XStack alignItems="center" gap={12} minHeight={44}>
        <Switch
          testID={CONTRIBUTE_SWITCH_TEST_ID}
          accessibilityLabel={CONTRIBUTE_SWITCH_LABEL}
          value={enabled}
          disabled={disabled}
          trackColor={{ false: mobileChrome.rule, true: brandTealDeep }}
          thumbColor={mobileChrome.white}
          onValueChange={onToggleEnabled}
        />
        <Text flex={1} fontFamily={fontBold} fontSize={16} lineHeight={lineHeightPx(22)} color={mobileChrome.ink}>
          {CONTRIBUTE_SWITCH_LABEL}
        </Text>
      </XStack>
      {note(CONTRIBUTE_OFF_NOTE)}
      {enabled && allowProductOptIn ? (
        <>
          {note(configured ? CONTRIBUTE_CONFIGURED_NOTE : CONTRIBUTE_UNCONFIGURED_NOTE)}
          <SelectCheckbox
            testID="contribute-product"
            label={CONTRIBUTE_CHECKBOX_LABEL}
            visibleLabel={CONTRIBUTE_CHECKBOX_LABEL}
            checked={choice.contribute}
            disabled={disabled}
            fontRegular={fontRegular}
            onChange={(next) => onChoice({ ...choice, contribute: next })}
          />
          {choice.contribute ? (
            <ChoiceList
              label={CONTRIBUTE_DATABASE_LABEL}
              choices={DATABASES}
              value={choice.contributeTo}
              disabled={disabled}
              fontRegular={fontRegular}
              fontBold={fontBold}
              testID="contribute-database"
              onChange={(value) => onChoice({ ...choice, contributeTo: value as ExternalSource })}
            />
          ) : null}
        </>
      ) : null}
      {enabled && !allowProductOptIn ? note(CONTRIBUTE_UPSTREAM_NOTE) : null}
      {error ? (
        <Text
          role="alert"
          accessibilityRole="alert"
          fontFamily={fontBold}
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
