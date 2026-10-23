import { Pressable, StyleSheet, Text, View } from 'react-native';
import type { BottomTabBarProps } from 'expo-router/tabs';
import { colors, fonts } from './theme';

export function ShelfTabBar({ state, descriptors, emitter, navigateToTab, insets }: BottomTabBarProps) {
  return (
    <View style={[styles.bar, { paddingBottom: Math.max(insets.bottom, 10) }]}>
      {state.routes.map((route, index) => {
        const focused = state.index === index;
        const options = descriptors[route.key]?.options;
        const title = options?.title ?? route.name;
        const label = typeof title === 'string' ? title : route.name;
        return (
          <Pressable
            key={route.key}
            accessibilityRole="tab"
            accessibilityState={{ selected: focused }}
            accessibilityLabel={label}
            onPress={() => {
              const event = emitter.emit({ type: 'tabPress', target: route.key, canPreventDefault: true });
              if (!focused && !event.defaultPrevented) {
                navigateToTab(route.key);
              }
            }}
            style={styles.tab}
          >
            <Text style={[styles.label, focused ? styles.labelActive : null]}>{label}</Text>
            <View style={[styles.mark, focused ? styles.markActive : null]} />
          </Pressable>
        );
      })}
    </View>
  );
}

const styles = StyleSheet.create({
  bar: {
    flexDirection: 'row',
    backgroundColor: colors.paper,
    borderTopWidth: 1,
    borderTopColor: colors.rule,
    paddingTop: 8,
  },
  tab: {
    flex: 1,
    alignItems: 'center',
    minHeight: 48,
    justifyContent: 'center',
    gap: 6,
  },
  label: {
    fontFamily: fonts.regular,
    fontSize: 16,
    color: colors.inkMuted,
  },
  labelActive: {
    fontFamily: fonts.bold,
    color: colors.ink,
  },
  mark: {
    width: 22,
    height: 3,
    backgroundColor: 'transparent',
  },
  markActive: {
    backgroundColor: colors.shelf,
  },
});
