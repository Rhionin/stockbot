/**
 * Jest-only shim: Tamagui 3 native-test hits missing usePressability under jest-expo.
 * Exercises @stockbot/ui layout and press wiring without pulling the full native runtime.
 */
import type { ReactNode } from 'react';
import { Pressable, Text as RNText, View, type PressableProps, type TextProps as RNTextProps, type ViewProps, type ViewStyle } from 'react-native';

export const createTokens = <T,>(tokens: T): T => tokens;

export const createTamagui = <T,>(config: T): T => config;

export function TamaguiProvider({ children }: { children: ReactNode }) {
  return <>{children}</>;
}

export function Theme({ children }: { children: ReactNode }) {
  return <>{children}</>;
}

type StackProps = ViewProps & {
  children?: ReactNode;
  gap?: number;
  testID?: string;
  backgroundColor?: string;
  borderColor?: string;
  borderWidth?: number;
  borderRadius?: number;
  paddingVertical?: number;
  paddingHorizontal?: number;
  alignItems?: ViewStyle['alignItems'];
};

export function XStack(props: StackProps) {
  const {
    children,
    gap,
    backgroundColor,
    borderColor,
    borderWidth,
    borderRadius,
    paddingVertical,
    paddingHorizontal,
    alignItems,
    style,
    ...rest
  } = props;
  const stackStyle: ViewStyle[] = [
    {
      flexDirection: 'row',
      gap,
      backgroundColor,
      borderColor,
      borderWidth,
      borderRadius,
      paddingVertical,
      paddingHorizontal,
      alignItems,
    },
    ...(style ? [style as ViewStyle] : []),
  ];
  return (
    <View {...rest} style={stackStyle}>
      {children}
    </View>
  );
}

type TextProps = RNTextProps & { children?: ReactNode; color?: string };

export function Text({ color, style, ...props }: TextProps) {
  return <RNText {...props} style={[{ color }, style]} />;
}

type ButtonProps = {
  children?: ReactNode;
  onPress?: () => void;
  accessibilityRole?: string;
  role?: string;
  'aria-label'?: string;
  testID?: string;
  style?: unknown;
};

export function Button({ children, onPress, accessibilityRole, role, 'aria-label': ariaLabel, style }: ButtonProps) {
  return (
    <Pressable
      accessibilityRole={(accessibilityRole ?? role ?? 'button') as 'button'}
      accessibilityLabel={ariaLabel}
      onPress={onPress}
      style={style as PressableProps['style']}
    >
      <RNText>{children}</RNText>
    </Pressable>
  );
}
