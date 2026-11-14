import { NumberInput, Stack, Text } from '@mantine/core';
import {
  ZERO_FOR_TRIP_CHANGE_AMOUNT_LABEL,
  ZERO_FOR_TRIP_INLINE_CONFIRM,
} from '@stockbot/shared/zeroForTripCopy';
import { shouldShowZeroForTripNudge, type RestockNudgeTarget } from '@stockbot/shared/zeroForTripNudge';
import type { RestockSnapshot } from '../../restock/types';
import { ZeroForTripNudge, type ZeroForTripNudgeProps } from './ZeroForTripNudge';

export type ChangeAmountWithSkipConfirmProps = {
  productName: string;
  entryId: string;
  line: RestockNudgeTarget;
  restock: RestockSnapshot | null;
  defaultQuantity: number;
  inputKey: string;
  showInlineConfirm: boolean;
  onBlurSave: (value: number | string) => void;
  nudge?: Pick<ZeroForTripNudgeProps, 'offline' | 'loading' | 'error' | 'onDontRestock'>;
};

export function ChangeAmountWithSkipConfirm({
  productName,
  entryId,
  line,
  restock,
  defaultQuantity,
  inputKey,
  showInlineConfirm,
  onBlurSave,
  nudge,
}: ChangeAmountWithSkipConfirmProps) {
  const showNudge = showInlineConfirm && nudge != null && shouldShowZeroForTripNudge(line, restock);

  return (
    <Stack gap={6} className="zero-for-trip-change-amount-block">
      <NumberInput
        id={`change-amount-${entryId}`}
        size="xs"
        label={ZERO_FOR_TRIP_CHANGE_AMOUNT_LABEL}
        aria-label={`${ZERO_FOR_TRIP_CHANGE_AMOUNT_LABEL} for ${productName}`}
        min={0}
        max={999}
        step={1}
        allowDecimal={false}
        defaultValue={defaultQuantity}
        key={inputKey}
        onBlur={(event) => onBlurSave(event.currentTarget.value)}
        w={120}
        className="zero-for-trip-change-amount-input"
      />
      {showInlineConfirm && (
        <Text size="sm" role="status" className="zero-for-trip-inline-confirm">
          {ZERO_FOR_TRIP_INLINE_CONFIRM}
        </Text>
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