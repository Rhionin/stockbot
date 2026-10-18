import { useCallback, useEffect, useState } from 'react';
import {
  ActivityIndicator,
  FlatList,
  Pressable,
  RefreshControl,
  StyleSheet,
  Text,
  View,
} from 'react-native';
import { StatusBar } from 'expo-status-bar';
import { useSafeAreaInsets } from 'react-native-safe-area-context';
import type { PantryLinkSuggestion, ScanEntry } from '../../../frontend/src/types';
import { ApiError, type StockbotClient } from '../api/client';
import { modeLabel } from '../scanner/modeLabel';
import { colors, fonts } from '../ui/theme';

export interface QueueScreenProps {
  client: StockbotClient;
  warning: string;
}

export function QueueScreen({ client, warning }: QueueScreenProps) {
  const insets = useSafeAreaInsets();
  const [entries, setEntries] = useState<ScanEntry[]>([]);
  const [loading, setLoading] = useState(true);
  const [refreshing, setRefreshing] = useState(false);
  const [error, setError] = useState('');
  const [busyId, setBusyId] = useState('');
  const [offer, setOffer] = useState<{ scanId: string; suggestions: PantryLinkSuggestion[] } | null>(null);

  const applyEntries = useCallback((next: ScanEntry[], nextError = '') => {
    setEntries(next);
    setError(nextError);
    setOffer((current) => (current && next.some((entry) => entry.id === current.scanId) ? current : null));
    setLoading(false);
    setRefreshing(false);
  }, []);

  const refresh = useCallback(async (keepError = '') => {
    setRefreshing(true);
    try {
      applyEntries(await client.listPending(), keepError);
    } catch (requestError) {
      setRefreshing(false);
      setLoading(false);
      setError(requestError instanceof Error ? requestError.message : 'The queue did not load.');
    }
  }, [applyEntries, client]);

  useEffect(() => {
    let active = true;
    client.listPending().then((next) => {
      if (!active) return;
      setEntries(next);
      setError('');
      setLoading(false);
    }).catch((requestError: unknown) => {
      if (!active) return;
      setLoading(false);
      setError(requestError instanceof Error ? requestError.message : 'The queue did not load.');
    });
    return () => {
      active = false;
    };
  }, [client]);

  async function confirmOne(id: string) {
    setBusyId(id);
    setError('');
    try {
      await client.commitScan(id);
      setOffer((current) => (current?.scanId === id ? null : current));
      await refresh();
    } catch (requestError) {
      if (requestError instanceof ApiError && requestError.code === 'no_item') {
        setOffer({ scanId: id, suggestions: requestError.suggestions });
        return;
      }
      setError(requestError instanceof Error ? requestError.message : 'That scan could not be confirmed.');
    } finally {
      setBusyId('');
    }
  }

  async function linkOne(id: string, productId: string) {
    setBusyId(id);
    setError('');
    try {
      await client.commitScan(id, productId);
      setOffer((current) => (current?.scanId === id ? null : current));
      await refresh();
    } catch (requestError) {
      setError(requestError instanceof Error ? requestError.message : 'That scan could not be confirmed.');
    } finally {
      setBusyId('');
    }
  }

  async function confirmAll() {
    setBusyId('all');
    setError('');
    try {
      const result = await client.batchCommit(entries.map((entry) => entry.id));
      let keepError = '';
      if (result.failedIds && result.failedIds.length > 0) {
        const reasons = result.failedReasons?.filter((reason) => reason !== '') ?? [];
        keepError = reasons.length > 0 ? reasons.join('\n') : 'Some scans could not be confirmed.';
      }
      await refresh(keepError);
    } catch (requestError) {
      setError(requestError instanceof Error ? requestError.message : 'Those scans could not be confirmed.');
    } finally {
      setBusyId('');
    }
  }

  const waiting = entries.length === 1 ? 'One scan is waiting' : `${entries.length} scans are waiting`;

  return (
    <View style={[styles.screen, { paddingTop: insets.top + 18 }]}>
      <StatusBar style="dark" />
      <Text style={styles.heading} accessibilityRole="header">To confirm</Text>
      <Text style={styles.count}>{loading ? 'Checking the queue' : waiting}</Text>
      {warning !== '' && <Text style={styles.warning}>{warning}</Text>}
      {error !== '' && <Text accessibilityLiveRegion="polite" style={styles.error}>{error}</Text>}

      {loading ? (
        <ActivityIndicator color={colors.shelfDeep} style={styles.spinner} />
      ) : (
        <FlatList
          data={entries}
          keyExtractor={(entry) => entry.id}
          refreshControl={(
            <RefreshControl refreshing={refreshing} onRefresh={() => { void refresh(); }} tintColor={colors.shelfDeep} />
          )}
          contentContainerStyle={entries.length === 0 ? styles.emptyWrap : styles.list}
          ListEmptyComponent={<Text style={styles.empty}>Nothing is waiting. A scan you take shows up here until you confirm it.</Text>}
          renderItem={({ item }) => (
            <View style={[styles.row, { borderLeftColor: item.direction === 'stock_out' ? colors.using : colors.shelf }]}>
              <View style={styles.rowMain}>
                <View style={styles.rowCopy}>
                  <Text style={styles.name}>{item.product?.name || item.barcode}</Text>
                  <Text style={styles.meta}>
                    {item.product?.name ? item.barcode : 'No name yet'}
                    {'  '}
                    {item.direction === null ? 'No direction' : modeLabel(item.direction)}
                  </Text>
                </View>
                <Pressable
                  accessibilityRole="button"
                  accessibilityLabel={`Confirm ${item.product?.name || item.barcode}`}
                  disabled={busyId !== ''}
                  onPress={() => { void confirmOne(item.id); }}
                  style={styles.confirm}
                >
                  <Text style={styles.confirmText}>{busyId === item.id ? 'Confirming' : 'Confirm'}</Text>
                </Pressable>
              </View>
              {offer?.scanId === item.id && (
                <LinkOffer
                  suggestions={offer.suggestions}
                  busy={busyId === item.id}
                  onLink={(productId) => { void linkOne(item.id, productId); }}
                  onDismiss={() => setOffer(null)}
                />
              )}
            </View>
          )}
        />
      )}

      {entries.length > 1 && (
        <Pressable
          accessibilityRole="button"
          accessibilityLabel="Confirm all"
          disabled={busyId !== ''}
          onPress={() => { void confirmAll(); }}
          style={styles.confirmAll}
        >
          <Text style={styles.confirmAllText}>{busyId === 'all' ? 'Confirming' : 'Confirm all'}</Text>
        </Pressable>
      )}
    </View>
  );
}

function LinkOffer({
  suggestions,
  busy,
  onLink,
  onDismiss,
}: {
  suggestions: PantryLinkSuggestion[];
  busy: boolean;
  onLink: (productId: string) => void;
  onDismiss: () => void;
}) {
  return (
    <View style={styles.offer}>
      <Text style={styles.offerTitle}>Not in your pantry</Text>
      <Text style={styles.offerCopy}>
        {suggestions.length === 0
          ? 'No close match found.'
          : 'Link the barcode to an item you stock. That scan out uses the item you pick.'}
      </Text>
      {suggestions.map((suggestion) => (
        <View key={suggestion.itemId} style={styles.offerRow}>
          <View style={styles.rowCopy}>
            <Text style={styles.name}>{suggestion.name}</Text>
            <Text style={styles.meta}>{suggestion.onHand} in stock</Text>
          </View>
          <Pressable
            accessibilityRole="button"
            accessibilityLabel={`Link ${suggestion.name} and scan it out`}
            disabled={busy}
            onPress={() => onLink(suggestion.productId)}
            style={styles.confirm}
          >
            <Text style={styles.confirmText}>{busy ? 'Linking' : 'Link'}</Text>
          </Pressable>
        </View>
      ))}
      <Pressable
        accessibilityRole="button"
        accessibilityLabel="Dismiss"
        disabled={busy}
        onPress={onDismiss}
        style={styles.dismiss}
      >
        <Text style={styles.dismissText}>Dismiss</Text>
      </Pressable>
    </View>
  );
}

const styles = StyleSheet.create({
  screen: {
    flex: 1,
    backgroundColor: colors.paper,
    paddingHorizontal: 20,
  },
  heading: {
    fontFamily: fonts.bold,
    fontSize: 36,
    lineHeight: 42,
    color: colors.ink,
  },
  count: {
    fontFamily: fonts.regular,
    fontSize: 18,
    lineHeight: 26,
    color: colors.ink,
    marginTop: 6,
    marginBottom: 16,
  },
  warning: {
    fontFamily: fonts.regular,
    fontSize: 16,
    lineHeight: 22,
    color: colors.using,
    marginBottom: 12,
  },
  error: {
    fontFamily: fonts.regular,
    fontSize: 16,
    lineHeight: 22,
    color: colors.danger,
    marginBottom: 12,
  },
  spinner: {
    marginTop: 32,
  },
  list: {
    paddingBottom: 12,
  },
  emptyWrap: {
    flexGrow: 1,
    justifyContent: 'center',
  },
  empty: {
    fontFamily: fonts.regular,
    fontSize: 18,
    lineHeight: 26,
    color: colors.ink,
    maxWidth: 340,
  },
  row: {
    borderLeftWidth: 4,
    paddingLeft: 12,
    paddingVertical: 14,
    borderBottomWidth: 1,
    borderBottomColor: colors.rule,
  },
  rowMain: {
    flexDirection: 'row',
    alignItems: 'center',
    gap: 12,
  },
  rowCopy: {
    flex: 1,
  },
  name: {
    fontFamily: fonts.bold,
    fontSize: 18,
    lineHeight: 24,
    color: colors.ink,
  },
  meta: {
    fontFamily: fonts.regular,
    fontSize: 15,
    lineHeight: 21,
    color: colors.inkMuted,
    marginTop: 2,
  },
  confirm: {
    minHeight: 44,
    minWidth: 88,
    alignItems: 'center',
    justifyContent: 'center',
    paddingHorizontal: 12,
    backgroundColor: colors.shelfDeep,
  },
  confirmText: {
    fontFamily: fonts.bold,
    fontSize: 15,
    color: colors.white,
  },
  offer: {
    marginTop: 14,
    gap: 10,
  },
  offerTitle: {
    fontFamily: fonts.bold,
    fontSize: 18,
    lineHeight: 24,
    color: colors.ink,
  },
  offerCopy: {
    fontFamily: fonts.regular,
    fontSize: 16,
    lineHeight: 22,
    color: colors.ink,
  },
  offerRow: {
    flexDirection: 'row',
    alignItems: 'center',
    gap: 12,
  },
  dismiss: {
    alignSelf: 'flex-start',
    minHeight: 44,
    justifyContent: 'center',
  },
  dismissText: {
    fontFamily: fonts.bold,
    fontSize: 16,
    color: colors.ink,
  },
  confirmAll: {
    minHeight: 52,
    alignItems: 'center',
    justifyContent: 'center',
    backgroundColor: colors.shelfDeep,
    marginBottom: 12,
  },
  confirmAllText: {
    fontFamily: fonts.bold,
    fontSize: 18,
    color: colors.white,
  },
});
