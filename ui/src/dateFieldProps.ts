export type DateFieldProps = {
  value: string;
  label: string;
  invalid?: boolean;
  autoFocus?: boolean;
  fontRegular: string;
  testID?: string;
  onChangeText: (text: string) => void;
  onCommit: () => void;
};

export const DATE_FIELD_PLACEHOLDER = 'YYYY-MM-DD';
