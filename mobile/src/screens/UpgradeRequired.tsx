import { StyleSheet, Text, View } from 'react-native';
import { UPGRADE_REQUIRED_SENTENCE } from '@stockbot/shared/contract';
import { FORM_COLUMN_MAX_WIDTH, useColumn } from '../ui/layout';
import { colors, fonts } from '../ui/theme';

export function UpgradeRequired({ topInset }: { topInset: number }) {
  const column = useColumn(FORM_COLUMN_MAX_WIDTH);
  return (
    <View style={[styles.screen, column, { paddingTop: topInset + 28 }]}>
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
