import { useState } from 'react';
import { ActivityIndicator, Pressable, ScrollView, StyleSheet, Text, View } from 'react-native';
import { inlineFailure, type AuthSession } from '@stockbot/shared/contract';
import { NoticeBanner } from '@stockbot/ui';
import { colors, fonts } from '../ui/theme';
import { BuildStamp } from '../updates/BuildStamp';
import { DESKTOP_PAGE_PADDING_TOP, isWeb, useColumn, useDesktopLayout } from '../ui/layout';

export interface HouseholdScreenProps {
  topInset: number;
  serverUrl: string;
  account: AuthSession | null;
  onSwitch: (householdId: string) => Promise<void>;
  onSignOut: () => void;
}

function householdLabel(household: { name: string; deletedAt: string | null }): string {
  return household.deletedAt ? `${household.name} (deleted)` : household.name;
}

export function HouseholdScreen({ topInset, serverUrl, account, onSwitch, onSignOut }: HouseholdScreenProps) {
  const column = useColumn();
  const desktop = useDesktopLayout();
  const web = isWeb();
  const [pendingId, setPendingId] = useState('');
  const [error, setError] = useState('');
  const households = account?.households ?? [];
  const active = households.find((household) => household.id === account?.activeHouseholdId);

  async function choose(householdId: string) {
    if (householdId === account?.activeHouseholdId || pendingId !== '') return;
    setPendingId(householdId);
    setError('');
    try {
      await onSwitch(householdId);
    } catch (requestError) {
      setError(inlineFailure(requestError, 'Could not switch households.') ?? '');
    } finally {
      setPendingId('');
    }
  }

  return (
    <View role="main" style={styles.frame}>
      <NoticeBanner topInset={topInset} fontRegular={fonts.regular} fontBold={fonts.bold} />
      <ScrollView contentContainerStyle={[styles.screen, desktop ? styles.screenDesktop : null, { paddingTop: desktop ? DESKTOP_PAGE_PADDING_TOP : topInset + 28 }]}>
        <View style={[column, desktop ? styles.columnDesktop : null]}>
          <Text style={styles.heading} accessibilityRole="header">Household</Text>
          <View style={styles.edge} />

          {account?.user ? (
            <View style={styles.field}>
              <Text style={styles.label}>Signed in as</Text>
              <Text style={styles.value}>{account.user.name || account.user.email}</Text>
              {account.user.name !== '' && <Text style={styles.muted}>{account.user.email}</Text>}
            </View>
          ) : null}

          <View style={styles.field}>
            <Text style={styles.label}>Current household</Text>
            <Text style={styles.value}>{active ? householdLabel(active) : 'Not chosen'}</Text>
          </View>

          {households.length > 1 && (
            <View style={styles.field} accessibilityRole="radiogroup">
              <Text style={styles.label}>Switch household</Text>
              {households.map((household) => {
                const selected = household.id === account?.activeHouseholdId;
                return (
                  <Pressable
                    key={household.id}
                    accessibilityRole="radio"
                    accessibilityLabel={householdLabel(household)}
                    accessibilityState={{ selected, disabled: pendingId !== '' }}
                    onPress={() => { void choose(household.id); }}
                    style={[styles.option, selected ? styles.optionSelected : null]}
                  >
                    <Text style={styles.optionText}>{householdLabel(household)}</Text>
                    {pendingId === household.id && <ActivityIndicator color={colors.shelfDeep} />}
                  </Pressable>
                );
              })}
            </View>
          )}

          {error !== '' && <Text accessibilityLiveRegion="polite" style={styles.error}>{error}</Text>}

          {!web && (
            <View style={styles.field}>
              <Text style={styles.label}>Server</Text>
              <Text style={styles.value}>{serverUrl}</Text>
            </View>
          )}

          <Pressable accessibilityRole="button" accessibilityLabel="Sign out" onPress={onSignOut} style={styles.signOut}>
            <Text style={styles.signOutText}>Sign out</Text>
          </Pressable>

          <BuildStamp />
        </View>
      </ScrollView>
    </View>
  );
}

const styles = StyleSheet.create({
  frame: {
    flex: 1,
    backgroundColor: colors.paper,
  },
  screen: {
    paddingHorizontal: 24,
    paddingBottom: 28,
  },
  screenDesktop: {
    paddingHorizontal: 0,
  },
  columnDesktop: {
    paddingHorizontal: 20,
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
    marginBottom: 22,
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
  value: {
    fontFamily: fonts.regular,
    fontSize: 18,
    lineHeight: 26,
    color: colors.ink,
  },
  muted: {
    fontFamily: fonts.regular,
    fontSize: 15,
    lineHeight: 21,
    color: colors.inkMuted,
  },
  option: {
    minHeight: 52,
    flexDirection: 'row',
    alignItems: 'center',
    justifyContent: 'space-between',
    paddingHorizontal: 14,
    borderBottomWidth: 1.5,
    borderBottomColor: colors.rule,
  },
  optionSelected: {
    borderLeftWidth: 6,
    borderLeftColor: colors.shelf,
  },
  optionText: {
    fontFamily: fonts.regular,
    fontSize: 18,
    color: colors.ink,
  },
  error: {
    fontFamily: fonts.regular,
    fontSize: 16,
    lineHeight: 22,
    color: colors.danger,
    marginBottom: 16,
    maxWidth: 420,
  },
  signOut: {
    minHeight: 48,
    justifyContent: 'center',
  },
  signOutText: {
    fontFamily: fonts.bold,
    fontSize: 16,
    color: colors.danger,
  },
});
