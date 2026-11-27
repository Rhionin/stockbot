import { Badge, Stack, Text } from '@mantine/core';
import type { OffListRow } from '@stockbot/shared/types';
import type { ShoppingGroupTargetLike } from '@stockbot/shared/shoppingRestockGate';
import {
  ZERO_FOR_TRIP_STATUS_LABEL,
  formatZeroForTripStatusLine,
  zeroForTripDisplayName,
  type ZeroForTripCopyFields,
} from '@stockbot/shared/zeroForTripCopy';
import { shouldShowZeroForTripNudge, type RestockNudgeTarget } from '@stockbot/shared/zeroForTripNudge';
import type { RestockSnapshot } from '../../restock/types';
import { ChangeAmountWithSkipConfirm } from './ChangeAmountWithSkipConfirm';
import { ZeroForTripNudge, type ZeroForTripNudgeProps } from './ZeroForTripNudge';

export type ZeroForTripStatusBlockProps = {
  row: OffListRow;
  actorFields: ZeroForTripCopyFields;
  restock: RestockSnapshot | null;
  groups?: ShoppingGroupTargetLike[] | null;
  changeAmount?: {
    entryId: string;
    defaultQuantity: number;
    inputKey: string;
    productName: string;
    onBlurSave: (value: number | string) => void;
  };
  nudge?: Pick<ZeroForTripNudgeProps, 'offline' | 'loading' | 'error' | 'onDontRestock'>;
};

function nudgeLineFromRow(row: OffListRow, productName: string): RestockNudgeTarget {
  const groupName = row.groupId ? row.name : undefined;
  return {
    itemId: row.itemId,
    groupId: row.groupId,
    productName,
    groupName,
  };
}

export function ZeroForTripStatusBlock({
  row,
  actorFields,
  restock,
  groups,
  changeAmount,
  nudge,
}: ZeroForTripStatusBlockProps) {
  const productName = zeroForTripDisplayName(row.name);
  const sentence = formatZeroForTripStatusLine(actorFields);
  const line = nudgeLineFromRow(row, productName);
  const showNudge = nudge != null && shouldShowZeroForTripNudge(line, restock, groups);

  return (
    <Stack gap="xs" className="zero-for-trip-status-block">
      <Badge variant="light" className="zero-for-trip-status-label">
        {ZERO_FOR_TRIP_STATUS_LABEL}
      </Badge>
      <Text size="sm" className="zero-for-trip-status-sentence">
        {sentence}
      </Text>
      {changeAmount != null && (
        <ChangeAmountWithSkipConfirm
          entryId={changeAmount.entryId}
          productName={changeAmount.productName}
          line={line}
          restock={restock}
          groups={groups}
          defaultQuantity={changeAmount.defaultQuantity}
          inputKey={changeAmount.inputKey}
          showInlineConfirm
          onBlurSave={changeAmount.onBlurSave}
          nudge={showNudge ? nudge : undefined}
        />
      )}
      {changeAmount == null && showNudge && (
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