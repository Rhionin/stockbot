import { Button, Group, Loader, Text } from '@mantine/core';
import { OFFLINE_SENTENCE } from '@stockbot/shared/contract';
import {
  type RestockNudgeTarget,
  zeroForTripNudgeButtonAria,
  zeroForTripNudgeButtonLabel,
  zeroForTripNudgePrompt,
} from '@stockbot/shared/zeroForTripNudge';

export type ZeroForTripNudgeProps = {
  line: RestockNudgeTarget;
  offline?: boolean;
  loading?: boolean;
  error?: string;
  onDontRestock: () => void;
  className?: string;
};

export function ZeroForTripNudge({
  line,
  offline = false,
  loading = false,
  error = '',
  onDontRestock,
  className,
}: ZeroForTripNudgeProps) {
  const prompt = zeroForTripNudgePrompt(line);
  const busy = loading || offline;

  return (
    <Group
      gap="xs"
      align="center"
      wrap="wrap"
      className={className ?? 'zero-for-trip-nudge'}
      data-offline={offline ? 'true' : undefined}
    >
      <Text size="sm" className="zero-for-trip-nudge-prompt">
        {prompt}
      </Text>
      <Button
        type="button"
        size="compact-sm"
        className="zero-for-trip-nudge-button"
        aria-label={zeroForTripNudgeButtonAria(line)}
        disabled={busy}
        onClick={onDontRestock}
      >
        <span className="zero-for-trip-nudge-button-label">
          {loading ? <Loader size="xs" className="zero-for-trip-nudge-spinner" aria-hidden /> : zeroForTripNudgeButtonLabel()}
        </span>
      </Button>
      {error !== '' && (
        <Text role="alert" size="sm" c="red" className="zero-for-trip-nudge-error">
          {error}
        </Text>
      )}
      {offline && (
        <Text size="sm" c="dimmed" className="zero-for-trip-nudge-offline">
          {OFFLINE_SENTENCE}
        </Text>
      )}
    </Group>
  );
}