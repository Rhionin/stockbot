import { StyleSheet, Text, View } from 'react-native';
import { UPGRADE_REQUIRED_SENTENCE } from '@stockbot/shared/contract';
import { colors, fonts } from '../ui/theme';

export function UpgradeRequired({ topInset }: { topInset: number }) {
  return (
    <View style={[styles.screen, { paddingTop: topInset + 28 }]}>
      <Text style={styles.sentence} accessibilityRole="alert">{UPGRADE_REQUIRED_SENTENCE}</Text>
    </View>
  );
}

const styles = StyleSheet.create({
  screen: {
    flex: 1,
    backgroundColor: colors.paper,
    paddingHorizontal: 24,
  },
  sentence: {
    fontFamily: fonts.regular,
    fontSize: 18,
    lineHeight: 26,
    color: colors.ink,
    maxWidth: 420,
  },
});
