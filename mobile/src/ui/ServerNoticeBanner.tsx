import { useSyncExternalStore } from 'react';
import { StyleSheet, Text, View } from 'react-native';
import { serverNoticeText, subscribeOfflineBanner } from '../../../frontend/src/api/contract';
import {
  NOTICE_BANNER_EDGE,
  NOTICE_BANNER_RADIUS,
  NOTICE_BANNER_TEXT,
  NOTICE_BANNER_TINT,
} from '../../../frontend/src/noticeBanner';
import { fonts } from './theme';

// The same inset on every tab: just below the status bar, inset from the screen edge.
export const SERVER_NOTICE_INSET = 20;

export function ServerNoticeBanner({ topInset = 0, text = '' }: { topInset?: number; text?: string }) {
  const notice = useSyncExternalStore(subscribeOfflineBanner, serverNoticeText, () => '');
  const shown = notice !== '' ? notice : text;
  if (shown === '') return null;
  return (
    <View
      testID="server-notice-banner"
      accessibilityLiveRegion="polite"
      style={[styles.banner, { marginTop: topInset, marginHorizontal: SERVER_NOTICE_INSET }]}
    >
      <View style={styles.icon} accessibilityElementsHidden>
        <Text style={styles.iconMark}>!</Text>
      </View>
      <Text style={styles.sentence}>{shown}</Text>
    </View>
  );
}

const styles = StyleSheet.create({
  banner: {
    flexDirection: 'row',
    alignItems: 'flex-start',
    backgroundColor: NOTICE_BANNER_TINT,
    borderWidth: 1,
    borderColor: NOTICE_BANNER_EDGE,
    borderRadius: NOTICE_BANNER_RADIUS,
    paddingVertical: 4,
    paddingHorizontal: 12,
    marginBottom: 12,
    gap: 10,
  },
  icon: {
    width: 20,
    height: 20,
    marginTop: 1,
    borderRadius: 10,
    borderWidth: 1.5,
    borderColor: NOTICE_BANNER_EDGE,
    alignItems: 'center',
    justifyContent: 'center',
  },
  iconMark: {
    color: NOTICE_BANNER_EDGE,
    fontFamily: fonts.bold,
    fontSize: 13,
    lineHeight: 16,
  },
  sentence: {
    flex: 1,
    fontFamily: fonts.regular,
    fontSize: 16,
    lineHeight: 22,
    color: NOTICE_BANNER_TEXT,
  },
});
