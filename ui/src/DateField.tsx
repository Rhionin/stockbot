import { TextInput } from 'react-native';
import { mobileChrome } from '@stockbot/shared/brandTokens';
import { DATE_FIELD_PLACEHOLDER, type DateFieldProps } from './dateFieldProps';

export const DATE_FIELD_TEST_ID = 'date-field';

// Native has no date picker module, so this is a typed YYYY-MM-DD field. The
// caller validates the text; the web build swaps in a native date input.
export function DateField({
  value,
  label,
  invalid = false,
  autoFocus = false,
  fontRegular,
  testID = DATE_FIELD_TEST_ID,
  onChangeText,
  onCommit,
}: DateFieldProps) {
  return (
    <TextInput
      testID={testID}
      accessibilityLabel={label}
      aria-invalid={invalid}
      value={value}
      autoFocus={autoFocus}
      placeholder={DATE_FIELD_PLACEHOLDER}
      placeholderTextColor={mobileChrome.inkMuted}
      keyboardType="numbers-and-punctuation"
      autoCorrect={false}
      autoCapitalize="none"
      maxLength={10}
      returnKeyType="done"
      onChangeText={onChangeText}
      onBlur={onCommit}
      onSubmitEditing={onCommit}
      style={{
        minHeight: 44,
        minWidth: 144,
        paddingHorizontal: 10,
        fontFamily: fontRegular,
        fontSize: 16,
        color: invalid ? mobileChrome.danger : mobileChrome.ink,
        backgroundColor: mobileChrome.white,
        borderWidth: 1,
        borderColor: invalid ? mobileChrome.danger : mobileChrome.rule,
      }}
    />
  );
}
