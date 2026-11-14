import { Badge, Button, Stack, Text } from '@mantine/core';
import type { OffListRow } from '@stockbot/shared/types';
import {
  ZERO_FOR_TRIP_CHANGE_AMOUNT_LABEL,
  ZERO_FOR_TRIP_STATUS_LABEL,
  formatZeroForTripStatusLine,
  type ZeroForTripCopyFields,
} from '@stockbot/shared/zeroForTripCopy';
import { shouldShowZeroForTripNudge, type RestockNudgeTarget } from '@stockbot/shared/zeroForTripNudge';
import type { RestockSnapshot } from '../../restock/types';
import { ZeroForTripNudge, type ZeroForTripNudgeProps } from './ZeroForTripNudge';

export type ZeroForTripStatusBlockProps = {
  row: OffListRow;
  actorFields: ZeroForTripCopyFields;
  restock: RestockSnapshot | null;
  onFocusChangeAmount?: () => void;
  nudge?: Pick<ZeroForTripNudgeProps, 'offline' | 'loading' | 'error' | 'onDontRestock'>;
};

function nudgeLineFromRow(row: OffListRow, productName: string): RestockNudgeTarget {
  return {
    itemId: row.itemId,
    groupId: row.groupId,
    productName,
    groupName: row.groupId ? row.name : undefined,
  };
}

export function ZeroForTripStatusBlock({
  row,
  actorFields,
  restock,
  onFocusChangeAmount,
  nudge,
}: ZeroForTripStatusBlockProps) {
  const productName = row.name;
  const sentence = formatZeroForTripStatusLine(actorFields);
  const line = nudgeLineFromRow(row, productName);
  const showNudge = nudge != null && shouldShowZeroForTripNudge(line, restock);

  return (
    <Stack gap="xs" className="zero-for-trip-status-block">
      <Badge variant="light" className="zero-for-trip-status-label">
        {ZERO_FOR_TRIP_STATUS_LABEL}
      </Badge>
      <Text size="sm" className="zero-for-trip-status-sentence">
        {sentence}
      </Text>
      {onFocusChangeAmount && (
        <Button
          type="button"
          size="compact-sm"
          variant="light"
          className="zero-for-trip-change-amount"
          onClick={onFocusChangeAmount}
        >
          {ZERO_FOR_TRIP_CHANGE_AMOUNT_LABEL}
        </Button>
      )}
      {showNudge && (
        <ZeroForTripNudge
          line={line}
          offline={nudge.offline}
          loading={nudge.loading}
          error={nudge.error}
          onDontRestock={nudge.onDontRestock}
        />
      )}
    </Stack>
  );
}