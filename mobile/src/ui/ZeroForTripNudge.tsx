import { Pressable, Text, View } from 'react-native';
import { OFFLINE_SENTENCE } from '@stockbot/shared/contract';
import {
  type RestockNudgeTarget,
  zeroForTripNudgeButtonAria,
  zeroForTripNudgeButtonLabel,
  zeroForTripNudgePrompt,
  shouldShowZeroForTripNudge,
  type RestockSnapshotLike,
} from '@stockbot/shared/zeroForTripNudge';

export type MobileZeroForTripNudgeProps = {
  line: RestockNudgeTarget;
  restock: RestockSnapshotLike | null;
  offline?: boolean;
  loading?: boolean;
  error?: string;
  onDontRestock: () => void;
};

export function ZeroForTripNudgeMobile({
  line,
  restock,
  offline = false,
  loading = false,
  error = '',
  onDontRestock,
}: MobileZeroForTripNudgeProps) {
  if (!shouldShowZeroForTripNudge(line, restock)) return null;
  const busy = loading || offline;
  return (
    <View testID="zero-for-trip-nudge">
      <Text>{zeroForTripNudgePrompt(line)}</Text>
      <Pressable
        accessibilityRole="button"
        accessibilityLabel={zeroForTripNudgeButtonAria(line)}
        accessibilityState={{ disabled: busy }}
        disabled={busy}
        onPress={onDontRestock}
        testID="zero-for-trip-nudge-button"
      >
        <Text>{loading ? '…' : zeroForTripNudgeButtonLabel()}</Text>
      </Pressable>
      {error !== '' && <Text accessibilityRole="alert">{error}</Text>}
      {offline && <Text>{OFFLINE_SENTENCE}</Text>}
    </View>
  );
}