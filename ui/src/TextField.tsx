import { forwardRef, type ComponentRef } from 'react';
import { TextInput, type TextInputProps } from 'react-native';
import { brandTealDeep, mobileChrome } from '@stockbot/shared/brandTokens';
import { Text } from '@tamagui/core';
import { YStack } from '@tamagui/stacks';
import { lineHeightPx } from './lineHeight';

export const TEXT_FIELD_TEST_ID_PREFIX = 'field-';

export type TextFieldHandle = ComponentRef<typeof TextInput>;

export type TextFieldProps = {
  label: string;
  value: string;
  onChangeText: (text: string) => void;
  hint?: string;
  error?: string;
  required?: boolean;
  disabled?: boolean;
  fieldId: string;
  fontRegular: string;
  fontBold: string;
  inputMode?: TextInputProps['inputMode'];
  autoCapitalize?: TextInputProps['autoCapitalize'];
  autoFocus?: boolean;
  autoComplete?: TextInputProps['autoComplete'];
  returnKeyType?: TextInputProps['returnKeyType'];
  placeholder?: string;
  onSubmitEditing?: () => void;
  onBlur?: () => void;
};

export const TextField = forwardRef<TextFieldHandle, TextFieldProps>(function TextField(
  {
    label,
    value,
    onChangeText,
    hint,
    error,
    required = false,
    disabled = false,
    fieldId,
    fontRegular,
    fontBold,
    inputMode,
    autoCapitalize,
    autoFocus,
    autoComplete,
    returnKeyType,
    placeholder,
    onSubmitEditing,
    onBlur,
  },
  ref,
) {
  const invalid = error !== undefined && error !== '';
  const hintId = `${fieldId}-hint`;
  const errorId = `${fieldId}-error`;
  const describedBy = [invalid ? errorId : '', hint ? hintId : ''].filter((id) => id !== '').join(' ');
  const a11y = {
    'aria-invalid': invalid,
    'aria-required': required,
    ...(describedBy === '' ? {} : { 'aria-describedby': describedBy }),
  };
  return (
    <YStack gap={4}>
      <Text fontFamily={fontBold} fontSize={16} lineHeight={lineHeightPx(22)} color={mobileChrome.ink}>
        {required ? `${label} (required)` : label}
      </Text>
      {hint ? (
        <Text
          id={hintId}
          nativeID={hintId}
          fontFamily={fontRegular}
          fontSize={15}
          lineHeight={lineHeightPx(21)}
          color={mobileChrome.inkMuted}
        >
          {hint}
        </Text>
      ) : null}
      <TextInput
        ref={ref}
        testID={`${TEXT_FIELD_TEST_ID_PREFIX}${fieldId}`}
        accessibilityLabel={label}
        accessibilityHint={invalid ? error : undefined}
        accessibilityState={{ disabled }}
        {...a11y}
        value={value}
        editable={!disabled}
        inputMode={inputMode}
        autoCapitalize={autoCapitalize}
        autoFocus={autoFocus}
        autoComplete={autoComplete}
        autoCorrect={false}
        returnKeyType={returnKeyType}
        placeholder={placeholder}
        placeholderTextColor={mobileChrome.inkMuted}
        onChangeText={onChangeText}
        onSubmitEditing={onSubmitEditing}
        onBlur={onBlur}
        style={{
          minHeight: 44,
          paddingHorizontal: 10,
          paddingVertical: 8,
          fontFamily: fontRegular,
          fontSize: 16,
          color: mobileChrome.ink,
          backgroundColor: mobileChrome.white,
          borderWidth: invalid ? 2 : 1,
          borderColor: invalid ? mobileChrome.danger : disabled ? mobileChrome.rule : brandTealDeep,
        }}
      />
      {invalid ? (
        <Text
          id={errorId}
          nativeID={errorId}
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
});
