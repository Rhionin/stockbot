import { useCallback, useEffect, useRef, useState } from 'react';
import {
  ActivityIndicator,
  FlatList,
  Pressable,
  RefreshControl,
  StyleSheet,
  Text,
  TextInput,
  View,
} from 'react-native';
import { StatusBar } from 'expo-status-bar';
import { useSafeAreaInsets } from 'react-native-safe-area-context';
import type { InventoryItem, PantryLinkSuggestion, ScanEntry } from '../../../frontend/src/types';
import { filterInventoryItems } from '../../../frontend/src/utils/inventoryFilter';
import { ApiError, type StockbotClient } from '../api/client';
import {
  BATCH_FAILURE_FALLBACK,
  batchFailureNotices,
  commitConflictBehavior,
  type CommitConflictBehavior,
  formatPartialRemoval,
  NO_CLOSE_MATCH,
} from '../../../frontend/src/api/contract';
import { modeLabel } from '../scanner/modeLabel';
import { colors, fonts } from '../ui/theme';

const LINK_OFFER_HEADING = 'Which item is this?';

export interface QueueScreenProps {
  client: StockbotClient;
  warning: string;
}

interface LinkOfferState {
  suggestions: PantryLinkSuggestion[];
  showEmptyLine: boolean;
}

interface LinkRow {
  key: string;
  name: string;
  onHand: number;
  productId: string;
}

function keepIds<T>(current: Record<string, T>, entries: ScanEntry[]): Record<string, T> {
  const ids = new Set(entries.map((entry) => entry.id));
  return Object.fromEntries(Object.entries(current).filter(([id]) => ids.has(id)));
}

function isShelved(row: InventoryItem): boolean {
  const shelved = (row.item as { shelved?: boolean | number }).shelved;
  return shelved === true || shelved === 1;
}

function suggestionRows(suggestions: PantryLinkSuggestion[]): LinkRow[] {
  return suggestions.map((suggestion) => ({
    key: suggestion.itemId,
    name: suggestion.name,
    onHand: suggestion.onHand,
    productId: suggestion.productId,
  }));
}

// Same name and category match as the inventory page. Shelved rows are dropped
// here until the list itself omits them. Zero on hand stays.
function householdRows(rows: InventoryItem[], query: string): LinkRow[] {
  return filterInventoryItems(rows, query)
    .filter((row) => !isShelved(row))
    .map((row) => ({
      key: row.item.id,
      name: row.item.product.name,
      onHand: row.instanceCount,
      productId: row.item.productId,
    }));
}

export function QueueScreen({ client, warning }: QueueScreenProps) {
  const insets = useSafeAreaInsets();
  const [entries, setEntries] = useState<ScanEntry[]>([]);
  const [loading, setLoading] = useState(true);
  const [refreshing, setRefreshing] = useState(false);
  const [error, setError] = useState('');
  const [toast, setToast] = useState('');
  const [busyId, setBusyId] = useState('');
  const [offers, setOffers] = useState<Record<string, LinkOfferState>>({});
  const [notes, setNotes] = useState<Record<string, string>>({});

  const applyEntries = useCallback((next: ScanEntry[], nextError = '') => {
    setEntries(next);
    setError(nextError);
    setOffers((current) => keepIds(current, next));
    setNotes((current) => keepIds(current, next));
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

  function showOffer(id: string, suggestions: PantryLinkSuggestion[], showEmptyLine: boolean) {
    setOffers((current) => ({
      ...current,
      [id]: { suggestions, showEmptyLine: showEmptyLine && suggestions.length === 0 },
    }));
  }

  function clearOffer(id: string) {
    setOffers((current) => {
      if (!(id in current)) return current;
      const next = { ...current };
      delete next[id];
      return next;
    });
  }

  function clearNote(id: string) {
    setNotes((current) => {
      if (!(id in current)) return current;
      const next = { ...current };
      delete next[id];
      return next;
    });
  }

  // A link recommit with an empty list asks again so the stale row can drop off.
  // One extra commit; its suggestions are the new list.
  async function refetchSuggestions(id: string): Promise<PantryLinkSuggestion[]> {
    try {
      await client.commitScan(id);
      return [];
    } catch (requestError) {
      return requestError instanceof ApiError ? requestError.suggestions : [];
    }
  }

  async function offerSuggestions(
    id: string,
    behavior: CommitConflictBehavior,
    carried: PantryLinkSuggestion[],
  ): Promise<PantryLinkSuggestion[]> {
    if (behavior.hideEmptyMatch) return [];
    if (behavior.refreshSuggestions && carried.length === 0) return refetchSuggestions(id);
    return carried;
  }

  async function showCommitFailure(id: string, requestError: ApiError) {
    const behavior = commitConflictBehavior(requestError.code);
    // refreshInstances would reload a per-unit picker. This queue does not have one.
    setToast(behavior.showMessage ? requestError.message : '');
    clearNote(id);
    if (!(behavior.linkOffer || behavior.refreshSuggestions)) {
      clearOffer(id);
      return;
    }
    const suggestions = await offerSuggestions(id, behavior, requestError.suggestions);
    showOffer(id, suggestions, behavior.linkOffer && !behavior.hideEmptyMatch);
  }

  async function confirmOne(id: string) {
    setBusyId(id);
    setError('');
    setToast('');
    try {
      const committed = await client.commitScan(id);
      clearOffer(id);
      clearNote(id);
      const unitCount = entries.find((entry) => entry.id === id)?.unitCount ?? 1;
      setToast(formatPartialRemoval(unitCount, committed) ?? '');
      await refresh();
    } catch (requestError) {
      if (requestError instanceof ApiError) {
        await showCommitFailure(id, requestError);
      } else {
        setToast(requestError instanceof Error ? requestError.message : 'That scan could not be confirmed.');
      }
    } finally {
      setBusyId('');
    }
  }

  async function linkOne(id: string, productId: string) {
    setBusyId(id);
    setError('');
    setToast('');
    try {
      const committed = await client.commitScan(id, productId);
      clearOffer(id);
      clearNote(id);
      const unitCount = entries.find((entry) => entry.id === id)?.unitCount ?? 1;
      setToast(formatPartialRemoval(unitCount, committed) ?? '');
      await refresh();
    } catch (requestError) {
      if (requestError instanceof ApiError) {
        await showCommitFailure(id, requestError);
      } else {
        setToast(requestError instanceof Error ? requestError.message : 'That scan could not be confirmed.');
      }
    } finally {
      setBusyId('');
    }
  }

  async function confirmAll() {
    setBusyId('all');
    setError('');
    setToast('');
    try {
      const result = await client.batchCommit(entries.map((entry) => entry.id));
      const notices = batchFailureNotices(result);
      const resolved = await Promise.all(notices.map(async (notice) => {
        const behavior = commitConflictBehavior(notice.code || undefined);
        const suggestions = behavior.refreshSuggestions ? await refetchSuggestions(notice.id) : [];
        return { notice, behavior, suggestions };
      }));
      const pending = await client.listPending();
      const nextNotes: Record<string, string> = {};
      const nextOffers: Record<string, LinkOfferState> = {};
      for (const { notice, behavior, suggestions } of resolved) {
        if (!pending.some((entry) => entry.id === notice.id)) continue;
        if (behavior.showMessage && notice.message !== '') nextNotes[notice.id] = notice.message;
        if (!(behavior.linkOffer || behavior.refreshSuggestions)) continue;
        const list = behavior.hideEmptyMatch ? [] : suggestions;
        nextOffers[notice.id] = {
          suggestions: list,
          showEmptyLine: behavior.linkOffer && !behavior.hideEmptyMatch && list.length === 0,
        };
      }
      setEntries(pending);
      setNotes(nextNotes);
      setOffers(nextOffers);
      setError(notices.length > 0 ? BATCH_FAILURE_FALLBACK : '');
      setLoading(false);
      setRefreshing(false);
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
            <View testID={`scan-${item.id}`} style={[styles.row, { borderLeftColor: item.direction === 'stock_out' ? colors.using : colors.shelf }]}>
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
              {notes[item.id] ? <Text style={styles.note}>{notes[item.id]}</Text> : null}
              {offers[item.id] && (
                <LinkOffer
                  suggestions={offers[item.id].suggestions}
                  showEmptyLine={offers[item.id].showEmptyLine}
                  busy={busyId === item.id}
                  loadHousehold={() => client.getInventoryList()}
                  onLink={(productId) => { void linkOne(item.id, productId); }}
                  onDismiss={() => clearOffer(item.id)}
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

      {toast !== '' && (
        <View testID="confirm-toast" accessibilityLiveRegion="polite" style={[styles.toast, { bottom: insets.bottom + 16 }]}>
          <Text style={styles.toastText}>{toast}</Text>
        </View>
      )}
    </View>
  );
}

function LinkOffer({
  suggestions,
  showEmptyLine,
  busy,
  loadHousehold,
  onLink,
  onDismiss,
}: {
  suggestions: PantryLinkSuggestion[];
  showEmptyLine: boolean;
  busy: boolean;
  loadHousehold: () => Promise<InventoryItem[]>;
  onLink: (productId: string) => void;
  onDismiss: () => void;
}) {
  const [query, setQuery] = useState('');
  const [household, setHousehold] = useState<InventoryItem[] | null>(null);
  const requested = useRef(false);
  const needle = query.trim();
  const visible = needle === '' ? suggestionRows(suggestions) : householdRows(household ?? [], needle);

  function onQuery(next: string) {
    setQuery(next);
    if (next.trim() === '' || requested.current) return;
    requested.current = true;
    void loadHousehold()
      .then((rows) => setHousehold(Array.isArray(rows) ? rows : []))
      .catch(() => setHousehold([]));
  }

  return (
    <View style={styles.offer}>
      <Text style={styles.offerTitle}>{LINK_OFFER_HEADING}</Text>
      {showEmptyLine && needle === '' && <Text style={styles.offerCopy}>{NO_CLOSE_MATCH}</Text>}
      {visible.map((row) => (
        <View key={row.key} style={styles.offerRow}>
          <View style={styles.rowCopy}>
            <Text style={styles.name}>{row.name}</Text>
            <Text style={styles.meta}>{row.onHand} in stock</Text>
          </View>
          <Pressable
            accessibilityRole="button"
            accessibilityLabel={`Link ${row.name}`}
            disabled={busy}
            onPress={() => onLink(row.productId)}
            style={styles.confirm}
          >
            <Text style={styles.confirmText}>{busy ? 'Linking' : 'Link'}</Text>
          </Pressable>
        </View>
      ))}
      <TextInput
        accessibilityLabel="Search"
        value={query}
        onChangeText={onQuery}
        placeholder="Search"
        placeholderTextColor={colors.inkMuted}
        autoCorrect={false}
        style={styles.search}
      />
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
    paddingVertical: 12,
    paddingHorizontal: 12,
    backgroundColor: colors.paper,
    borderLeftWidth: 3,
    borderLeftColor: colors.rule,
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
    color: colors.inkMuted,
  },
  note: {
    fontFamily: fonts.regular,
    fontSize: 16,
    lineHeight: 22,
    color: colors.ink,
    marginTop: 10,
  },
  offerRow: {
    flexDirection: 'row',
    alignItems: 'center',
    gap: 12,
  },
  search: {
    minHeight: 44,
    fontFamily: fonts.regular,
    fontSize: 16,
    color: colors.ink,
    borderBottomWidth: 1,
    borderBottomColor: colors.rule,
    paddingVertical: 8,
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
  toast: {
    position: 'absolute',
    left: 20,
    right: 20,
    backgroundColor: colors.ink,
    paddingVertical: 14,
    paddingHorizontal: 16,
  },
  toastText: {
    fontFamily: fonts.regular,
    fontSize: 16,
    lineHeight: 22,
    color: colors.white,
  },
});
