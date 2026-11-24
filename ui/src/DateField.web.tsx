import { useEffect, useRef } from 'react';
import { mobileChrome } from '@stockbot/shared/brandTokens';
import type { DateFieldProps } from './dateFieldProps';

export { DATE_FIELD_TEST_ID } from './DateField';

export function DateField({
  value,
  label,
  invalid = false,
  autoFocus = false,
  fontRegular,
  testID = 'date-field',
  onChangeText,
  onCommit,
}: DateFieldProps) {
  const ref = useRef<HTMLInputElement>(null);
  useEffect(() => {
    if (autoFocus) ref.current?.focus();
  }, [autoFocus]);
  return (
    <input
      ref={ref}
      type="date"
      data-testid={testID}
      aria-label={label}
      aria-invalid={invalid}
      value={value}
      onChange={(event) => onChangeText(event.currentTarget.value)}
      onBlur={onCommit}
      style={{
        boxSizing: 'border-box',
        minHeight: 44,
        minWidth: 160,
        padding: '0 10px',
        fontFamily: fontRegular,
        fontSize: 16,
        color: invalid ? mobileChrome.danger : mobileChrome.ink,
        backgroundColor: mobileChrome.white,
        border: `1px solid ${invalid ? mobileChrome.danger : mobileChrome.rule}`,
        borderRadius: 0,
      }}
    />
  );
}
