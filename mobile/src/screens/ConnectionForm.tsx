import { useState } from 'react';
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
import { DEFAULT_SERVER_URL } from '../auth/credentials';
import { colors, fonts } from '../ui/theme';

export interface ConnectionFormProps {
  heading: string;
  lede: string;
  submitLabel: string;
  initialUrl?: string;
  initialPassword?: string;
  initialError?: string;
  onSubmit: (serverUrl: string, password: string) => Promise<void>;
  onDisconnect?: () => void;
}

export function ConnectionForm({
  heading,
  lede,
  submitLabel,
  initialUrl = DEFAULT_SERVER_URL,
  initialPassword = '',
  initialError = '',
  onSubmit,
  onDisconnect,
}: ConnectionFormProps) {
  const [serverUrl, setServerUrl] = useState(initialUrl);
  const [password, setPassword] = useState(initialPassword);
  const [error, setError] = useState(initialError);
  const [submitting, setSubmitting] = useState(false);

  async function submit() {
    setSubmitting(true);
    setError('');
    try {
      await onSubmit(serverUrl, password);
    } catch (requestError) {
      setError(requestError instanceof Error ? requestError.message : 'Stockbot did not answer at that address.');
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
        <Text style={styles.label}>Household password</Text>
        <TextInput
          accessibilityLabel="Household password"
          value={password}
          onChangeText={setPassword}
          secureTextEntry
          autoCapitalize="none"
          autoCorrect={false}
          style={styles.input}
          onSubmitEditing={() => { void submit(); }}
        />
        <Text style={styles.hint}>The public pantry signs in as pantry.</Text>
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

      {onDisconnect !== undefined && (
        <Pressable accessibilityRole="button" accessibilityLabel="Disconnect" onPress={onDisconnect} style={styles.disconnect}>
          <Text style={styles.disconnectText}>Disconnect</Text>
        </Pressable>
      )}
    </KeyboardAvoidingView>
  );
}

const styles = StyleSheet.create({
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
  hint: {
    fontFamily: fonts.regular,
    fontSize: 15,
    lineHeight: 21,
    color: colors.inkMuted,
    marginTop: 8,
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
  disconnect: {
    minHeight: 48,
    justifyContent: 'center',
    marginTop: 8,
  },
  disconnectText: {
    fontFamily: fonts.bold,
    fontSize: 16,
    color: colors.danger,
  },
});
