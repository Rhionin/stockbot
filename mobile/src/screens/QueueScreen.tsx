import { useCallback, useEffect, useRef, useState, useSyncExternalStore } from 'react';
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
import type { InventoryItem, PantryLinkSuggestion, ScanDirection, ScanEntry, ScannerConfig } from '@stockbot/shared/types';
import {
  batchTimeBounds,
  confirmableSelection,
  formatBatchScanCount,
  formatBatchTimeRange,
  getEntriesForView,
  groupScansIntoBatches,
  isBatchEligible,
  mergeScanEvent,
  selectableEntries,
  selectionState,
  toggleSelectAll,
} from '@stockbot/shared/queueUtils';
import {
  BATCH_CONFIRM_FAILURE,
  BATCH_REVIEW_BACK,
  CLEAR_SELECTION_LABEL,
  SELECT_ALL_IN_TAB_LABEL,
  SELECT_ALL_LABEL,
  batchConfirmLabel,
  batchReviewHeading,
  reviewSelectedLabel,
  selectAllInSessionLabel,
  selectScanLabel,
} from '@stockbot/shared/batchReview';
import {
  DIRECTION_TABS_LABEL,
  DIRECTION_TAB_LABEL,
  QUEUE_TITLE,
  SCAN_MODE_FAILURE,
} from '@stockbot/shared/queueHeader';
import { scannerModeFromEventData } from '@stockbot/shared/scannerModeEvent';
import { filterInventoryForScanLinkSearch } from '@stockbot/shared/scanLinkInventoryFilter';
import { ApiError, type StockbotClient } from '../api/client';
import { POLL_MS } from '../live/connection';
import { foregroundSource } from '../live/foreground';
import { useLiveEvents, type LiveEvent } from '../live/LiveEvents';
import {
  inlineFailure,
  ADD_IT_AND_TAKE_IT_OUT,
  ADDED_AND_TAKEN_OUT,
  backfillFailureKind,
  batchCardCommitNotice,
  batchFailureBannerText,
  batchFailureNotices,
  CODE_NONE_ON_HAND,
  failuresAfterConfirm,
  isStorageFailureCode,
  type BatchFailureNotice,
  offlineBannerActive,
  serverNoticeText,
  subscribeOfflineBanner,
  confirmSkipsNothingOnHand,
  commitConflictBehavior,
  formatPartialRemoval,
  NOTHING_ON_HAND_TITLE,
  nothingOnHandBody,
  nothingOnHandPill,
  NO_CLOSE_MATCH,
  SERVER_ERROR_FALLBACK,
} from '@stockbot/shared/contract';
import { mobileNothingOnHandCue } from './scanNothingOnHandRow';
import {
  BatchReviewPanel,
  DirectionHint,
  DirectionTabs,
  NoticeBanner,
  QueueCard,
  ScannerStatus,
  SelectCheckbox,
  SessionCard,
  UndoToastView,
  focusOnWeb,
} from '@stockbot/ui';
import { UNDO_ACTION_LABEL, UNDO_TOAST_MS, undoStillOffered } from '@stockbot/shared/undoToast';
import { modeLabel } from '../scanner/modeLabel';
import { CardDetails, type Flush } from './cardDetails';
import { colors, fonts } from '../ui/theme';
import { DESKTOP_PAGE_PADDING_TOP, useColumn, useDesktopLayout } from '../ui/layout';

function besideBanner(error: unknown, fallback: string): string {
  return inlineFailure(error, fallback) ?? '';
}

function storageFailure(error: unknown): { code: string; message: string } | null {
  if (!(error instanceof ApiError) || !isStorageFailureCode(error.code)) return null;
  return { code: error.code, message: error.message };
}

const LINK_OFFER_HEADING = 'Which item is this?';
const NO_MATCHING_PRODUCTS = 'No matching products.';

interface UndoOffer {
  id: string;
  text: string;
  moveId: string;
}

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

function isNothingOnHand(entry: ScanEntry, pins: Map<string, NothingPin>): boolean {
  return mobileNothingOnHandCue(entry, pins.has(entry.id));
}

function readJson(data: string): unknown {
  try {
    return JSON.parse(data);
  } catch {
    return null;
  }
}

function isScanEntry(value: unknown): value is ScanEntry {
  if (value === null || typeof value !== 'object') return false;
  const row = value as { id?: unknown; status?: unknown };
  return typeof row.id === 'string' && typeof row.status === 'string';
}

function isScansHint(value: unknown): boolean {
  if (value === null || typeof value !== 'object') return false;
  const type = (value as { type?: unknown }).type;
  return type === 'scans' || type === 'rekey';
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

function assignSessionKeys(
  sessions: ScanEntry[][],
  tokens: Map<string, string>,
): { keys: string[]; tokens: Map<string, string> } {
  const next = new Map<string, string>();
  const keys = sessions.map((session) => {
    const known = session.map((entry) => tokens.get(entry.id)).find((token) => token !== undefined);
    const key = known ?? `s-${session[0].id}`;
    for (const entry of session) next.set(entry.id, key);
    return key;
  });
  return { keys, tokens: next };
}

function sameTokens(current: Map<string, string>, next: Map<string, string>): boolean {
  if (current.size !== next.size) return false;
  for (const [id, key] of next) if (current.get(id) !== key) return false;
  return true;
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
  return filterInventoryForScanLinkSearch(rows, query, excludeProductId)
    .map((row) => ({
      key: row.item.id,
      name: row.item.product.name,
      onHand: row.instanceCount,
      productId: row.item.productId,
    }));
}

export function QueueScreen({ client, warning, focused = true }: QueueScreenProps) {
  const insets = useSafeAreaInsets();
  const column = useColumn();
  const desktop = useDesktopLayout();
  const [entries, setEntries] = useState<ScanEntry[]>([]);
  const shownIds = useRef(new Set<string>());
  const [loading, setLoading] = useState(true);
  const [refreshing, setRefreshing] = useState(false);
  const [error, setError] = useState('');
  const [loadError, setLoadError] = useState('');
  const [queueUnloaded, setQueueUnloaded] = useState(false);
  const [failureBannerIds, setFailureBannerIds] = useState<Record<string, BatchFailureNotice>>({});
  const [confirmedCount, setConfirmedCount] = useState(0);
  const [partialLine, setPartialLine] = useState('');
  const [selectedIds, setSelectedIds] = useState<string[]>([]);
  const [reviewing, setReviewing] = useState(false);
  const [reviewError, setReviewError] = useState('');
  const [sessionOpen, setSessionOpen] = useState<Record<string, boolean>>({});
  const [sessionTokens, setSessionTokens] = useState<Map<string, string>>(() => new Map());
  const reviewTrigger = useRef<unknown>(null);
  const headingRef = useRef<unknown>(null);
  const refreshGeneration = useRef(0);
  const loadsInFlight = useRef(0);
  const liveMerged = useRef(false);
  const focusedRef = useRef(focused);
  const [toast, setToastText] = useState('');
  const [undoOffer, setUndoOffer] = useState<UndoOffer | null>(null);
  const undoOfferRef = useRef<UndoOffer | null>(null);
  const undoSerial = useRef(0);
  const [instanceChoices, setInstanceChoices] = useState<Record<string, string>>({});
  const [instancesOpen, setInstancesOpen] = useState<Record<string, boolean>>({});
  const [instanceReloads, setInstanceReloads] = useState<Record<string, number>>({});
  const flushers = useRef(new Map<string, Flush>());
  const [busyId, setBusyId] = useState('');
  const [busyKind, setBusyKind] = useState<'remove' | 'backfill' | 'confirm' | 'link' | 'all' | ''>('');
  const [backfillErrors, setBackfillErrors] = useState<Record<string, string>>({});
  const [offers, setOffers] = useState<Record<string, LinkOfferState>>({});
  const [notes, setNotes] = useState<Record<string, string>>({});
  const [household, setHousehold] = useState<InventoryItem[] | null>(null);
  const [pins, setPins] = useState<Map<string, NothingPin>>(() => new Map());
  const [excluded, setExcluded] = useState<Record<string, string>>({});
  const [mode, setMode] = useState<ScanDirection | null>(null);
  const [scannerConnected, setScannerConnected] = useState<boolean | null>(null);
  const [foreground, setForeground] = useState(() => foregroundSource.isActive());
  const modeRef = useRef<ScanDirection | null>(null);
  const modeEpoch = useRef(0);
  const mounted = useRef(true);

  const notice = useSyncExternalStore(subscribeOfflineBanner, serverNoticeText, () => '');
  const offline = offlineBannerActive();

  const storeHousehold = useCallback((rows: InventoryItem[] | null, listed: ScanEntry[]) => {
    if (rows === null) return;
    setHousehold(rows);
    setPins((current) => reconcilePins(current, listed, rows));
  }, []);

  const dropGoneCards = useCallback((next: ScanEntry[]) => {
    setFailureBannerIds((current) => {
      const kept: Record<string, BatchFailureNotice> = {};
      for (const entry of next) {
        const notice = current[entry.id];
        if (notice !== undefined) kept[entry.id] = notice;
      }
      return kept;
    });
    setOffers((current) => keepIds(current, next));
    setNotes((current) => keepIds(current, next));
    setBackfillErrors((current) => keepIds(current, next));
    setExcluded((current) => keepIds(current, next));
    setInstanceChoices((current) => keepIds(current, next));
    setInstancesOpen((current) => keepIds(current, next));
    setInstanceReloads((current) => keepIds(current, next));
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
    setQueueUnloaded(false);
    dropGoneCards(next);
    setLoading(false);
    setRefreshing(false);
  }, [dropGoneCards]);

  const loadHousehold = useCallback(async () => {
    try {
      const rows = await client.getInventoryList();
      return readableInventory(rows);
    } catch {
      return null;
    }
  }, [client]);

  const refresh = useCallback(async (keepError = '', surfaceError = true, quiet = false) => {
    const generation = refreshGeneration.current + 1;
    refreshGeneration.current = generation;
    loadsInFlight.current += 1;
    if (!quiet) setRefreshing(true);
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
        setLoadError(besideBanner(requestError, 'The queue did not load.'));
      }
    } finally {
      loadsInFlight.current -= 1;
    }
  }, [applyEntries, client, loadHousehold, storeHousehold]);

  useEffect(() => {
    let active = true;
    const generation = refreshGeneration.current + 1;
    refreshGeneration.current = generation;
    loadsInFlight.current += 1;
    void (async () => {
      try {
        const next = await client.listPending();
        if (!active || refreshGeneration.current !== generation) return;
        shownIds.current = new Set(next.map((entry) => entry.id));
        setEntries(next);
        setError('');
        setLoadError('');
        setQueueUnloaded(false);
        setLoading(false);
        const rows = await loadHousehold();
        if (!active) return;
        storeHousehold(rows, next);
      } catch (requestError: unknown) {
        if (!active || refreshGeneration.current !== generation) return;
        setLoading(false);
        const message = inlineFailure(requestError, 'The queue did not load.');
        if (message === null) setQueueUnloaded(true);
        setLoadError(message ?? '');
      } finally {
        loadsInFlight.current -= 1;
      }
    })();
    return () => {
      active = false;
    };
  }, [client, loadHousehold, storeHousehold]);

  useEffect(() => {
    focusedRef.current = focused;
  }, [focused]);

  useEffect(() => {
    mounted.current = true;
    return () => {
      mounted.current = false;
    };
  }, []);

  useEffect(() => foregroundSource.subscribe(setForeground), []);

  const acceptMode = useCallback((next: ScanDirection) => {
    if (modeRef.current !== next) {
      setSelectedIds([]);
      setReviewing(false);
      setReviewError('');
    }
    modeRef.current = next;
    setMode(next);
  }, []);

  // A response that started before a switch, local or from another device,
  // must not put the earlier direction back.
  const loadScannerConfig = useCallback(() => {
    const epoch = modeEpoch.current;
    client.getScannerConfig().then((config: ScannerConfig) => {
      if (!mounted.current) return;
      if (typeof config.connected === 'boolean') setScannerConnected(config.connected);
      if (modeEpoch.current !== epoch) return;
      if (config.currentMode === 'stock_in' || config.currentMode === 'stock_out') acceptMode(config.currentMode);
      else if (modeRef.current === null) acceptMode('stock_in');
    }).catch(() => {
      // With no answer at all the queue would load forever, so the first miss settles on stock in.
      if (mounted.current && modeRef.current === null) acceptMode('stock_in');
    });
  }, [acceptMode, client]);

  // The server publishes no event when the scanner connects or drops, so this
  // is polled, and only while the screen is in front and the app is awake.
  const watchingScanner = focused && foreground;
  // The list waits on the direction, so a screen that mounts out of view still asks once.
  useEffect(() => {
    if (!watchingScanner) loadScannerConfig();
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);
  useEffect(() => {
    if (!watchingScanner) return;
    loadScannerConfig();
    const timer = setInterval(loadScannerConfig, POLL_MS);
    return () => clearInterval(timer);
  }, [loadScannerConfig, watchingScanner]);

  useEffect(() => {
    undoOfferRef.current = undoOffer;
    if (undoOffer === null) return;
    const offered = undoOffer.id;
    const timer = setTimeout(() => {
      if (undoOfferRef.current?.id === offered) setUndoOffer(null);
    }, UNDO_TOAST_MS);
    return () => clearTimeout(timer);
  }, [undoOffer]);

  // A new sentence replaces an Undo that is still showing, and an empty one leaves it alone.
  function setToast(text: string) {
    setToastText(text);
    if (text !== '') setUndoOffer(null);
  }

  function offerUndo(text: string, moveId: string) {
    undoSerial.current += 1;
    setToast('');
    setUndoOffer({ id: `undo-${undoSerial.current}`, text, moveId });
  }

  function runUndo(offeredId: string) {
    const offer = undoOfferRef.current;
    if (offer === null || !undoStillOffered(offer.id, offeredId)) return;
    undoOfferRef.current = null;
    setUndoOffer(null);
    client.undoMove(offer.moveId)
      .then(() => {
        if (mounted.current) void refresh('', false, true);
      })
      .catch((requestError: unknown) => {
        if (!mounted.current) return;
        const message = inlineFailure(requestError, SERVER_ERROR_FALLBACK);
        if (message !== null) setToast(message || SERVER_ERROR_FALLBACK);
      });
  }

  const registerFlush = useCallback((entryId: string, flush: Flush | null) => {
    if (flush === null) flushers.current.delete(entryId);
    else flushers.current.set(entryId, flush);
  }, []);

  function chosenInstance(id: string): string {
    return instanceChoices[id] ?? '';
  }

  function setEntrySelected(id: string, selected: boolean) {
    setSelectedIds((current) => (selected ? [...new Set([...current, id])] : current.filter((picked) => picked !== id)));
  }

  function chooseInstance(id: string, instanceId: string) {
    setInstanceChoices((current) => ({ ...current, [id]: instanceId }));
  }

  function toggleInstances(id: string) {
    setInstancesOpen((current) => ({ ...current, [id]: !current[id] }));
  }

  async function commitWith(id: string, linkProductId?: string) {
    const instanceId = chosenInstance(id);
    if (instanceId !== '') return client.commitScan(id, linkProductId, instanceId);
    return linkProductId === undefined ? client.commitScan(id) : client.commitScan(id, linkProductId);
  }

  async function chooseMode(next: ScanDirection) {
    if (next === modeRef.current) return;
    const epoch = modeEpoch.current + 1;
    modeEpoch.current = epoch;
    acceptMode(next);
    setPartialLine('');
    try {
      const response = await client.setScannerMode(next);
      if (mounted.current && modeEpoch.current === epoch) acceptMode(response.mode);
    } catch (requestError) {
      if (!mounted.current || modeEpoch.current !== epoch) return;
      setToast(besideBanner(requestError, SCAN_MODE_FAILURE));
      loadScannerConfig();
    }
  }

  const applyScanEvent = useCallback((event: ScanEntry) => {
    const stockedIn = event.status === 'committed' && event.direction === 'stock_in';
    const reload = loadsInFlight.current > 0 || stockedIn;
    setEntries((current) => mergeScanEvent(current, event));
    liveMerged.current = true;
    setPartialLine('');
    // A load that started before this event may not include it, so the list
    // is fetched again and that newer response wins.
    if (reload) void refresh('', false, true);
  }, [refresh]);

  useEffect(() => {
    if (!liveMerged.current) return;
    liveMerged.current = false;
    shownIds.current = new Set(entries.map((entry) => entry.id));
    dropGoneCards(entries);
    setPins((held) => {
      const kept = new Map([...held].filter(([id]) => shownIds.current.has(id)));
      return kept.size === held.size ? held : kept;
    });
  }, [entries, dropGoneCards]);

  useLiveEvents((event: LiveEvent) => {
    if (event.kind === 'refresh') {
      loadScannerConfig();
      if (focusedRef.current) void refresh('', false, true);
      return;
    }
    if (event.event === 'scanner_mode') {
      const next = scannerModeFromEventData(event.data);
      if (next === null) return;
      modeEpoch.current += 1;
      acceptMode(next);
      return;
    }
    const body = readJson(event.data);
    if (body === null) return;
    if (event.event === 'scan' && isScanEntry(body)) {
      applyScanEvent(body);
    } else if (event.event === 'hint') {
      loadScannerConfig();
      if (isScansHint(body)) void refresh('', false, true);
    }
  });

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

  function clearBackfillError(id: string) {
    setBackfillErrors((current) => {
      if (!(id in current)) return current;
      const next = { ...current };
      delete next[id];
      return next;
    });
  }

  // A confirm that succeeds drops the lock and disk notices, and the card just
  // confirmed. Item-specific codes stay until that card is resolved. A confirm
  // that fails leaves the banner up.
  // A lock or a full disk is the banner, including the first time one card fails.
  // A notice already on that card stays, and nothing about it is toasted. A new
  // one is not the batch that saved earlier scans, so that count does not prefix it.
  function noteStorageFailure(id: string, code: string, message: string) {
    setToast('');
    setFailureBannerIds((current) => {
      if (current[id] !== undefined) return current;
      setConfirmedCount(0);
      return { ...current, [id]: { id, code, message } };
    });
  }

  function retireSingleConfirm(id: string) {
    setFailureBannerIds((current) => {
      const next = failuresAfterConfirm(Object.values(current), id);
      if (next.length === Object.keys(current).length) return current;
      return Object.fromEntries(next.map((notice) => [notice.id, notice]));
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
    const storage = storageFailure(requestError);
    if (storage !== null) {
      noteStorageFailure(id, storage.code, storage.message);
      return;
    }
    const behavior = commitConflictBehavior(requestError.code);
    setToast(behavior.showMessage ? (inlineFailure(requestError, '') ?? '') : '');
    clearNote(id);
    if (behavior.refreshInstances) {
      setInstancesOpen((current) => ({ ...current, [id]: true }));
      chooseInstance(id, '');
      setInstanceReloads((current) => ({ ...current, [id]: (current[id] ?? 0) + 1 }));
    }
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

  async function showBackfillFailure(id: string, requestError: ApiError) {
    const kind = backfillFailureKind(requestError.code);
    if (kind === 'define') {
      await showCommitFailure(id, requestError);
      return;
    }
    const fromInline = inlineFailure(requestError, SERVER_ERROR_FALLBACK);
    const message = (fromInline !== null && fromInline !== '')
      ? fromInline
      : SERVER_ERROR_FALLBACK;
    clearNote(id);
    setToast('');
    setBackfillErrors((current) => ({ ...current, [id]: message }));
    if (kind === 'stock-out' || kind === 'reload') await refresh();
  }

  async function backfillOne(id: string) {
    setBusyId(id);
    setBusyKind('backfill');
    setError('');
    setPartialLine('');
    clearBackfillError(id);
    setToast('');
    try {
      if ((await flushers.current.get(id)?.()) === false) return;
      const result = await client.commitBackfill(id);
      retireSingleConfirm(id);
      clearOffer(id);
      clearNote(id);
      clearBackfillError(id);
      setPartialLine('');
      const moveId = result?.backfill?.stockInMoveId ?? '';
      if (moveId === '') setToast(ADDED_AND_TAKEN_OUT);
      else offerUndo(ADDED_AND_TAKEN_OUT, moveId);
      await refresh();
    } catch (requestError) {
      if (requestError instanceof ApiError) {
        await showBackfillFailure(id, requestError);
      } else {
        const banner = besideBanner(requestError, SERVER_ERROR_FALLBACK);
        setToast('');
        setBackfillErrors((current) => ({
          ...current,
          [id]: banner !== '' ? banner : SERVER_ERROR_FALLBACK,
        }));
      }
    } finally {
      setBusyId('');
      setBusyKind('');
    }
  }

  async function confirmOne(id: string) {
    setBusyId(id);
    setBusyKind('confirm');
    setError('');
    setPartialLine('');
    setToast('');
    try {
      if ((await flushers.current.get(id)?.()) === false) return;
      const committed = await commitWith(id);
      retireSingleConfirm(id);
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
        setToast(besideBanner(requestError, 'That scan could not be confirmed.'));
      }
    } finally {
      setBusyId('');
      setBusyKind('');
    }
  }

  async function removeOne(id: string) {
    setBusyId(id);
    setBusyKind('remove');
    setError('');
    setToast('');
    try {
      const removed = await client.cancelScan(id);
      if (removed.status !== 'cancelled') {
        setToast('That scan could not be removed.');
        return;
      }
      refreshGeneration.current += 1;
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
      const storage = storageFailure(requestError);
      if (storage !== null) {
        noteStorageFailure(id, storage.code, storage.message);
        return;
      }
      setToast(besideBanner(requestError, 'That scan could not be removed.'));
    } finally {
      setBusyId('');
      setBusyKind('');
    }
  }

  async function linkOne(id: string, productId: string) {
    const previous = offers[id]?.suggestions ?? [];
    setBusyId(id);
    setBusyKind('link');
    setError('');
    setPartialLine('');
    setToast('');
    try {
      const committed = await commitWith(id, productId);
      retireSingleConfirm(id);
      clearOffer(id);
      clearNote(id);
      setPartialLine('');
      setToast(formatPartialRemoval(committed) ?? '');
      if (committed.direction === 'stock_in' && committed.productId) releasePins(committed.productId);
      await refresh();
    } catch (requestError) {
      const storage = storageFailure(requestError);
      if (storage !== null) {
        noteStorageFailure(id, storage.code, storage.message);
      } else if (requestError instanceof ApiError && commitConflictBehavior(requestError.code).refreshSuggestions) {
        setToast(besideBanner(requestError, ''));
        clearNote(id);
        setExcluded((current) => ({ ...current, [id]: productId }));
        storeHousehold(await loadHousehold(), entries);
        showOffer(id, previous.filter((row) => row.productId !== productId), true);
      } else if (requestError instanceof ApiError && requestError.code === CODE_NONE_ON_HAND) {
        rememberPin(id);
      } else if (requestError instanceof ApiError) {
        await showCommitFailure(id, requestError);
      } else {
        setToast(besideBanner(requestError, 'That scan could not be confirmed.'));
      }
    } finally {
      setBusyId('');
      setBusyKind('');
    }
  }

  async function confirmAll() {
    await confirmBatch(viewEntries.filter((entry) => !isNothingOnHand(entry, pins)), false);
  }

  async function confirmSelected() {
    await confirmBatch(selection, true);
  }

  async function confirmBatch(confirmable: ScanEntry[], fromSelection: boolean) {
    if (confirmable.length === 0) return;
    setReviewError('');
    refreshGeneration.current += 1;
    setRefreshing(false);
    setBusyId('all');
    setBusyKind('all');
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
        const cardNotice = batchCardCommitNotice(notice.code, notice.message);
        if (behavior.showMessage && cardNotice !== '') nextNotes[notice.id] = cardNotice;
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
      const amberLeft = notices.filter((notice) => notice.code === CODE_NONE_ON_HAND && stillThere(notice.id)).map((notice) => notice.id);
      const pendingFailures: Record<string, BatchFailureNotice> = {};
      for (const notice of notices) {
        if (notice.code === CODE_NONE_ON_HAND) continue;
        if (stillThere(notice.id)) pendingFailures[notice.id] = notice;
      }
      setFailureBannerIds(pendingFailures);
      setSelectedIds(fromSelection ? Object.keys(pendingFailures) : []);
      setReviewing(false);
      focusOnWeb(headingRef.current);
      // The prefix belongs to this batch. A batch that saved everything leaves
      // nothing for a later lock or full disk to inherit.
      setConfirmedCount(Object.keys(pendingFailures).length === 0 ? 0 : (result.committedIds?.length ?? 0));
      // The banner is derived from the notices still listed. A card that already
      // left, including one beside an amber card, does not keep a sentence.
      setError('');
      setPartialLine(formatPartialRemoval(result, names) ?? '');
      setLoadError('');
      if (amberLeft.length > 0) AccessibilityInfo.announceForAccessibility(NOTHING_ON_HAND_TITLE);
      setLoading(false);
      setRefreshing(false);
    } catch (requestError) {
      const message = besideBanner(requestError, BATCH_CONFIRM_FAILURE);
      if (fromSelection) setReviewError(message);
      else setError(message);
    } finally {
      setBusyId('');
      setBusyKind('');
    }
  }

  const viewEntries = mode === null ? [] : getEntriesForView(entries, mode);
  const stockInCount = getEntriesForView(entries, 'stock_in').length;
  const stockOutCount = entries.length - stockInCount;
  const failedPending = entries.some((entry) => (notes[entry.id] ?? '') !== '');
  const openNotices = entries.flatMap((entry) => {
    const stored = failureBannerIds[entry.id];
    return stored === undefined ? [] : [stored];
  });
  const derivedBanner = batchFailureBannerText(openNotices, confirmedCount) ?? '';
  const shownPartial = offline ? '' : partialLine;
  const batchHead = offline || derivedBanner !== '' ? '' : error;
  const blockedIds = new Set(viewEntries.filter((entry) => isNothingOnHand(entry, pins)).map((entry) => entry.id));
  const blockedCount = blockedIds.size;
  const selectable = selectableEntries(viewEntries, blockedIds);
  const selection = confirmableSelection(selectedIds, viewEntries, blockedIds);
  const sessions = groupScansIntoBatches(viewEntries);
  const directionName = mode === 'stock_out' ? 'stock out' : 'stock in';
  const tabState = selectionState(selectable, selectedIds);
  const tabSelectionChecked = tabState === 'all' ? true : tabState === 'some' ? 'mixed' : false;
  const reviewOpen = reviewing && selection.length > 0;
  // Each scan keeps the key of the first session it joined, so a scan that
  // commits does not remount the cards still open beside it.
  const { keys: sessionKeys, tokens: nextTokens } = assignSessionKeys(sessions, sessionTokens);
  if (!sameTokens(sessionTokens, nextTokens)) setSessionTokens(nextTokens);

  const selectableIds = new Set(selectable.map((entry) => entry.id));
  if (selectedIds.some((id) => !selectableIds.has(id))) {
    setSelectedIds(selectedIds.filter((id) => selectableIds.has(id)));
  }
  if (reviewing && selection.length === 0) setReviewing(false);
  const successBanner = derivedBanner === '' && batchHead === '' && !failedPending && shownPartial !== '';
  const pinned = notice !== '' || derivedBanner !== '';
  const pill = nothingOnHandPill(blockedCount);
  const confirmAllVisible = viewEntries.length > 1;
  // A single in-flight Confirm disables the button. The skip line stays, because Confirm all would still skip a card.
  const confirmAllWouldSkip = blockedCount > 0 && blockedCount < viewEntries.length;
  const skipLine = confirmAllVisible && confirmAllWouldSkip ? confirmSkipsNothingOnHand(blockedCount) : null;

  function renderCard(item: ScanEntry) {
    const blocked = isNothingOnHand(item, pins);
    const label = item.product?.name || item.barcode;
    const metaLine = `${item.product?.name ? item.barcode : 'No name yet'}  ${item.direction === null ? 'No direction' : modeLabel(item.direction)}`;
    const offer = offers[item.id];
    const nothingBody = nothingOnHandBody(item.unitCount);
    const backfillBusy = busyId === item.id && busyKind === 'backfill';
    return (
      <QueueCard
        key={item.id}
        entryId={item.id}
        label={label}
        barcode={item.barcode}
        metaLine={metaLine}
        stockIn={item.direction !== 'stock_out'}
        stockOut={item.direction === 'stock_out'}
        blocked={blocked}
        busy={busyId !== ''}
        busyThis={busyId === item.id}
        busyConfirm={busyId === item.id && busyKind === 'confirm'}
        busyRemove={busyId === item.id && busyKind === 'remove'}
        leading={(
          <SelectCheckbox
            label={selectScanLabel(label)}
            checked={selectedIds.includes(item.id) && !blocked}
            disabled={blocked || !isBatchEligible(item)}
            fontRegular={fonts.regular}
            onChange={(next) => setEntrySelected(item.id, next)}
          />
        )}
        pinLive={pins.has(item.id)}
        note={notes[item.id]}
        nothingBody={nothingBody}
        fontRegular={fonts.regular}
        fontBold={fonts.bold}
        onConfirm={() => { void confirmOne(item.id); }}
        onRemove={() => { void removeOne(item.id); }}
        details={item.status === 'pending' ? (
          <CardDetails
            client={client}
            entry={item}
            blocked={blocked}
            linkOfferOpen={offer !== undefined}
            itemId={household?.find((row) => row.item.productId === item.productId)?.item.id}
            instanceId={chosenInstance(item.id)}
            instancesOpen={instancesOpen[item.id] === true}
            instanceReload={instanceReloads[item.id] ?? 0}
            busy={busyId !== ''}
            register={registerFlush}
            onInstanceChange={(instanceId) => chooseInstance(item.id, instanceId)}
            onToggleInstances={() => toggleInstances(item.id)}
            onChanged={() => { void refresh('', false, true); }}
            onStorageFailure={(code, message) => noteStorageFailure(item.id, code, message)}
          />
        ) : undefined}
        blockedExtra={blocked ? (
          <>
            <Pressable
              accessibilityRole="button"
              accessibilityLabel={`${ADD_IT_AND_TAKE_IT_OUT} ${label}`}
              disabled={busyId !== ''}
              onPress={() => { void backfillOne(item.id); }}
              style={styles.backfill}
            >
              <View style={styles.backfillRow}>
                <View style={styles.backfillSpinnerSlot} importantForAccessibility="no-hide-descendants">
                  {backfillBusy ? (
                    <ActivityIndicator accessible={false} color={colors.white} size="small" />
                  ) : null}
                </View>
                <Text style={styles.backfillText}>{ADD_IT_AND_TAKE_IT_OUT}</Text>
              </View>
            </Pressable>
            {backfillErrors[item.id] ? (
              <Text
                testID={`backfill-error-${item.id}`}
                style={styles.backfillError}
                accessibilityRole="alert"
              >
                {backfillErrors[item.id]}
              </Text>
            ) : null}
          </>
        ) : undefined}
        footer={offer ? (
          <LinkOffer
            key={offer.showEmptyLine ? `${item.id}-empty` : `${item.id}-pick`}
            suggestions={offer.suggestions}
            showEmptyLine={offer.showEmptyLine}
            busy={busyId === item.id}
            household={household}
            excludeProductId={excluded[item.id] ?? ''}
            onLink={(productId) => { void linkOne(item.id, productId); }}
            onDismiss={() => clearOffer(item.id)}
          />
        ) : undefined}
      />
    );
  }

  return (
    <View role="main" style={styles.screen}>
      <StatusBar style="dark" />
      <NoticeBanner topInset={insets.top} text={derivedBanner} fontRegular={fonts.regular} fontBold={fonts.bold} />
      <View style={[styles.body, column, { paddingTop: pinned ? 0 : desktop ? DESKTOP_PAGE_PADDING_TOP : insets.top + 18 }]}>
      <Text ref={headingRef as never} {...{ tabIndex: -1 }} style={styles.heading} accessibilityRole="header">{QUEUE_TITLE}</Text>
      <View style={styles.header}>
        {mode !== null && <DirectionHint mode={mode} fontBold={fonts.bold} />}
        {!offline && <ScannerStatus connected={scannerConnected} fontRegular={fonts.regular} />}
        <View style={styles.tabs}>
          <DirectionTabs
            label={DIRECTION_TABS_LABEL}
            fontBold={fonts.bold}
            value={mode}
            onChange={(next) => { void chooseMode(next); }}
            tabs={[
              { value: 'stock_in', label: DIRECTION_TAB_LABEL.stock_in, count: stockInCount },
              { value: 'stock_out', label: DIRECTION_TAB_LABEL.stock_out, count: stockOutCount },
            ]}
          />
        </View>
      </View>
      {pill !== null && <Text style={styles.pill}>{pill}</Text>}
      {warning !== '' && <Text style={styles.warning}>{warning}</Text>}
      {(batchHead !== '' || shownPartial !== '') && (
        <View
          testID="batch-banner"
          accessibilityLiveRegion="polite"
          style={successBanner ? styles.successBanner : undefined}
        >
          {batchHead !== '' && <Text style={styles.error}>{batchHead}</Text>}
          {shownPartial !== '' && (
            <Text style={successBanner ? styles.bannerBody : styles.partialLine}>{shownPartial}</Text>
          )}
        </View>
      )}
      {loadError !== '' && <Text testID="queue-load-error" style={styles.error}>{loadError}</Text>}

      {loading || mode === null ? (
        <ActivityIndicator color={colors.shelfDeep} style={styles.spinner} />
      ) : (
        <FlatList
          data={sessions}
          keyExtractor={(session, index) => sessionKeys[index] ?? session[0].id}
          refreshControl={(
            <RefreshControl
              refreshing={refreshing}
              enabled={busyId !== 'all'}
              onRefresh={() => { if (busyId !== 'all') void refresh(); }}
              tintColor={colors.shelfDeep}
            />
          )}
          contentContainerStyle={viewEntries.length === 0 ? styles.emptyWrap : styles.list}
          ListHeaderComponent={sessions.length > 1 ? (
            <SelectCheckbox
              testID="select-all-tab"
              label={SELECT_ALL_IN_TAB_LABEL}
              visibleLabel={SELECT_ALL_LABEL}
              checked={tabSelectionChecked}
              disabled={selectable.length === 0}
              fontRegular={fonts.regular}
              onChange={() => setSelectedIds((current) => toggleSelectAll(selectable, current))}
            />
          ) : undefined}
          ListEmptyComponent={queueUnloaded ? undefined : <Text style={styles.empty}>Nothing is waiting. A scan you take shows up here until you confirm it.</Text>}
          renderItem={({ item: session, index }) => {
            const key = sessionKeys[index] ?? session[0].id;
            const { earliest, latest } = batchTimeBounds(session);
            const timeRange = formatBatchTimeRange(earliest, latest);
            const sessionSelectable = selectableEntries(session, blockedIds);
            const sessionState = selectionState(sessionSelectable, selectedIds);
            const expanded = sessionOpen[key] ?? index === 0;
            const sessionBlocked = session.filter((entry) => blockedIds.has(entry.id)).length;
            return (
              <SessionCard
                sessionId={key}
                timeRange={timeRange}
                countLabel={formatBatchScanCount(session.length)}
                directionLabel={directionName}
                pill={sessions.length > 1 ? nothingOnHandPill(sessionBlocked) : null}
                expanded={expanded}
                selectAll={{
                  label: selectAllInSessionLabel(timeRange),
                  visibleLabel: SELECT_ALL_LABEL,
                  checked: sessionState === 'all' ? true : sessionState === 'some' ? 'mixed' : false,
                  disabled: sessionSelectable.length === 0,
                  onChange: () => setSelectedIds((current) => toggleSelectAll(sessionSelectable, current)),
                }}
                fontRegular={fonts.regular}
                fontBold={fonts.bold}
                onToggle={() => setSessionOpen((current) => ({ ...current, [key]: !(current[key] ?? index === 0) }))}
              >
                {session.map((entry) => renderCard(entry))}
              </SessionCard>
            );
          }}
        />
      )}

      {skipLine !== null && !reviewOpen && <Text style={styles.skip}>{skipLine}</Text>}
      {reviewOpen && (
        <BatchReviewPanel
          heading={batchReviewHeading(selection.length)}
          rows={selection.map((entry) => ({
            id: entry.id,
            label: entry.product?.name || entry.barcode,
            detail: entry.unitCount > 1 ? `\u00d7${entry.unitCount}` : undefined,
          }))}
          skipLine={blockedCount > 0 ? confirmSkipsNothingOnHand(blockedCount) : null}
          error={reviewError}
          busy={busyId === 'all'}
          confirmLabel={batchConfirmLabel(selection.length)}
          confirmName={`Confirm ${selection.length} selected scans`}
          backLabel={BATCH_REVIEW_BACK}
          fontRegular={fonts.regular}
          fontBold={fonts.bold}
          onConfirm={() => { void confirmSelected(); }}
          onBack={() => {
            setReviewing(false);
            setReviewError('');
            setTimeout(() => focusOnWeb(reviewTrigger.current), 0);
          }}
        />
      )}
      {!reviewOpen && selection.length > 0 && (
        <View style={styles.selectionBar}>
          <Pressable
            ref={reviewTrigger as never}
            accessibilityRole="button"
            accessibilityLabel={reviewSelectedLabel(selection.length)}
            disabled={busyId !== ''}
            onPress={() => setReviewing(true)}
            style={[styles.confirmAll, styles.reviewButton]}
          >
            <Text style={styles.confirmAllText}>{reviewSelectedLabel(selection.length)}</Text>
          </Pressable>
          <Pressable
            accessibilityRole="button"
            accessibilityLabel={CLEAR_SELECTION_LABEL}
            onPress={() => setSelectedIds([])}
            style={styles.clearButton}
          >
            <Text style={styles.dismissText}>{CLEAR_SELECTION_LABEL}</Text>
          </Pressable>
        </View>
      )}
      {confirmAllVisible && selection.length === 0 && (
        <Pressable
          accessibilityRole="button"
          accessibilityLabel="Confirm all"
          disabled={busyId !== '' || blockedCount === viewEntries.length}
          onPress={() => { void confirmAll(); }}
          style={styles.confirmAll}
        >
          <Text style={styles.confirmAllText}>{busyId === 'all' ? 'Confirming' : 'Confirm all'}</Text>
        </Pressable>
      )}

      {(undoOffer !== null || toast !== '') && (
        <UndoToastView
          message={undoOffer?.text ?? toast}
          actionLabel={undoOffer === null ? undefined : UNDO_ACTION_LABEL}
          onAction={undoOffer === null ? undefined : () => runUndo(undoOffer.id)}
          bottom={insets.bottom + 16}
          fontRegular={fonts.regular}
          fontBold={fonts.bold}
        />
      )}
      </View>
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
  },
  body: {
    flex: 1,
    paddingHorizontal: 20,
  },
  heading: {
    fontFamily: fonts.bold,
    fontSize: 36,
    lineHeight: 42,
    color: colors.ink,
  },
  header: {
    marginTop: 6,
    marginBottom: 16,
    gap: 6,
  },
  tabs: {
    marginTop: 8,
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
  backfill: {
    marginTop: 12,
    alignSelf: 'flex-start',
    minHeight: 44,
    minWidth: 44,
    alignItems: 'center',
    justifyContent: 'center',
    paddingHorizontal: 14,
    backgroundColor: colors.shelfDeep,
  },
  backfillRow: {
    flexDirection: 'row',
    alignItems: 'center',
    gap: 8,
  },
  backfillSpinnerSlot: {
    width: 20,
    height: 20,
    alignItems: 'center',
    justifyContent: 'center',
  },
  backfillText: {
    fontFamily: fonts.bold,
    fontSize: 15,
    color: colors.white,
  },
  backfillError: {
    marginTop: 10,
    fontFamily: fonts.regular,
    fontSize: 16,
    lineHeight: 22,
    color: colors.nothingInk,
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
  selectionBar: {
    flexDirection: 'row',
    alignItems: 'center',
    gap: 12,
    marginBottom: 12,
  },
  reviewButton: {
    flex: 1,
    marginBottom: 0,
  },
  clearButton: {
    minHeight: 52,
    minWidth: 72,
    alignItems: 'center',
    justifyContent: 'center',
    borderWidth: 1,
    borderColor: colors.inkMuted,
    paddingHorizontal: 12,
  },
  confirmAllText: {
    fontFamily: fonts.bold,
    fontSize: 18,
    color: colors.white,
  },
});
