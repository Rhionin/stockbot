import { useSyncExternalStore } from 'react';
import { StyleSheet, Text, View } from 'react-native';
import { OFFLINE_SENTENCE, offlineBannerActive, subscribeOfflineBanner } from '../../../frontend/src/api/contract';
import { colors, fonts } from './theme';

// Same red line as the queue's batch banner. Other screens use this so an
// offline startup or a failed load shows one notice at the top.
export function ServerNoticeBanner() {
  const text = useSyncExternalStore(
    subscribeOfflineBanner,
    () => (offlineBannerActive() ? OFFLINE_SENTENCE : ''),
    () => '',
  );
  if (text === '') return null;
  return (
    <View testID="server-notice-banner" accessibilityLiveRegion="polite">
      <Text style={styles.sentence}>{text}</Text>
    </View>
  );
}

const styles = StyleSheet.create({
  sentence: {
    fontFamily: fonts.regular,
    fontSize: 16,
    lineHeight: 22,
    color: colors.danger,
    backgroundColor: colors.paper,
    marginBottom: 12,
  },
});
