import { ActivityIndicator, Pressable, Text, View, type ViewStyle } from 'react-native';
import {
  ZERO_FOR_TRIP_NUDGE_OFFLINE,
} from '@stockbot/shared/zeroForTripCopy';
import {
  type RestockNudgeTarget,
  zeroForTripNudgeButtonAria,
  zeroForTripNudgeButtonLabel,
  zeroForTripNudgePrompt,
  shouldShowZeroForTripNudge,
  type RestockSnapshotLike,
} from '@stockbot/shared/zeroForTripNudge';
import type { ShoppingGroupTargetLike } from '@stockbot/shared/shoppingRestockGate';

export type MobileZeroForTripNudgeProps = {
  line: RestockNudgeTarget;
  restock: RestockSnapshotLike | null;
  groups?: ShoppingGroupTargetLike[] | null;
  offline?: boolean;
  loading?: boolean;
  error?: string;
  onDontRestock: () => void;
};

const buttonStyle: ViewStyle = {
  minHeight: 44,
  minWidth: 120,
  justifyContent: 'center',
  alignItems: 'center',
};

const spinnerSlot: ViewStyle = {
  width: 24,
  height: 24,
  justifyContent: 'center',
  alignItems: 'center',
};

export function ZeroForTripNudgeMobile({
  line,
  restock,
  groups,
  offline = false,
  loading = false,
  error = '',
  onDontRestock,
}: MobileZeroForTripNudgeProps) {
  if (!shouldShowZeroForTripNudge(line, restock, groups)) return null;
  const busy = loading || offline;
  return (
    <View testID="zero-for-trip-nudge">
      <Text>{zeroForTripNudgePrompt(line)}</Text>
      <Pressable
        accessibilityRole="button"
        accessibilityLabel={zeroForTripNudgeButtonAria(line)}
        accessibilityState={{ disabled: busy, busy: loading }}
        disabled={busy}
        onPress={onDontRestock}
        testID="zero-for-trip-nudge-button"
        style={buttonStyle}
      >
        <View style={{ flexDirection: 'row', alignItems: 'center', gap: 8 }}>
          <View style={spinnerSlot} importantForAccessibility="no-hide-descendants">
            {loading ? <ActivityIndicator accessible={false} size="small" /> : null}
          </View>
          <Text>{zeroForTripNudgeButtonLabel()}</Text>
        </View>
      </Pressable>
      {error !== '' && <Text accessibilityRole="alert">{error}</Text>}
      {offline && <Text>{ZERO_FOR_TRIP_NUDGE_OFFLINE}</Text>}
    </View>
  );
}