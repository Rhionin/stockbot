import { Pressable, View } from 'react-native';
import { brandTealDeep, mobileChrome } from '@stockbot/shared/brandTokens';
import { Text } from '@tamagui/core';
import { YStack } from '@tamagui/stacks';
import { lineHeightPx } from './lineHeight';

export const CHOICE_TEST_ID_PREFIX = 'choice-';

export type Choice = { value: string; label: string };

export type ChoiceListProps = {
  label: string;
  description?: string;
  choices: Choice[];
  value: string;
  disabled?: boolean;
  fontRegular: string;
  fontBold: string;
  testID?: string;
  onChange: (value: string) => void;
};

export function ChoiceList({
  label,
  description,
  choices,
  value,
  disabled = false,
  fontRegular,
  fontBold,
  testID = 'choice-list',
  onChange,
}: ChoiceListProps) {
  return (
    <YStack testID={testID} role="radiogroup" aria-label={label} accessibilityLabel={label} gap={2}>
      <Text fontFamily={fontBold} fontSize={16} lineHeight={lineHeightPx(22)} color={mobileChrome.ink}>
        {label}
      </Text>
      {description ? (
        <Text fontFamily={fontRegular} fontSize={15} lineHeight={lineHeightPx(21)} color={mobileChrome.inkMuted}>
          {description}
        </Text>
      ) : null}
      {choices.map((choice) => {
        const checked = choice.value === value;
        return (
          <Pressable
            key={choice.value === '' ? 'automatic' : choice.value}
            testID={`${CHOICE_TEST_ID_PREFIX}${choice.value === '' ? 'automatic' : choice.value}`}
            role="radio"
            aria-checked={checked}
            accessibilityRole="radio"
            accessibilityState={{ checked, disabled }}
            accessibilityLabel={choice.label}
            disabled={disabled}
            onPress={() => onChange(choice.value)}
            style={{
              minHeight: 48,
              flexDirection: 'row',
              alignItems: 'center',
              gap: 12,
              paddingVertical: 6,
            }}
          >
            <View
              style={{
                width: 24,
                height: 24,
                borderRadius: 12,
                borderWidth: 2,
                borderColor: checked ? brandTealDeep : mobileChrome.inkMuted,
                alignItems: 'center',
                justifyContent: 'center',
              }}
            >
              {checked ? (
                <View style={{ width: 12, height: 12, borderRadius: 6, backgroundColor: brandTealDeep }} />
              ) : null}
            </View>
            <Text
              flex={1}
              fontFamily={checked ? fontBold : fontRegular}
              fontSize={16}
              lineHeight={lineHeightPx(22)}
              color={mobileChrome.ink}
            >
              {choice.label}
            </Text>
          </Pressable>
        );
      })}
    </YStack>
  );
}
