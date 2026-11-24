import { Pressable } from 'react-native';
import { brandTealDeep, mobileChrome } from '@stockbot/shared/brandTokens';
import { sizeUnits, type NetSizeDraft } from '@stockbot/shared/netSize';
import {
  NET_SIZE_AMOUNT_LABEL,
  NET_SIZE_PACKAGE_HINT,
  NET_SIZE_PACKAGE_LABEL,
  NET_SIZE_PACK_COUNT_HINT,
  NET_SIZE_PACK_COUNT_LABEL,
  NET_SIZE_UNIT_LABEL,
} from '@stockbot/shared/productForm';
import { Text } from '@tamagui/core';
import { XStack, YStack } from '@tamagui/stacks';
import { lineHeightPx } from './lineHeight';
import { TextField } from './TextField';

export const NET_SIZE_UNIT_TEST_ID_PREFIX = 'size-unit-';

export type NetSizeFieldsProps = {
  draft: NetSizeDraft;
  onChange: (draft: NetSizeDraft) => void;
  sizeError?: string;
  packCountError?: string;
  disabled?: boolean;
  idPrefix?: string;
  fontRegular: string;
  fontBold: string;
};

export function NetSizeFields({
  draft,
  onChange,
  sizeError,
  packCountError,
  disabled = false,
  idPrefix = 'size',
  fontRegular,
  fontBold,
}: NetSizeFieldsProps) {
  return (
    <YStack gap={14}>
      <TextField
        fieldId={`${idPrefix}-amount`}
        label={NET_SIZE_AMOUNT_LABEL}
        value={draft.amount}
        inputMode="decimal"
        disabled={disabled}
        error={sizeError}
        fontRegular={fontRegular}
        fontBold={fontBold}
        onChangeText={(amount) => onChange({ ...draft, amount })}
      />
      <YStack role="radiogroup" aria-label={NET_SIZE_UNIT_LABEL} accessibilityLabel={NET_SIZE_UNIT_LABEL} gap={4}>
        <Text fontFamily={fontBold} fontSize={16} lineHeight={lineHeightPx(22)} color={mobileChrome.ink}>
          {NET_SIZE_UNIT_LABEL}
        </Text>
        <XStack flexWrap="wrap" gap={8}>
          {sizeUnits.map((unit) => {
            const checked = unit.value === draft.unit;
            return (
              <Pressable
                key={unit.value}
                testID={`${NET_SIZE_UNIT_TEST_ID_PREFIX}${unit.value}`}
                role="radio"
                aria-checked={checked}
                accessibilityRole="radio"
                accessibilityState={{ checked, disabled }}
                accessibilityLabel={unit.label}
                disabled={disabled}
                onPress={() => onChange({ ...draft, unit: checked ? '' : unit.value })}
                style={{
                  minHeight: 44,
                  minWidth: 56,
                  paddingHorizontal: 12,
                  alignItems: 'center',
                  justifyContent: 'center',
                  borderWidth: 1,
                  borderColor: checked ? brandTealDeep : mobileChrome.rule,
                  backgroundColor: checked ? brandTealDeep : mobileChrome.white,
                }}
              >
                <Text
                  fontFamily={checked ? fontBold : fontRegular}
                  fontSize={16}
                  lineHeight={lineHeightPx(22)}
                  color={checked ? mobileChrome.white : mobileChrome.ink}
                >
                  {unit.label}
                </Text>
              </Pressable>
            );
          })}
        </XStack>
      </YStack>
      <TextField
        fieldId={`${idPrefix}-package`}
        label={NET_SIZE_PACKAGE_LABEL}
        hint={NET_SIZE_PACKAGE_HINT}
        value={draft.packageWord}
        disabled={disabled}
        autoCapitalize="none"
        fontRegular={fontRegular}
        fontBold={fontBold}
        onChangeText={(packageWord) => onChange({ ...draft, packageWord })}
      />
      <TextField
        fieldId={`${idPrefix}-pack-count`}
        label={NET_SIZE_PACK_COUNT_LABEL}
        hint={NET_SIZE_PACK_COUNT_HINT}
        value={draft.packCount}
        inputMode="numeric"
        disabled={disabled}
        error={packCountError}
        fontRegular={fontRegular}
        fontBold={fontBold}
        onChangeText={(packCount) => onChange({ ...draft, packCount })}
      />
    </YStack>
  );
}
