import { useState, useSyncExternalStore } from 'react';
import {
  ActivityIndicator,
  KeyboardAvoidingView,
  Platform,
  Pressable,
  StyleSheet,
  Text,
  TextInput,
  View,
} from 'react-native';
import { inlineFailure, raiseOfflineBanner, serverNoticeText, subscribeOfflineBanner } from '@stockbot/shared/contract';
import { NoticeBanner } from '@stockbot/ui';
import { DEFAULT_SERVER_URL } from '../auth/credentials';
import { colors, fonts } from '../ui/theme';

export interface ConnectionFormProps {
  heading: string;
  lede: string;
  submitLabel: string;
  initialUrl?: string;
  initialError?: string;
  onSubmit: (serverUrl: string, email: string, password: string) => Promise<void>;
}

export function ConnectionForm({
  heading,
  lede,
  submitLabel,
  initialUrl = DEFAULT_SERVER_URL,
  initialError = '',
  onSubmit,
}: ConnectionFormProps) {
  const [serverUrl, setServerUrl] = useState(initialUrl);
  const [email, setEmail] = useState('');
  const [password, setPassword] = useState('');
  const [error, setError] = useState(initialError);
  const [submitting, setSubmitting] = useState(false);

  async function submit() {
    setSubmitting(true);
    setError('');
    try {
      await onSubmit(serverUrl, email, password);
    } catch (requestError) {
      const message = inlineFailure(requestError, '');
      if (message === null || !(requestError instanceof Error)) {
        if (message !== null) raiseOfflineBanner();
        setError('');
      } else {
        setError(message);
      }
    } finally {
      setSubmitting(false);
    }
  }

  return (
    <KeyboardAvoidingView
      style={styles.screen}
      behavior={Platform.OS === 'ios' ? 'padding' : undefined}
    >
      <View style={styles.copy}>
        <Text style={styles.heading} accessibilityRole="header">{heading}</Text>
        <View style={styles.edge} />
        <Text style={styles.lede}>{lede}</Text>
      </View>

      <View style={styles.field}>
        <Text style={styles.label}>Server</Text>
        <TextInput
          accessibilityLabel="Server"
          value={serverUrl}
          onChangeText={setServerUrl}
          autoCapitalize="none"
          autoCorrect={false}
          keyboardType="url"
          inputMode="url"
          style={styles.input}
        />
      </View>

      <View style={styles.field}>
        <Text style={styles.label}>Email</Text>
        <TextInput
          accessibilityLabel="Email"
          value={email}
          onChangeText={setEmail}
          autoCapitalize="none"
          autoCorrect={false}
          autoComplete="email"
          keyboardType="email-address"
          inputMode="email"
          style={styles.input}
        />
      </View>

      <View style={styles.field}>
        <Text style={styles.label}>Password</Text>
        <TextInput
          accessibilityLabel="Password"
          value={password}
          onChangeText={setPassword}
          secureTextEntry
          autoCapitalize="none"
          autoCorrect={false}
          autoComplete="current-password"
          style={styles.input}
          onSubmitEditing={() => { void submit(); }}
        />
      </View>

      {error !== '' && (
        <Text accessibilityLiveRegion="polite" style={styles.error}>{error}</Text>
      )}

      <Pressable
        accessibilityRole="button"
        accessibilityLabel={submitLabel}
        accessibilityState={{ disabled: submitting }}
        disabled={submitting}
        onPress={() => { void submit(); }}
        style={[styles.submit, submitting ? styles.submitDisabled : null]}
      >
        {submitting
          ? <ActivityIndicator color={colors.white} />
          : <Text style={styles.submitText}>{submitLabel}</Text>}
      </Pressable>

    </KeyboardAvoidingView>
  );
}

export function ConnectionScreen({ topInset, ...props }: ConnectionFormProps & { topInset: number }) {
  const notice = useSyncExternalStore(subscribeOfflineBanner, serverNoticeText, () => '');
  return (
    <View style={styles.frame}>
      <NoticeBanner topInset={topInset} fontRegular={fonts.regular} fontBold={fonts.bold} />
      <View style={[styles.frame, { paddingTop: notice === '' ? topInset : 0 }]}>
        <ConnectionForm {...props} />
      </View>
    </View>
  );
}

const styles = StyleSheet.create({
  frame: {
    flex: 1,
  },
  screen: {
    flex: 1,
    backgroundColor: colors.paper,
    paddingHorizontal: 24,
    paddingTop: 28,
    justifyContent: 'flex-start',
  },
  copy: {
    marginBottom: 28,
    maxWidth: 420,
  },
  heading: {
    fontFamily: fonts.bold,
    fontSize: 40,
    lineHeight: 46,
    color: colors.ink,
  },
  edge: {
    width: 72,
    height: 6,
    backgroundColor: colors.shelf,
    marginTop: 12,
    marginBottom: 14,
  },
  lede: {
    fontFamily: fonts.regular,
    fontSize: 18,
    lineHeight: 26,
    color: colors.ink,
    maxWidth: 360,
  },
  field: {
    marginBottom: 22,
    maxWidth: 480,
  },
  label: {
    fontFamily: fonts.bold,
    fontSize: 16,
    color: colors.ink,
    marginBottom: 6,
  },
  input: {
    fontFamily: fonts.regular,
    fontSize: 18,
    color: colors.ink,
    borderBottomWidth: 1.5,
    borderBottomColor: colors.rule,
    paddingVertical: 10,
    minHeight: 48,
  },
  error: {
    fontFamily: fonts.regular,
    fontSize: 16,
    lineHeight: 22,
    color: colors.danger,
    marginBottom: 16,
    maxWidth: 420,
  },
  submit: {
    backgroundColor: colors.shelfDeep,
    minHeight: 52,
    alignItems: 'center',
    justifyContent: 'center',
    paddingHorizontal: 18,
    maxWidth: 480,
  },
  submitDisabled: {
    opacity: 0.7,
  },
  submitText: {
    fontFamily: fonts.bold,
    fontSize: 18,
    color: colors.white,
  },
});
