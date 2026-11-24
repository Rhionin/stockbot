import { Pressable, View } from 'react-native';
import { brandTealDeep, mobileChrome } from '@stockbot/shared/brandTokens';
import { Text } from '@tamagui/core';
import { lineHeightPx } from './lineHeight';

export const SELECT_CHECKBOX_TEST_ID_PREFIX = 'select-';

export type SelectCheckboxProps = {
  // The accessible name. Needs the scan or session so a list of them is not "Select, Select".
  label: string;
  checked: boolean | 'mixed';
  disabled?: boolean;
  visibleLabel?: string;
  testID?: string;
  fontRegular: string;
  onChange: (next: boolean) => void;
};

export function SelectCheckbox({
  label,
  checked,
  disabled = false,
  visibleLabel,
  testID,
  fontRegular,
  onChange,
}: SelectCheckboxProps) {
  const on = checked !== false;
  return (
    <Pressable
      testID={testID}
      role="checkbox"
      aria-checked={checked}
      accessibilityRole="checkbox"
      accessibilityState={{ checked, disabled }}
      accessibilityLabel={label}
      disabled={disabled}
      onPress={() => onChange(checked !== true)}
      style={{
        minHeight: 44,
        minWidth: 44,
        flexDirection: 'row',
        alignItems: 'center',
        justifyContent: 'center',
        gap: 8,
        opacity: disabled ? 0.45 : 1,
      }}
    >
      <View
        style={{
          width: 24,
          height: 24,
          borderWidth: 2,
          borderRadius: 4,
          borderColor: on ? brandTealDeep : mobileChrome.inkMuted,
          backgroundColor: on ? brandTealDeep : mobileChrome.white,
          alignItems: 'center',
          justifyContent: 'center',
        }}
      >
        {on ? (
          <Text fontSize={16} lineHeight={lineHeightPx(16)} color={mobileChrome.white} aria-hidden>
            {checked === 'mixed' ? '–' : '✓'}
          </Text>
        ) : null}
      </View>
      {visibleLabel ? (
        <Text fontFamily={fontRegular} fontSize={16} lineHeight={lineHeightPx(22)} color={mobileChrome.ink}>
          {visibleLabel}
        </Text>
      ) : null}
    </Pressable>
  );
}
