import { Text } from '@mantine/core';
import {
  formatStagedCartSkipRemovalLine,
  type FormatStagedCartSkipRemovalOptions,
  type StagedCartSkipRemovalFields,
} from '@stockbot/shared/shoppingListCopy';

export type TripSkipRemovalLineProps = {
  skip: StagedCartSkipRemovalFields;
  format?: FormatStagedCartSkipRemovalOptions;
};

export function TripSkipRemovalLine({ skip, format }: TripSkipRemovalLineProps) {
  return (
    <Text size="sm" c="dimmed" data-testid="trip-skip-removal-line">
      {formatStagedCartSkipRemovalLine(skip, format)}
    </Text>
  );
}
