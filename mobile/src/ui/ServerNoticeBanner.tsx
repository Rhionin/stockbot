import { useSyncExternalStore } from 'react';
import { StyleSheet, Text, View } from 'react-native';
import { serverNoticeText, subscribeOfflineBanner } from '../../../frontend/src/api/contract';
import { fonts } from './theme';

// Brand red, boxed, pinned under the status bar. Offline, busy, and no_space
// share this one banner so every tab looks the same.
export const SERVER_NOTICE_RED = '#EF4444';

export function ServerNoticeBanner({ topInset = 0 }: { topInset?: number }) {
  const text = useSyncExternalStore(subscribeOfflineBanner, serverNoticeText, () => '');
  if (text === '') return null;
  return (
    <View
      testID="server-notice-banner"
      accessibilityLiveRegion="polite"
      style={[styles.banner, { marginTop: topInset }]}
    >
      <Text style={styles.sentence}>{text}</Text>
    </View>
  );
}

const styles = StyleSheet.create({
  banner: {
    backgroundColor: SERVER_NOTICE_RED,
    borderRadius: 4,
    paddingVertical: 8,
    paddingHorizontal: 12,
    marginBottom: 12,
  },
  sentence: {
    fontFamily: fonts.regular,
    fontSize: 16,
    lineHeight: 22,
    color: '#FFFFFF',
  },
});
