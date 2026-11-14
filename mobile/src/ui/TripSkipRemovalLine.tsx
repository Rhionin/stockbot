import { Text, type TextProps } from 'react-native';
import {
  formatStagedCartSkipRemovalLine,
  type FormatStagedCartSkipRemovalOptions,
  type StagedCartSkipRemovalFields,
} from '@stockbot/shared/shoppingListCopy';

export type TripSkipRemovalLineProps = TextProps & {
  skip: StagedCartSkipRemovalFields;
  format?: FormatStagedCartSkipRemovalOptions;
};

export function TripSkipRemovalLine({ skip, format, ...rest }: TripSkipRemovalLineProps) {
  return (
    <Text accessibilityRole="text" testID="trip-skip-removal-line" {...rest}>
      {formatStagedCartSkipRemovalLine(skip, format)}
    </Text>
  );
}
