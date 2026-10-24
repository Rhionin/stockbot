import { useState } from 'react';
import { Pressable, StyleSheet, Text, View } from 'react-native';
import { AUTH_SETUP_SENTENCE } from '@stockbot/shared/contract';
import { ServerNoticeBanner } from '../ui/ServerNoticeBanner';
import { colors, fonts } from '../ui/theme';

export function AuthSetup({ topInset, onRetry }: { topInset: number; onRetry: () => Promise<void> }) {
  const [pending, setPending] = useState(false);

  return (
    <View style={styles.screen}>
      <ServerNoticeBanner topInset={topInset} />
      <View style={styles.copy}>
        <Text style={styles.sentence}>{AUTH_SETUP_SENTENCE}</Text>
        <Pressable
          accessibilityRole="button"
          accessibilityLabel="Try again"
          accessibilityState={{ disabled: pending }}
          disabled={pending}
          onPress={() => {
            setPending(true);
            void onRetry().finally(() => setPending(false));
          }}
          style={styles.retry}
        >
          <Text style={styles.retryText}>Try again</Text>
        </Pressable>
      </View>
    </View>
  );
}

const styles = StyleSheet.create({
  screen: {
    flex: 1,
    backgroundColor: colors.paper,
  },
  copy: {
    paddingHorizontal: 24,
    paddingTop: 16,
    maxWidth: 420,
  },
  sentence: {
    fontFamily: fonts.regular,
    fontSize: 18,
    lineHeight: 26,
    color: colors.ink,
  },
  retry: {
    alignSelf: 'flex-start',
    marginTop: 20,
    minHeight: 52,
    paddingHorizontal: 18,
    backgroundColor: colors.shelfDeep,
    alignItems: 'center',
    justifyContent: 'center',
  },
  retryText: {
    fontFamily: fonts.bold,
    fontSize: 18,
    color: colors.white,
  },
});
