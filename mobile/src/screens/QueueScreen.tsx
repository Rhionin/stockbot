import { useCallback, useEffect, useState } from 'react';
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
import { ApiError, type StockbotClient } from '../api/client';
import {
  BATCH_FAILURE_FALLBACK,
  batchFailureNotices,
  commitConflictBehavior,
  formatPartialRemoval,
  NO_CLOSE_MATCH,
} from '../../../frontend/src/api/contract';
import { modeLabel } from '../scanner/modeLabel';
import { colors, fonts } from '../ui/theme';

const LINK_OFFER_HEADING = 'Which item is this?';
const NOTHING_ON_HAND = 'Nothing on hand to take out.';
const NO_MATCHING_PRODUCTS = 'No matching products.';

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

function suggestionRows(suggestions: PantryLinkSuggestion[]): LinkRow[] {
  return suggestions.map((suggestion) => ({
    key: suggestion.itemId,
    name: suggestion.name,
    onHand: suggestion.onHand,
    productId: suggestion.productId,
  }));
}

// Same fields as the web link search: an empty query matches every row, and a
// typed query matches the name and category as one string. The list already
// omits shelved rows. Zero on hand stays.
function householdRows(rows: InventoryItem[], query: string, excludeProductId: string): LinkRow[] {
  const normalized = query.trim().toLocaleLowerCase();
  return rows
    .filter((row) => {
      if (excludeProductId !== '' && row.item.productId === excludeProductId) return false;
      if (normalized === '') return true;
      const product = row.item.product;
      return `${product.name} ${product.category}`.toLocaleLowerCase().includes(normalized);
    })
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
  const [partialLine, setPartialLine] = useState('');
  const [toast, setToast] = useState('');
  const [busyId, setBusyId] = useState('');
  const [offers, setOffers] = useState<Record<string, LinkOfferState>>({});
  const [notes, setNotes] = useState<Record<string, string>>({});
  const [household, setHousehold] = useState<InventoryItem[] | null>(null);
  const [excluded, setExcluded] = useState<Record<string, string>>({});

  const applyEntries = useCallback((next: ScanEntry[], nextError = '') => {
    setEntries(next);
    setError(nextError);
    setOffers((current) => keepIds(current, next));
    setNotes((current) => keepIds(current, next));
    setExcluded((current) => keepIds(current, next));
    setLoading(false);
    setRefreshing(false);
  }, []);

  const loadHousehold = useCallback(async () => {
    try {
      const rows = await client.getInventoryList();
      return Array.isArray(rows) ? rows : [];
    } catch {
      return [];
    }
  }, [client]);

  const refresh = useCallback(async (keepError = '') => {
    setRefreshing(true);
    try {
      const [next, rows] = await Promise.all([client.listPending(), loadHousehold()]);
      setHousehold(rows);
      applyEntries(next, keepError);
    } catch (requestError) {
      setRefreshing(false);
      setLoading(false);
      setError(requestError instanceof Error ? requestError.message : 'The queue did not load.');
    }
  }, [applyEntries, client, loadHousehold]);

  useEffect(() => {
    let active = true;
    Promise.all([client.listPending(), loadHousehold()]).then(([next, rows]) => {
      if (!active) return;
      setHousehold(rows);
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
  }, [client, loadHousehold]);

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
    setExcluded((current) => {
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

  async function showCommitFailure(id: string, requestError: ApiError) {
    const behavior = commitConflictBehavior(requestError.code);
    // refreshInstances would reload a per-unit picker. This queue does not have one.
    setToast(behavior.showMessage ? requestError.message : '');
    clearNote(id);
    if (behavior.refreshSuggestions) {
      setHousehold(await loadHousehold());
      showOffer(id, offers[id]?.suggestions ?? [], true);
      return;
    }
    if (!behavior.linkOffer) {
      clearOffer(id);
      return;
    }
    const suggestions = behavior.hideEmptyMatch ? [] : requestError.suggestions;
    showOffer(id, suggestions, !behavior.hideEmptyMatch);
  }

  async function confirmOne(id: string) {
    setBusyId(id);
    setError('');
    setToast('');
    try {
      const committed = await client.commitScan(id);
      clearOffer(id);
      clearNote(id);
      setPartialLine('');
      setToast(formatPartialRemoval(committed) ?? '');
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
    const previous = offers[id]?.suggestions ?? [];
    setBusyId(id);
    setError('');
    setToast('');
    try {
      const committed = await client.commitScan(id, productId);
      clearOffer(id);
      clearNote(id);
      setPartialLine('');
      setToast(formatPartialRemoval(committed) ?? '');
      await refresh();
    } catch (requestError) {
      if (requestError instanceof ApiError && commitConflictBehavior(requestError.code).refreshSuggestions) {
        setToast(requestError.message);
        clearNote(id);
        setExcluded((current) => ({ ...current, [id]: productId }));
        setHousehold(await loadHousehold());
        showOffer(id, previous.filter((row) => row.productId !== productId), true);
      } else if (requestError instanceof ApiError && requestError.code === 'none_on_hand' && offers[id]) {
        setToast(requestError.message);
      } else if (requestError instanceof ApiError) {
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
    setPartialLine('');
    setToast('');
    try {
      const names: Record<string, string> = {};
      for (const entry of entries) names[entry.id] = entry.product?.name ?? 'Unknown product';
      const result = await client.batchCommit(entries.map((entry) => entry.id));
      const notices = batchFailureNotices(result);
      if (notices.some((notice) => commitConflictBehavior(notice.code || undefined).refreshSuggestions)) {
        setHousehold(await loadHousehold());
      }
      const pending = await client.listPending();
      const nextNotes: Record<string, string> = {};
      const nextOffers: Record<string, LinkOfferState> = {};
      for (const notice of notices) {
        if (!pending.some((entry) => entry.id === notice.id)) continue;
        const behavior = commitConflictBehavior(notice.code || undefined);
        if (behavior.showMessage && notice.message !== '') nextNotes[notice.id] = notice.message;
        if (!(behavior.linkOffer || behavior.refreshSuggestions)) continue;
        nextOffers[notice.id] = {
          suggestions: [],
          showEmptyLine: !behavior.hideEmptyMatch,
        };
      }
      setEntries(pending);
      setNotes(nextNotes);
      setOffers(nextOffers);
      setExcluded({});
      setError(notices.length > 0 ? BATCH_FAILURE_FALLBACK : '');
      setPartialLine(formatPartialRemoval(result, names) ?? '');
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
      {(error !== '' || partialLine !== '') && (
        <View testID="batch-banner" accessibilityLiveRegion="polite">
          {error !== '' && <Text style={styles.error}>{error}</Text>}
          {partialLine !== '' && <Text style={styles.bannerBody}>{partialLine}</Text>}
        </View>
      )}

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
              {household !== null
                && !offers[item.id]
                && item.direction === 'stock_out'
                && item.productId !== null
                && !household.some((row) => row.item.productId === item.productId)
                && notes[item.id] !== NOTHING_ON_HAND
                && <Text style={styles.note}>{NOTHING_ON_HAND}</Text>}
              {offers[item.id] && (
                <LinkOffer
                  key={offers[item.id].showEmptyLine ? `${item.id}-empty` : `${item.id}-pick`}
                  suggestions={offers[item.id].suggestions}
                  showEmptyLine={offers[item.id].showEmptyLine}
                  busy={busyId === item.id}
                  household={household}
                  excludeProductId={excluded[item.id] ?? ''}
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

function LinkRows({ rows, busy, onLink }: { rows: LinkRow[]; busy: boolean; onLink: (productId: string) => void }) {
  return rows.map((row) => (
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
  ));
}

function LinkOffer({
  suggestions,
  showEmptyLine,
  busy,
  household,
  excludeProductId,
  onLink,
  onDismiss,
}: {
  suggestions: PantryLinkSuggestion[];
  showEmptyLine: boolean;
  busy: boolean;
  household: InventoryItem[] | null;
  excludeProductId: string;
  onLink: (productId: string) => void;
  onDismiss: () => void;
}) {
  const [query, setQuery] = useState('');
  const [open, setOpen] = useState(false);
  const needle = query.trim();
  const suggestedIds = new Set(suggestions.map((suggestion) => suggestion.productId));
  const householdMatches = household === null ? null : householdRows(household, needle, excludeProductId);
  const matches = householdMatches?.filter((row) => !suggestedIds.has(row.productId)) ?? null;
  const showMatches = open || needle !== '';

  return (
    <View style={styles.offer}>
      <Text style={styles.offerTitle}>{LINK_OFFER_HEADING}</Text>
      {showEmptyLine && needle === '' && <Text style={styles.offerCopy}>{NO_CLOSE_MATCH}</Text>}
      <LinkRows rows={suggestionRows(suggestions)} busy={busy} onLink={onLink} />
      <TextInput
        accessibilityLabel="Search"
        value={query}
        onChangeText={(next) => {
          setQuery(next);
          setOpen(true);
        }}
        onFocus={() => setOpen(true)}
        placeholder="Search"
        placeholderTextColor={colors.inkMuted}
        autoCorrect={false}
        style={styles.search}
      />
      {showMatches && householdMatches !== null && householdMatches.length === 0 && (
        <Text style={styles.offerCopy}>{NO_MATCHING_PRODUCTS}</Text>
      )}
      {showMatches && matches !== null && matches.length > 0 && <LinkRows rows={matches} busy={busy} onLink={onLink} />}
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
  bannerBody: {
    fontFamily: fonts.regular,
    fontSize: 16,
    lineHeight: 22,
    color: colors.ink,
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
