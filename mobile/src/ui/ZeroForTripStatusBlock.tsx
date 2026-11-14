import { Text, View } from 'react-native';
import type { OffListRow } from '@stockbot/shared/types';
import {
  ZERO_FOR_TRIP_STATUS_LABEL,
  formatZeroForTripStatusLine,
  type ZeroForTripCopyFields,
} from '@stockbot/shared/zeroForTripCopy';
import { type RestockNudgeTarget, type RestockSnapshotLike, shouldShowZeroForTripNudge } from '@stockbot/shared/zeroForTripNudge';
import { ZeroForTripNudgeMobile } from './ZeroForTripNudge';

export type MobileZeroForTripStatusBlockProps = {
  row: OffListRow;
  actorFields: ZeroForTripCopyFields;
  restock: RestockSnapshotLike | null;
  nudge?: {
    offline?: boolean;
    loading?: boolean;
    error?: string;
    onDontRestock: () => void;
  };
};

function nudgeLineFromRow(row: OffListRow, productName: string): RestockNudgeTarget {
  return {
    itemId: row.itemId,
    groupId: row.groupId,
    productName,
    groupName: row.groupId ? row.name : undefined,
  };
}

export function ZeroForTripStatusBlockMobile({
  row,
  actorFields,
  restock,
  nudge,
}: MobileZeroForTripStatusBlockProps) {
  const sentence = formatZeroForTripStatusLine(actorFields);
  const line = nudgeLineFromRow(row, row.name);
  const showNudge = nudge != null && shouldShowZeroForTripNudge(line, restock);

  return (
    <View testID="zero-for-trip-status">
      <Text testID="zero-for-trip-status-label">{ZERO_FOR_TRIP_STATUS_LABEL}</Text>
      <Text testID="zero-for-trip-status-sentence">{sentence}</Text>
      {showNudge && (
        <ZeroForTripNudgeMobile
          line={line}
          restock={restock}
          offline={nudge.offline}
          loading={nudge.loading}
          error={nudge.error}
          onDontRestock={nudge.onDontRestock}
        />
      )}
    </View>
  );
}