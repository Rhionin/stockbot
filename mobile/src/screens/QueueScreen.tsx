import { useCallback, useEffect, useRef, useState } from 'react';
import {
  AccessibilityInfo,
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
  CODE_NONE_ON_HAND,
  confirmSkipsNothingOnHand,
  commitConflictBehavior,
  formatPartialRemoval,
  NOTHING_ON_HAND_BODY,
  NOTHING_ON_HAND_TITLE,
  nothingOnHandPill,
  NO_CLOSE_MATCH,
  shelfHasNothingOnHand,
} from '../../../frontend/src/api/contract';
import { modeLabel } from '../scanner/modeLabel';
import { colors, fonts } from '../ui/theme';

const LINK_OFFER_HEADING = 'Which item is this?';
const NO_MATCHING_PRODUCTS = 'No matching products.';

interface NothingPin {
  onHand: number | null;
  observed: boolean;
}

function shelfCount(rows: InventoryItem[], productId: string | null): number | undefined {
  if (productId === null) return undefined;
  return rows.find((row) => row.item.productId === productId)?.instanceCount;
}

function pinFromShelf(entry: ScanEntry, rows: InventoryItem[] | null): NothingPin {
  if (rows === null) return { onHand: null, observed: false };
  const count = shelfCount(rows, entry.productId);
  return { onHand: count === undefined ? null : count, observed: true };
}

// A pin records the count seen when a commit came back none_on_hand. The first
// household load after an unobserved pin records that count and stays. A later
// count that differs drops the pin, and a stock-in drops it directly.
function reconcilePins(
  current: Map<string, NothingPin>,
  listed: ScanEntry[],
  rows: InventoryItem[],
): Map<string, NothingPin> {
  const next = new Map<string, NothingPin>();
  for (const entry of listed) {
    const pin = current.get(entry.id);
    if (pin === undefined) continue;
    const count = shelfCount(rows, entry.productId);
    const onHand = count === undefined ? null : count;
    if (!pin.observed) {
      next.set(entry.id, { onHand, observed: true });
      continue;
    }
    if (pin.onHand !== onHand) continue;
    next.set(entry.id, pin);
  }
  return next;
}

function readableInventory(rows: unknown): InventoryItem[] | null {
  if (!Array.isArray(rows)) return null;
  for (const row of rows) {
    if (row === null || typeof row !== 'object') return null;
    const item = (row as InventoryItem).item;
    if (item === undefined || typeof item.productId !== 'string') return null;
    if (typeof (row as InventoryItem).instanceCount !== 'number') return null;
  }
  return rows as InventoryItem[];
}

function isNothingOnHand(
  entry: ScanEntry,
  rows: InventoryItem[] | null,
  pins: Map<string, NothingPin>,
): boolean {
  if (entry.direction !== 'stock_out' || entry.productId === null) return false;
  if (pins.has(entry.id)) return true;
  if (rows === null) return false;
  return shelfHasNothingOnHand(shelfCount(rows, entry.productId));
}

export interface QueueScreenProps {
  client: StockbotClient;
  warning: string;
  focused?: boolean;
}

function sameQueue(current: Set<string>, next: ScanEntry[]): boolean {
  if (current.size !== next.length) return false;
  return next.every((entry) => current.has(entry.id));
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

export function QueueScreen({ client, warning, focused = true }: QueueScreenProps) {
  const insets = useSafeAreaInsets();
  const [entries, setEntries] = useState<ScanEntry[]>([]);
  const shownIds = useRef(new Set<string>());
  const [loading, setLoading] = useState(true);
  const [refreshing, setRefreshing] = useState(false);
  const [error, setError] = useState('');
  const [loadError, setLoadError] = useState('');
  const [failureBannerIds, setFailureBannerIds] = useState<Record<string, true>>({});
  const [partialLine, setPartialLine] = useState('');
  const refreshGeneration = useRef(0);
  const [toast, setToast] = useState('');
  const [busyId, setBusyId] = useState('');
  const [offers, setOffers] = useState<Record<string, LinkOfferState>>({});
  const [notes, setNotes] = useState<Record<string, string>>({});
  const [household, setHousehold] = useState<InventoryItem[] | null>(null);
  const [pins, setPins] = useState<Map<string, NothingPin>>(() => new Map());
  const [excluded, setExcluded] = useState<Record<string, string>>({});

  const storeHousehold = useCallback((rows: InventoryItem[] | null, listed: ScanEntry[]) => {
    if (rows === null) return;
    setHousehold(rows);
    setPins((current) => reconcilePins(current, listed, rows));
  }, []);

  const applyEntries = useCallback((next: ScanEntry[], nextError = '') => {
    const changed = !sameQueue(shownIds.current, next);
    shownIds.current = new Set(next.map((entry) => entry.id));
    setEntries(next);
    if (changed) setPartialLine('');
    // The red sentence follows the failed cards still pending. A load error
    // lives in its own state, so a refresh cannot replace that sentence.
    setError(nextError);
    setLoadError('');
    setFailureBannerIds((current) => {
      const kept: Record<string, true> = {};
      for (const entry of next) {
        if (current[entry.id] === true) kept[entry.id] = true;
      }
      return kept;
    });
    setOffers((current) => keepIds(current, next));
    setNotes((current) => keepIds(current, next));
    setExcluded((current) => keepIds(current, next));
    setLoading(false);
    setRefreshing(false);
  }, []);

  const loadHousehold = useCallback(async () => {
    try {
      const rows = await client.getInventoryList();
      return readableInventory(rows);
    } catch {
      return null;
    }
  }, [client]);

  const refresh = useCallback(async (keepError = '', surfaceError = true) => {
    const generation = refreshGeneration.current + 1;
    refreshGeneration.current = generation;
    setRefreshing(true);
    try {
      const [next, rows] = await Promise.all([client.listPending(), loadHousehold()]);
      if (refreshGeneration.current !== generation) return;
      storeHousehold(rows, next);
      applyEntries(next, keepError);
    } catch (requestError) {
      if (refreshGeneration.current !== generation) return;
      setRefreshing(false);
      setLoading(false);
      if (surfaceError) {
        setLoadError(requestError instanceof Error ? requestError.message : 'The queue did not load.');
      }
    }
  }, [applyEntries, client, loadHousehold, storeHousehold]);

  useEffect(() => {
    let active = true;
    Promise.all([client.listPending(), loadHousehold()]).then(([next, rows]) => {
      if (!active) return;
      storeHousehold(rows, next);
      shownIds.current = new Set(next.map((entry) => entry.id));
      setEntries(next);
      setError('');
      setLoadError('');
      setLoading(false);
    }).catch((requestError: unknown) => {
      if (!active) return;
      setLoading(false);
      setLoadError(requestError instanceof Error ? requestError.message : 'The queue did not load.');
    });
    return () => {
      active = false;
    };
  }, [client, loadHousehold, storeHousehold]);

  const [focusSeen, setFocusSeen] = useState(focused);
  if (focusSeen !== focused) {
    setFocusSeen(focused);
    if (!focused) setPartialLine('');
  }

  const previousFocus = useRef<boolean | null>(null);
  useEffect(() => {
    const previous = previousFocus.current;
    previousFocus.current = focused;
    // Regain focus reloads the household so a stock-in made elsewhere can clear amber.
    if (previous === false && focused) void refresh();
  }, [focused, refresh]);

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

  function clearFailure(id: string) {
    setFailureBannerIds((current) => {
      if (current[id] !== true) return current;
      const next = { ...current };
      delete next[id];
      return next;
    });
  }

  function rememberPin(id: string) {
    const entry = entries.find((item) => item.id === id);
    if (entry === undefined || entry.direction !== 'stock_out' || entry.productId === null) return;
    const pin = pinFromShelf(entry, household);
    setPins((current) => {
      const next = new Map(current);
      next.set(id, pin);
      return next;
    });
    AccessibilityInfo.announceForAccessibility(NOTHING_ON_HAND_TITLE);
  }

  function releasePins(productId: string) {
    setPins((current) => {
      let changed = false;
      const next = new Map(current);
      for (const [id] of current) {
        const entry = entries.find((item) => item.id === id);
        if (entry?.productId !== productId) continue;
        next.delete(id);
        changed = true;
      }
      return changed ? next : current;
    });
  }

  async function showCommitFailure(id: string, requestError: ApiError) {
    if (requestError.code === CODE_NONE_ON_HAND) {
      rememberPin(id);
      clearNote(id);
      return;
    }
    const behavior = commitConflictBehavior(requestError.code);
    // refreshInstances would reload a per-unit picker. This queue does not have one.
    setToast(behavior.showMessage ? requestError.message : '');
    clearNote(id);
    if (behavior.refreshSuggestions) {
      storeHousehold(await loadHousehold(), entries);
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
    clearFailure(id);
    setPartialLine('');
    setToast('');
    try {
      const committed = await client.commitScan(id);
      clearOffer(id);
      clearNote(id);
      setPartialLine('');
      setToast(formatPartialRemoval(committed) ?? '');
      if (committed.direction === 'stock_in' && committed.productId) releasePins(committed.productId);
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

  async function removeOne(id: string) {
    setBusyId(id);
    setError('');
    setToast('');
    try {
      const removed = await client.cancelScan(id);
      if (removed.status !== 'cancelled') {
        setToast('That scan could not be removed.');
        return;
      }
      setEntries((current) => {
        const next = current.filter((entry) => entry.id !== id);
        shownIds.current = new Set(next.map((entry) => entry.id));
        return next;
      });
      setPins((current) => {
        if (!current.has(id)) return current;
        const next = new Map(current);
        next.delete(id);
        return next;
      });
      clearOffer(id);
      clearNote(id);
      await refresh('', false);
    } catch (requestError) {
      setToast(requestError instanceof Error ? requestError.message : 'That scan could not be removed.');
    } finally {
      setBusyId('');
    }
  }

  async function linkOne(id: string, productId: string) {
    const previous = offers[id]?.suggestions ?? [];
    setBusyId(id);
    setError('');
    clearFailure(id);
    setPartialLine('');
    setToast('');
    try {
      const committed = await client.commitScan(id, productId);
      clearOffer(id);
      clearNote(id);
      setPartialLine('');
      setToast(formatPartialRemoval(committed) ?? '');
      if (committed.direction === 'stock_in' && committed.productId) releasePins(committed.productId);
      await refresh();
    } catch (requestError) {
      if (requestError instanceof ApiError && commitConflictBehavior(requestError.code).refreshSuggestions) {
        setToast(requestError.message);
        clearNote(id);
        setExcluded((current) => ({ ...current, [id]: productId }));
        storeHousehold(await loadHousehold(), entries);
        showOffer(id, previous.filter((row) => row.productId !== productId), true);
      } else if (requestError instanceof ApiError && requestError.code === CODE_NONE_ON_HAND) {
        rememberPin(id);
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
    const confirmable = entries.filter((entry) => !isNothingOnHand(entry, household, pins));
    if (confirmable.length === 0) return;
    refreshGeneration.current += 1;
    setRefreshing(false);
    setBusyId('all');
    setError('');
    setFailureBannerIds({});
    setPartialLine('');
    setToast('');
    try {
      const names: Record<string, string> = {};
      for (const entry of confirmable) {
        const name = entry.product?.name;
        if (name !== undefined && name !== '') names[entry.id] = name;
      }
      const result = await client.batchCommit(confirmable.map((entry) => entry.id));
      const notices = batchFailureNotices(result);
      const loaded = notices.some((notice) => commitConflictBehavior(notice.code || undefined).refreshSuggestions)
        ? await loadHousehold()
        : null;
      const shelf = loaded ?? household;
      const pending = await client.listPending();
      const committed = new Set(result.committedIds ?? []);
      const nextNotes: Record<string, string> = {};
      const nextOffers: Record<string, LinkOfferState> = {};
      for (const notice of notices) {
        if (!pending.some((entry) => entry.id === notice.id)) continue;
        if (notice.code === CODE_NONE_ON_HAND) continue;
        const behavior = commitConflictBehavior(notice.code || undefined);
        if (behavior.showMessage && notice.message !== '') nextNotes[notice.id] = notice.message;
        if (!(behavior.linkOffer || behavior.refreshSuggestions)) continue;
        nextOffers[notice.id] = {
          suggestions: [],
          showEmptyLine: !behavior.hideEmptyMatch,
        };
      }
      if (loaded !== null) setHousehold(loaded);
      setPins((current) => {
        const next = new Map(loaded !== null ? reconcilePins(current, pending, loaded) : current);
        for (const notice of notices) {
          if (notice.code !== CODE_NONE_ON_HAND) continue;
          const entry = pending.find((item) => item.id === notice.id);
          if (entry === undefined || entry.direction !== 'stock_out' || entry.productId === null) continue;
          const count = shelf === null ? undefined : shelfCount(shelf, entry.productId);
          next.set(entry.id, {
            onHand: shelf === null || count === undefined ? null : count,
            observed: shelf !== null,
          });
        }
        for (const entry of confirmable) {
          if (entry.direction !== 'stock_in' || entry.productId === null || !committed.has(entry.id)) continue;
          for (const id of next.keys()) {
            const card = entries.find((item) => item.id === id) ?? pending.find((item) => item.id === id);
            if (card?.productId === entry.productId) next.delete(id);
          }
        }
        for (const id of next.keys()) {
          if (!pending.some((item) => item.id === id)) next.delete(id);
        }
        return next;
      });
      shownIds.current = new Set(pending.map((entry) => entry.id));
      setEntries(pending);
      setNotes(nextNotes);
      setOffers(nextOffers);
      setExcluded({});
      const stillThere = (noticeId: string) => pending.some((entry) => entry.id === noticeId);
      const blocking = notices.some((notice) => notice.code !== CODE_NONE_ON_HAND && stillThere(notice.id));
      const amberLeft = notices.filter((notice) => notice.code === CODE_NONE_ON_HAND && stillThere(notice.id)).map((notice) => notice.id);
      const pendingFailures: Record<string, true> = {};
      for (const notice of notices) {
        if (notice.code === CODE_NONE_ON_HAND) continue;
        if (stillThere(notice.id)) pendingFailures[notice.id] = true;
      }
      setFailureBannerIds(pendingFailures);
      // Red only while a non-amber failure is still open. A card that already
      // left, including one beside an amber card, does not keep the sentence.
      setError(blocking ? BATCH_FAILURE_FALLBACK : '');
      setPartialLine(formatPartialRemoval(result, names) ?? '');
      setLoadError('');
      if (amberLeft.length > 0) AccessibilityInfo.announceForAccessibility(NOTHING_ON_HAND_TITLE);
      setLoading(false);
      setRefreshing(false);
    } catch (requestError) {
      setError(requestError instanceof Error ? requestError.message : 'Those scans could not be confirmed.');
    } finally {
      setBusyId('');
    }
  }

  const waiting = entries.length === 1 ? 'One scan is waiting' : `${entries.length} scans are waiting`;
  const failedPending = entries.some((entry) => (notes[entry.id] ?? '') !== '');
  const failureBanner = entries.some((entry) => failureBannerIds[entry.id] === true);
  const batchHead = failureBanner ? BATCH_FAILURE_FALLBACK : error;
  const blockedCount = entries.filter((entry) => isNothingOnHand(entry, household, pins)).length;
  const successBanner = batchHead === '' && !failedPending && partialLine !== '';
  const pill = nothingOnHandPill(blockedCount);
  const confirmAllVisible = entries.length > 1;
  // A single in-flight Confirm disables the button. The skip line stays, because Confirm all would still skip a card.
  const confirmAllWouldSkip = blockedCount > 0 && blockedCount < entries.length;
  const skipLine = confirmAllVisible && confirmAllWouldSkip ? confirmSkipsNothingOnHand(blockedCount) : null;

  return (
    <View style={[styles.screen, { paddingTop: insets.top + 18 }]}>
      <StatusBar style="dark" />
      <Text style={styles.heading} accessibilityRole="header">To confirm</Text>
      <Text style={styles.count}>{loading ? 'Checking the queue' : waiting}</Text>
      {pill !== null && <Text style={styles.pill}>{pill}</Text>}
      {warning !== '' && <Text style={styles.warning}>{warning}</Text>}
      {(batchHead !== '' || partialLine !== '') && (
        <View
          testID="batch-banner"
          accessibilityLiveRegion="polite"
          style={successBanner ? styles.successBanner : undefined}
        >
          {batchHead !== '' && <Text style={styles.error}>{batchHead}</Text>}
          {partialLine !== '' && (
            <Text style={successBanner ? styles.bannerBody : styles.partialLine}>{partialLine}</Text>
          )}
        </View>
      )}
      {loadError !== '' && <Text testID="queue-load-error" style={styles.error}>{loadError}</Text>}

      {loading ? (
        <ActivityIndicator color={colors.shelfDeep} style={styles.spinner} />
      ) : (
        <FlatList
          data={entries}
          keyExtractor={(entry) => entry.id}
          refreshControl={(
            <RefreshControl
              refreshing={refreshing}
              enabled={busyId !== 'all'}
              onRefresh={() => { if (busyId !== 'all') void refresh(); }}
              tintColor={colors.shelfDeep}
            />
          )}
          contentContainerStyle={entries.length === 0 ? styles.emptyWrap : styles.list}
          ListEmptyComponent={<Text style={styles.empty}>Nothing is waiting. A scan you take shows up here until you confirm it.</Text>}
          renderItem={({ item }) => {
            const blocked = isNothingOnHand(item, household, pins);
            const label = item.product?.name || item.barcode;
            return (
            <View
              testID={`scan-${item.id}`}
              style={[
                styles.row,
                blocked
                  ? styles.rowBlocked
                  : { borderLeftColor: item.direction === 'stock_out' ? colors.using : colors.shelf },
              ]}
            >
              <View style={styles.rowMain}>
                <View style={styles.rowCopy}>
                  <Text style={styles.name}>{label}</Text>
                  <Text style={styles.meta}>
                    {item.product?.name ? item.barcode : 'No name yet'}
                    {'  '}
                    {item.direction === null ? 'No direction' : modeLabel(item.direction)}
                  </Text>
                </View>
                {blocked ? (
                  // #294 replaces this Remove with "Add it and take it out".
                  <View>
                    <Pressable
                      accessibilityRole="button"
                      accessibilityLabel={`Remove ${label}`}
                      disabled={busyId !== ''}
                      onPress={() => { void removeOne(item.id); }}
                      style={styles.remove}
                    >
                      <Text style={styles.removeText}>{busyId === item.id ? 'Removing' : 'Remove'}</Text>
                    </Pressable>
                  </View>
                ) : (
                  <Pressable
                    accessibilityRole="button"
                    accessibilityLabel={`Confirm ${label}`}
                    disabled={busyId !== ''}
                    onPress={() => { void confirmOne(item.id); }}
                    style={styles.confirm}
                  >
                    <Text style={styles.confirmText}>{busyId === item.id ? 'Confirming' : 'Confirm'}</Text>
                  </Pressable>
                )}
              </View>
              {blocked && (
                <View style={styles.nothing}>
                  <View style={styles.nothingHead}>
                    <View style={styles.nothingMark}>
                      <Text style={styles.nothingMarkText}>!</Text>
                    </View>
                    <Text
                      style={styles.nothingTitle}
                      accessibilityLabel={`Scan ${item.barcode}, ${NOTHING_ON_HAND_TITLE}`}
                      accessibilityHint={NOTHING_ON_HAND_BODY}
                      accessibilityLiveRegion={pins.has(item.id) ? 'polite' : undefined}
                    >
                      {NOTHING_ON_HAND_TITLE}
                    </Text>
                  </View>
                  <Text style={styles.nothingBody}>{NOTHING_ON_HAND_BODY}</Text>
                </View>
              )}
              {notes[item.id] ? <Text style={styles.note}>{notes[item.id]}</Text> : null}
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
            );
          }}
        />
      )}

      {skipLine !== null && <Text style={styles.skip}>{skipLine}</Text>}
      {confirmAllVisible && (
        <Pressable
          accessibilityRole="button"
          accessibilityLabel="Confirm all"
          disabled={busyId !== '' || blockedCount === entries.length}
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
  pill: {
    alignSelf: 'flex-start',
    marginTop: -8,
    marginBottom: 12,
    paddingVertical: 4,
    paddingHorizontal: 10,
    borderRadius: 999,
    overflow: 'hidden',
    backgroundColor: colors.nothingWash,
    borderWidth: 1,
    borderColor: colors.nothingBorder,
    color: colors.nothingInk,
    fontFamily: fonts.bold,
    fontSize: 14,
    lineHeight: 20,
  },
  skip: {
    fontFamily: fonts.regular,
    fontSize: 15,
    lineHeight: 21,
    color: colors.nothingInk,
    marginBottom: 8,
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
  },
  partialLine: {
    fontFamily: fonts.regular,
    fontSize: 16,
    lineHeight: 22,
    color: colors.ink,
    marginBottom: 12,
  },
  successBanner: {
    backgroundColor: colors.successWash,
    borderColor: colors.successEdge,
    borderWidth: 1,
    borderRadius: 4,
    paddingVertical: 4,
    paddingHorizontal: 16,
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
  rowBlocked: {
    backgroundColor: colors.nothingWash,
    borderWidth: 1,
    borderColor: colors.nothingBorder,
    borderLeftWidth: 4,
    borderLeftColor: colors.nothingBorder,
    paddingRight: 12,
    marginBottom: 10,
  },
  nothing: {
    marginTop: 10,
  },
  nothingHead: {
    flexDirection: 'row',
    alignItems: 'flex-start',
    gap: 8,
  },
  nothingMark: {
    width: 22,
    height: 22,
    borderRadius: 11,
    backgroundColor: colors.nothingInk,
    alignItems: 'center',
    justifyContent: 'center',
    marginTop: 1,
  },
  nothingMarkText: {
    color: colors.nothingWash,
    fontFamily: fonts.bold,
    fontSize: 14,
    lineHeight: 18,
  },
  nothingTitle: {
    flex: 1,
    fontFamily: fonts.bold,
    fontSize: 16,
    lineHeight: 22,
    color: colors.nothingInk,
  },
  nothingBody: {
    fontFamily: fonts.regular,
    fontSize: 16,
    lineHeight: 22,
    color: colors.ink,
    marginTop: 4,
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
  remove: {
    minHeight: 44,
    minWidth: 88,
    alignItems: 'center',
    justifyContent: 'center',
    paddingHorizontal: 12,
    backgroundColor: colors.removeWash,
  },
  removeText: {
    fontFamily: fonts.bold,
    fontSize: 15,
    color: colors.danger,
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
