import { vi } from 'vitest'

vi.mock('react-native', () => ({
  Platform: { OS: 'web', select: (options: { web?: unknown; default?: unknown }) => options.web ?? options.default },
  StyleSheet: { create: (styles: unknown) => styles, flatten: () => ({}) },
  View: 'div',
  Text: 'span',
  Pressable: 'button',
}))

vi.mock('react-native-web', () => ({}))
