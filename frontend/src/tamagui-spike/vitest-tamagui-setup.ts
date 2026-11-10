import { createElement, type ReactNode } from 'react'
import { vi } from 'vitest'

vi.mock('react-native', () => ({
  Platform: { OS: 'web', select: (options: { web?: unknown; default?: unknown }) => options.web ?? options.default },
  StyleSheet: { create: (styles: unknown) => styles, flatten: () => ({}) },
  View: 'div',
  Text: 'span',
  Pressable: ({
    children,
    onPress,
    accessibilityRole,
    accessibilityLabel,
    style,
  }: {
    children?: ReactNode;
    onPress?: () => void;
    accessibilityRole?: string;
    accessibilityLabel?: string;
    style?: unknown;
  }) =>
    createElement(
      'button',
      {
        type: 'button',
        role: accessibilityRole ?? 'button',
        'aria-label': accessibilityLabel,
        onClick: onPress,
        style,
      },
      children,
    ),
}))

vi.mock('react-native-web', () => ({}))
