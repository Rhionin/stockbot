import { ActivityIndicator, Pressable, View } from 'react-native';
import { brandTealDeep, mobileChrome } from '@stockbot/shared/brandTokens';
import { Text } from '@tamagui/core';
import { lineHeightPx } from './lineHeight';

export type ActionButtonProps = {
  label: string;
  variant?: 'primary' | 'secondary';
  busy?: boolean;
  disabled?: boolean;
  fontBold: string;
  testID?: string;
  onPress: () => void;
};

export function ActionButton({
  label,
  variant = 'primary',
  busy = false,
  disabled = false,
  fontBold,
  testID,
  onPress,
}: ActionButtonProps) {
  const primary = variant === 'primary';
  const off = disabled || busy;
  const ink = primary ? mobileChrome.white : brandTealDeep;
  return (
    <Pressable
      testID={testID}
      accessibilityRole="button"
      accessibilityLabel={label}
      accessibilityState={{ disabled: off, busy }}
      disabled={off}
      onPress={onPress}
      style={{
        minHeight: primary ? 52 : 48,
        flexDirection: 'row',
        alignItems: 'center',
        justifyContent: 'center',
        gap: 8,
        paddingHorizontal: 16,
        backgroundColor: primary ? brandTealDeep : mobileChrome.white,
        borderWidth: primary ? 0 : 2,
        borderColor: brandTealDeep,
        opacity: disabled && !busy ? 0.5 : 1,
      }}
    >
      {busy ? (
        <View importantForAccessibility="no-hide-descendants">
          <ActivityIndicator accessible={false} color={ink} size="small" />
        </View>
      ) : null}
      <Text fontFamily={fontBold} fontSize={primary ? 18 : 17} lineHeight={lineHeightPx(24)} color={ink}>
        {label}
      </Text>
    </Pressable>
  );
}
