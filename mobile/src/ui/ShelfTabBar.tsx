import { Pressable, StyleSheet, Text, View } from 'react-native';
import type { BottomTabBarProps } from 'expo-router/tabs';
import { colors, fonts } from './theme';
import { DESKTOP_NAV_HEIGHT, PAGE_COLUMN_MAX_WIDTH, useDesktopLayout } from './layout';

type HoverState = { hovered?: boolean };

export function ShelfTabBar({ state, descriptors, emitter, navigateToTab, insets }: BottomTabBarProps) {
  const desktop = useDesktopLayout();
  const tabs = state.routes.flatMap((route, index) => {
    const options = descriptors[route.key]?.options;
    return options?.hidden === true ? [] : [{ route, index, options }];
  });

  const items = tabs.map(({ route, index, options }) => {
    const focused = state.index === index;
    const title = options?.title ?? route.name;
    const label = typeof title === 'string' ? title : route.name;
    const onPress = () => {
      const event = emitter.emit({ type: 'tabPress', target: route.key, canPreventDefault: true });
      if (!focused && !event.defaultPrevented) {
        navigateToTab(route.key);
      }
    };
    return { key: route.key, focused, label, onPress };
  });

  if (desktop) {
    return (
      <View role="navigation" aria-label="Sections" style={styles.topFrame}>
        <View style={styles.topInner}>
          <View style={styles.brand}>
            <View style={styles.brandMark} />
            <Text style={styles.brandName}>Stockbot</Text>
          </View>
          <View role="tablist" style={styles.topTabs}>
            {items.map((item) => (
              <Pressable
                key={item.key}
                accessibilityRole="tab"
                accessibilityState={{ selected: item.focused }}
                aria-selected={item.focused}
                accessibilityLabel={item.label}
                onPress={item.onPress}
                style={(state) => [styles.topTab, (state as HoverState).hovered && !item.focused ? styles.topTabHover : null]}
              >
                <Text style={[styles.topLabel, item.focused ? styles.labelActive : null]}>{item.label}</Text>
                <View style={[styles.topMark, item.focused ? styles.markActive : null]} />
              </Pressable>
            ))}
          </View>
        </View>
      </View>
    );
  }

  return (
    <View role="navigation" style={[styles.frame, { paddingBottom: Math.max(insets.bottom, 10) }]}>
      <View role="tablist" style={styles.bar}>
        {items.map((item) => (
          <Pressable
            key={item.key}
            accessibilityRole="tab"
            accessibilityState={{ selected: item.focused }}
            aria-selected={item.focused}
            accessibilityLabel={item.label}
            onPress={item.onPress}
            style={styles.tab}
          >
            <Text style={[styles.label, item.focused ? styles.labelActive : null]}>{item.label}</Text>
            <View style={[styles.mark, item.focused ? styles.markActive : null]} />
          </Pressable>
        ))}
      </View>
    </View>
  );
}

const styles = StyleSheet.create({
  topFrame: {
    backgroundColor: colors.paper,
    borderBottomWidth: 1,
    borderBottomColor: colors.rule,
  },
  topInner: {
    width: '100%',
    maxWidth: PAGE_COLUMN_MAX_WIDTH,
    alignSelf: 'center',
    height: DESKTOP_NAV_HEIGHT,
    paddingHorizontal: 20,
    flexDirection: 'row',
    alignItems: 'center',
    gap: 36,
  },
  brand: {
    flexDirection: 'row',
    alignItems: 'center',
    gap: 10,
  },
  brandMark: {
    width: 6,
    height: 26,
    backgroundColor: colors.shelf,
  },
  brandName: {
    fontFamily: fonts.bold,
    fontSize: 22,
    lineHeight: 28,
    color: colors.ink,
  },
  topTabs: {
    flexDirection: 'row',
    alignSelf: 'stretch',
    gap: 8,
  },
  topTab: {
    alignItems: 'center',
    justifyContent: 'flex-end',
    paddingHorizontal: 14,
    gap: 0,
  },
  topTabHover: {
    backgroundColor: colors.successWash,
  },
  topLabel: {
    fontFamily: fonts.regular,
    fontSize: 17,
    lineHeight: 24,
    color: colors.inkMuted,
    paddingVertical: 17,
  },
  topMark: {
    alignSelf: 'stretch',
    height: 4,
    backgroundColor: 'transparent',
  },
  frame: {
    backgroundColor: colors.paper,
    borderTopWidth: 1,
    borderTopColor: colors.rule,
    paddingTop: 8,
  },
  bar: {
    flexDirection: 'row',
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
