import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { notifications } from '@mantine/notifications';
import { trackEventSource } from '../../telemetry/client';
import { Alert, Badge, Checkbox, Group, Loader, Tabs, Stack, Text, Title } from '@mantine/core';
import { createScanEntry, DEFAULT_SCAN_USER_ID, getInventoryList, getScannerConfig, listScanEntries, setScannerMode } from '../../api/client';
import { batchCardCommitNotice, batchFailureBannerText, batchFailureNotices, CODE_BUSY, CODE_NONE_ON_HAND, CODE_NO_SPACE, confirmSkipsNothingOnHand, formatPartialRemoval, shelfHasNothingOnHand, type BatchCommitResponse, type BatchFailureNotice } from '../../api/contract';
import type { InventoryItem, ProcessingFailure, ProcessingNotice, ScanEntry, ScannerConfig } from '../../types';
import { mergeInventoryEvent } from '../inventory/inventoryUtils';
import { BarcodeInputField } from '../scanner/BarcodeInputField';
import { focusBarcodeScanner } from '../scanner/focusBarcodeScanner';
import { CameraScanner } from '../scanner/CameraScanner';
import { BatchReviewPanel } from './BatchReviewPanel';
import { ProcessingScanCard } from './ProcessingScanCard';
import { QrScanCard } from './QrScanCard';
import { ScanEntryCard } from './ScanEntryCard';
import { ScanSessionCard } from './ScanSessionCard';
import { SCAN_ALERT_OPEN, takeScanAlertDirection, type ScanAlertOpenDetail } from './browserScanAlert';
import { addProcessingNotice, entryMatchesView, formatReviewCount, getEntriesForView, groupScansIntoBatches, isBatchEligible, mergeScanEvent, pruneSelection, removeProcessingNotice, settleProcessingNotice, sortScansNewestFirst, toggleSelectAll } from './queueUtils';
import { scannerModeFromEventData } from './scannerModeEvent';

const DEFAULT_USER_ID = 'user-1' satisfies typeof DEFAULT_SCAN_USER_ID;

interface NothingPin {
  onHand: number | null;
  observed: boolean;
}

// A reload that still reports the same count keeps the pin. A different count
// means the shelf moved, so the card can leave the amber state on its own.
const reconcilePins = (
  current: Map<string, NothingPin>,
  items: InventoryItem[],
  listed: ScanEntry[],
): Map<string, NothingPin> => {
  if (current.size === 0) return current;
  const counts = new Map(items.map((item) => [item.item.productId, item.instanceCount]));
  let changed = false;
  const next = new Map(current);
  for (const [id, pin] of current) {
    const entry = listed.find((row) => row.id === id);
    if (entry === undefined || entry.productId === null || entry.status !== 'pending') {
      next.delete(id);
      changed = true;
      continue;
    }
    const onHand = counts.has(entry.productId) ? (counts.get(entry.productId) ?? null) : null;
    if (!pin.observed) {
      next.set(id, { onHand, observed: true });
      changed = true;
      continue;
    }
    if (pin.onHand !== onHand) {
      next.delete(id);
      changed = true;
    }
  }
  return changed ? next : current;
};

export type QueueView = 'stock_in' | 'stock_out';

export interface ScanQueuePageProps {
  userId?: string;
}

export const ScanQueuePage = ({ userId = DEFAULT_USER_ID }: ScanQueuePageProps) => {
  const [entries, setEntries] = useState<ScanEntry[]>([]);
  const [inventory, setInventory] = useState<InventoryItem[]>([]);
  const [inventoryReady, setInventoryReady] = useState(false);
  const [pinnedNone, setPinnedNone] = useState<Map<string, NothingPin>>(() => new Map());
  const [selectedIds, setSelectedIds] = useState<string[]>([]);
  const [scannerMode, setScannerModeState] = useState<QueueView>('stock_in');
  const [scannerConfig, setScannerConfig] = useState<ScannerConfig | null>(null);
  const [scannerConnected, setScannerConnected] = useState<boolean | null>(null);
  const [processing, setProcessing] = useState<ProcessingNotice[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');
  const [failureById, setFailureById] = useState<Map<string, BatchFailureNotice>>(new Map());
  const [confirmedCount, setConfirmedCount] = useState(0);
  const [partialLine, setPartialLine] = useState<string | null>(null);
  const [scanError, setScanError] = useState('');
  const [cameraOpen, setCameraOpen] = useState(false);
  const [capturedBarcode, setCapturedBarcode] = useState('');
  const [sessionExpansion, setSessionExpansion] = useState<Record<string, boolean>>({});
  const captureHighlightTimer = useRef(0);
  // The id-set key changes when one scan commits and would remount the cards
  // that are still open. Each scan keeps the key it first joined.
  const [sessionTokens, setSessionTokens] = useState<Map<string, string>>(() => new Map());
  const scannerModeRef = useRef<QueueView>('stock_in');
  const entriesRef = useRef(entries);
  const inventoryReadyRef = useRef(false);
  const onHandRef = useRef<Map<string, number>>(new Map());
  const releasePinsRef = useRef<(productIds: ReadonlySet<string>) => void>(() => {});
  // Bumped when this page or an event stream sets the mode, so a config
  // response that started earlier cannot put the previous direction back.
  const modeEpoch = useRef(0);
  // A newer list wins. A live update that arrives while a list is in flight
  // is folded into that list, so the other rows are not thrown away.
  const inventoryEpoch = useRef(0);
  const inventoryLive = useRef<InventoryItem[]>([]);
  const [cuedNothing, setCuedNothing] = useState<ReadonlySet<string>>(() => new Set());

  // The selected tab is the scan direction. A refresh that reports the same
  // direction leaves the queue the user is reviewing in place.
  const acceptRemoteMode = useCallback((mode: QueueView) => {
    if (mode !== 'stock_in' && mode !== 'stock_out') return;
    const changed = scannerModeRef.current !== mode;
    scannerModeRef.current = mode;
    setScannerModeState(mode);
    if (changed) {
      setSelectedIds([]);
      setPartialLine(null);
    }
  }, []);

  const holdLocalMode = useCallback((mode: QueueView) => {
    modeEpoch.current += 1;
    scannerModeRef.current = mode;
    setScannerModeState(mode);
    setPartialLine(null);
  }, []);

  const stockInCount = useMemo(() => getEntriesForView(entries, 'stock_in').length, [entries]);
  const stockOutCount = useMemo(() => getEntriesForView(entries, 'stock_out').length, [entries]);

  const viewEntries = useMemo(() => getEntriesForView(entries, scannerMode), [entries, scannerMode]);
  const qrEntries = useMemo(() => viewEntries.filter((entry) => entry.status === 'qr'), [viewEntries]);
  const sessionEntries = useMemo(() => viewEntries.filter((entry) => entry.status !== 'qr'), [viewEntries]);
  const batches = useMemo(() => groupScansIntoBatches(sessionEntries), [sessionEntries]);
  const sessionKeys = batches.map((batch) => {
    const joined = batch.map((entry) => entry.id).sort().join('|');
    const known = batch
      .map((entry) => sessionTokens.get(entry.id))
      .find((token) => token !== undefined);
    return known ?? joined;
  });
  const missingTokens = batches.flatMap((batch, index) => {
    const key = sessionKeys[index] ?? '';
    return batch
      .filter((entry) => sessionTokens.get(entry.id) !== key)
      .map((entry) => [entry.id, key] as const);
  });
  if (missingTokens.length > 0) {
    setSessionTokens((current) => {
      const next = new Map(current);
      for (const [id, key] of missingTokens) next.set(id, key);
      return next;
    });
  }
  const processingForView = useMemo(
    () => processing.filter((notice) => notice.userId === userId && entryMatchesView(notice.direction, scannerMode)),
    [processing, userId, scannerMode],
  );
  const viewEntryIds = useMemo(() => new Set(viewEntries.map((entry) => entry.id)), [viewEntries]);

  // A batch outcome reloads the queue because cards just left. That refresh
  // must keep the line it recorded. Every other reload is a queue change.
  const loadQueue = useCallback(async (keepPartialLine = false) => {
    try {
      const [pending, flagged, qr] = await Promise.all([
        listScanEntries(userId, 'pending'),
        listScanEntries(userId, 'flagged'),
        listScanEntries(userId, 'qr'),
      ]);
      setEntries(sortScansNewestFirst([...pending, ...flagged, ...qr]));
      setSelectedIds((current) => current.filter((id) => pending.some((entry) => entry.id === id)));
      setFailureById((current) => {
        if (current.size === 0) return current;
        const pendingIds = new Set(pending.map((entry) => entry.id));
        let changed = false;
        const next = new Map<string, BatchFailureNotice>();
        for (const [id, notice] of current) {
          if (pendingIds.has(id)) next.set(id, notice);
          else changed = true;
        }
        return changed ? next : current;
      });
      if (!keepPartialLine) setPartialLine(null);
      setError('');
      const epoch = inventoryEpoch.current + 1;
      inventoryEpoch.current = epoch;
      inventoryLive.current = [];
      void getInventoryList(undefined, userId)
        .then((items) => {
          if (inventoryEpoch.current !== epoch) return;
          const merged = inventoryLive.current.reduce(
            (list, event) => mergeInventoryEvent(list, event),
            items,
          );
          setInventory(merged);
          setInventoryReady(true);
          setPinnedNone((current) => reconcilePins(current, merged, entriesRef.current));
        })
        .catch(() => {
          // Keep the last list. An empty stand-in would mark every stock-out as nothing on hand.
        });
    } catch (requestError) {
      setError(requestError instanceof Error ? requestError.message : 'Unable to load scan queue.');
    } finally {
      setLoading(false);
    }
  }, [userId]);

  useEffect(() => {
    void Promise.resolve().then(() => loadQueue());
  }, [loadQueue]);

  useEffect(() => () => {
    window.clearTimeout(captureHighlightTimer.current);
    setPartialLine(null);
  }, []);

  useEffect(() => {
    if (capturedBarcode === '') return;
    const node = document.querySelector('.scan-entry-card--just-captured, .processing-scan-card--just-captured, .qr-scan--just-captured');
    node?.scrollIntoView?.({ block: 'nearest' });
  }, [capturedBarcode, entries, processing]);

  // Load the reserved control-barcode strings so captureBarcode can classify a
  // scan as a mode switch instead of a product barcode, and seed the displayed
  // mode from the backend's current direction so a browser that connects after
  // a switch starts on the right mode. If the config cannot be loaded,
  // classification falls back to off (no crash) and scans post as usual.
  useEffect(() => {
    let cancelled = false;
    const epoch = modeEpoch.current;
    getScannerConfig()
      .then((config) => {
        if (cancelled || modeEpoch.current !== epoch) return;
        setScannerConfig(config);
        if (typeof config.connected === 'boolean') {
          setScannerConnected(config.connected);
        }
        if (config.currentMode === 'stock_in' || config.currentMode === 'stock_out') {
          acceptRemoteMode(config.currentMode);
        }
      })
      .catch(() => {
        if (!cancelled) setScannerConfig(null);
      });
    return () => {
      cancelled = true;
    };
  }, [acceptRemoteMode]);

  useEffect(() => {
    let cancelled = false;
    const refreshConnection = () => {
      const epoch = modeEpoch.current;
      getScannerConfig()
        .then((config) => {
          if (cancelled || modeEpoch.current !== epoch) return;
          if (typeof config.connected === 'boolean') {
            setScannerConnected(config.connected);
          }
          if (config.currentMode === 'stock_in' || config.currentMode === 'stock_out') {
            acceptRemoteMode(config.currentMode);
          }
        })
        .catch(() => undefined);
    };
    const timer = window.setInterval(refreshConnection, 2000);
    return () => {
      cancelled = true;
      window.clearInterval(timer);
    };
  }, [acceptRemoteMode]);

  useEffect(() => {
    const eventSource = new EventSource('/api/events');
    trackEventSource(eventSource);
    eventSource.addEventListener('scan', (message) => {
      const scanEntry = JSON.parse((message as MessageEvent).data) as ScanEntry;
      if (scanEntry.direction === 'stock_in' && scanEntry.productId !== null && scanEntry.status === 'committed') {
        releasePinsRef.current(new Set([scanEntry.productId]));
      }
      setPartialLine(null);
      setEntries((current) => {
        const next = sortScansNewestFirst(mergeScanEvent(current, scanEntry));
        setSelectedIds((selected) => pruneSelection(selected, next));
        return next;
      });
      setProcessing((current) => settleProcessingNotice(current, scanEntry));
    });
    eventSource.addEventListener('scan_processing', (message) => {
      const notice = JSON.parse((message as MessageEvent).data) as ProcessingNotice;
      setProcessing((current) => addProcessingNotice(current, notice));
    });
    eventSource.addEventListener('scan_processing_failed', (message) => {
      const failure = JSON.parse((message as MessageEvent).data) as ProcessingFailure;
      setProcessing((current) => removeProcessingNotice(current, failure.id));
      setScanError(failure.message);
    });
    eventSource.addEventListener('scanner_mode', (message) => {
      const mode = scannerModeFromEventData((message as MessageEvent).data);
      if (mode === null) return;
      modeEpoch.current += 1;
      acceptRemoteMode(mode);
    });
    eventSource.addEventListener('inventory', (message) => {
      const inventoryItem = JSON.parse((message as MessageEvent).data) as InventoryItem;
      inventoryLive.current = mergeInventoryEvent(inventoryLive.current, inventoryItem);
      setInventory((current) => mergeInventoryEvent(current, inventoryItem));
      setInventoryReady(true);
      if (inventoryItem.instanceCount > 0) {
        releasePinsRef.current(new Set([inventoryItem.item.productId]));
      }
    });
    return () => eventSource.close();
  }, [acceptRemoteMode]);

  const itemIdByProductId = useMemo(
    () => new Map(inventory.map((inventoryItem) => [inventoryItem.item.productId, inventoryItem.item.id])),
    [inventory],
  );
  const onHandByProductId = useMemo(
    () => new Map(inventory.map((inventoryItem) => [inventoryItem.item.productId, inventoryItem.instanceCount])),
    [inventory],
  );
  const nothingOnHandIds = useMemo(() => {
    const ids = new Set<string>();
    for (const entry of entries) {
      if (entry.status !== 'pending' || entry.direction !== 'stock_out' || entry.productId === null) continue;
      if (pinnedNone.has(entry.id)) {
        ids.add(entry.id);
        continue;
      }
      if (!inventoryReady) continue;
      if (shelfHasNothingOnHand(onHandByProductId.get(entry.productId))) ids.add(entry.id);
    }
    return ids;
  }, [entries, inventoryReady, onHandByProductId, pinnedNone]);

  const eligibleEntries = useMemo(
    () => viewEntries.filter((entry) => isBatchEligible(entry) && !nothingOnHandIds.has(entry.id)),
    [viewEntries, nothingOnHandIds],
  );
  const allEligibleSelected = eligibleEntries.length > 0
    && eligibleEntries.every((entry) => selectedIds.includes(entry.id));
  const selectedInView = viewEntries.filter((entry) => selectedIds.includes(entry.id) && !nothingOnHandIds.has(entry.id));
  const skippedBlocked = viewEntries.filter((entry) => nothingOnHandIds.has(entry.id) && !selectedIds.includes(entry.id)).length;
  const skipLine = selectedInView.length > 0 && skippedBlocked > 0
    ? confirmSkipsNothingOnHand(skippedBlocked)
    : null;
  const blockingFailures = [...failureById.values()].filter((notice) => notice.code !== CODE_NONE_ON_HAND);

  const rememberPin = useCallback((ids: string[]) => {
    if (ids.length === 0) return;
    setCuedNothing(new Set(ids));
    setPinnedNone((current) => {
      const next = new Map(current);
      const ready = inventoryReadyRef.current;
      for (const id of ids) {
        const entry = entriesRef.current.find((row) => row.id === id);
        const productId = entry?.productId ?? null;
        const counted = productId === null ? undefined : onHandRef.current.get(productId);
        next.set(id, { onHand: ready ? (counted ?? null) : null, observed: ready });
      }
      return next;
    });
    setSelectedIds((current) => current.filter((id) => !ids.includes(id)));
  }, []);

  const releasePins = useCallback((productIds: ReadonlySet<string>) => {
    if (productIds.size === 0) return;
    const stocked = (entry: ScanEntry | undefined) => (
      entry?.productId !== null && entry?.productId !== undefined && productIds.has(entry.productId)
    );
    setPinnedNone((current) => {
      let changed = false;
      const next = new Map(current);
      for (const [id] of current) {
        if (!stocked(entriesRef.current.find((row) => row.id === id))) continue;
        next.delete(id);
        changed = true;
      }
      return changed ? next : current;
    });
    // Stock arriving resolves the none_on_hand failure. Leaving it recorded kept an empty teal banner.
    setFailureById((current) => {
      let changed = false;
      const next = new Map(current);
      for (const [id, notice] of current) {
        if (notice.code !== CODE_NONE_ON_HAND) continue;
        if (!stocked(entriesRef.current.find((row) => row.id === id))) continue;
        next.delete(id);
        changed = true;
      }
      return changed ? next : current;
    });
  }, []);

  useEffect(() => {
    entriesRef.current = entries;
    inventoryReadyRef.current = inventoryReady;
    onHandRef.current = onHandByProductId;
    releasePinsRef.current = releasePins;
  }, [entries, inventoryReady, onHandByProductId, releasePins]);

  // Writes the same scannerMode the magic barcode and the headless listener
  // share. A repeat of the current direction is not a change.
  const applyScannerMode = useCallback(async (mode: QueueView) => {
    if (mode !== 'stock_in' && mode !== 'stock_out') return;
    if (scannerModeRef.current === mode) return;
    holdLocalMode(mode);
    try {
      await setScannerMode(mode);
    } catch (requestError) {
      setScanError(requestError instanceof Error ? requestError.message : 'Unable to switch scanner mode.');
    }
  }, [holdLocalMode]);

  // A notification click names the tab that holds the scans. The stored
  // direction covers a queue that was not mounted when the click happened.
  // A cold open from the service worker puts the same direction in the URL.
  useEffect(() => {
    const openDirection = (direction: unknown) => {
      if (direction !== 'stock_in' && direction !== 'stock_out') return;
      void applyScannerMode(direction);
    };
    const params = new URLSearchParams(window.location.search);
    const fromQuery = params.get('scanAlert');
    if (fromQuery === 'stock_in' || fromQuery === 'stock_out') {
      params.delete('scanAlert');
      const rest = params.toString();
      const path = window.location.pathname || '/';
      window.history.replaceState(null, '', rest === '' ? path : `${path}?${rest}`);
    }
    openDirection(fromQuery === 'stock_in' || fromQuery === 'stock_out' ? fromQuery : takeScanAlertDirection());
    const onOpen = (event: Event) => {
      takeScanAlertDirection();
      openDirection((event as CustomEvent<ScanAlertOpenDetail>).detail?.direction);
    };
    window.addEventListener(SCAN_ALERT_OPEN, onOpen);
    return () => window.removeEventListener(SCAN_ALERT_OPEN, onOpen);
  }, [applyScannerMode]);

  const handleViewChange = (value: string | null) => {
    if (value !== 'stock_in' && value !== 'stock_out') return;
    if (value === scannerModeRef.current) return;
    setSelectedIds([]);
    void applyScannerMode(value);
  };

  // Classify a scanned string against the configured control barcodes using the
  // same exact-match rule the backend classify() applies (no trimming or
  // case-folding beyond what BarcodeInputField already trims). Returns the mode
  // to switch to, or null when the string is an ordinary product barcode or the
  // config has not loaded yet.
  const classifyControlBarcode = (barcode: string): QueueView | null => {
    if (scannerConfig === null) return null;
    if (barcode === scannerConfig.stockInBarcode) return 'stock_in';
    if (barcode === scannerConfig.stockOutBarcode) return 'stock_out';
    return null;
  };

  const captureBarcode = async (barcode: string) => {
    setScanError('');
    setPartialLine(null);
    const targetMode = classifyControlBarcode(barcode);
    if (targetMode !== null) {
      // Show the switch immediately. The mode write and the scanner_mode event
      // confirm it; a config response that was already in flight is ignored.
      await applyScannerMode(targetMode);
      return;
    }
    try {
      // Stamp the entry with the selected mode so browser scans land in the
      // same view as the current direction, matching the headless listener
      // which stamps each entry with its Current_Mode. The ref is current even
      // when a camera frame arrives in the same turn as a mode tap.
      await createScanEntry({ barcode, direction: scannerModeRef.current, userId });
      await loadQueue();
    } catch (requestError) {
      setScanError(requestError instanceof Error ? requestError.message : 'Unable to add the scan.');
    }
  };

  const noteCameraCapture = (barcode: string) => {
    const targetMode = classifyControlBarcode(barcode);
    if (targetMode !== null) {
      notifications.show({
        message: targetMode === 'stock_in' ? 'Switched to stock in' : 'Switched to stock out',
        color: targetMode === 'stock_in' ? 'blue' : 'orange',
        autoClose: 2000,
        position: 'top-center',
      });
    } else {
      setCapturedBarcode(barcode);
      window.clearTimeout(captureHighlightTimer.current);
      captureHighlightTimer.current = window.setTimeout(() => setCapturedBarcode(''), 4000);
      notifications.show({
        message: `Captured ${barcode}`,
        color: 'teal',
        autoClose: 2000,
        position: 'top-center',
      });
    }
    void captureBarcode(barcode);
  };

  const rememberBatchOutcome = (response: BatchCommitResponse) => {
    const notices = batchFailureNotices(response);
    setFailureById(new Map(notices.map((notice) => [notice.id, notice])));
    setConfirmedCount(response.committedIds?.length ?? 0);
    const blockedIds = notices.filter((notice) => notice.code === CODE_NONE_ON_HAND).map((notice) => notice.id);
    rememberPin(blockedIds);
    setSelectedIds(notices.filter((notice) => notice.code !== CODE_NONE_ON_HAND).map((notice) => notice.id));
    const names: Record<string, string> = {};
    for (const entry of entries) {
      const name = entry.product?.name;
      if (name !== undefined && name !== '') names[entry.id] = name;
    }
    setPartialLine(formatPartialRemoval(response, names));
    void loadQueue(true);
  };

  // A busy or no_space banner is one sentence for the whole stop. Confirming
  // one card on its own makes that sentence out of date, so every storage
  // notice comes down together. Other failure codes stay on their cards.
  const beginSingleConfirm = (entryId: string) => {
    setPartialLine(null);
    setFailureById((current) => {
      let changed = false;
      const next = new Map(current);
      if (next.delete(entryId)) changed = true;
      for (const [id, notice] of current) {
        if (notice.code !== CODE_BUSY && notice.code !== CODE_NO_SPACE) continue;
        if (next.delete(id)) changed = true;
      }
      return changed ? next : current;
    });
  };

  const setEntrySelected = (entryId: string, selected: boolean) => {
    setSelectedIds((current) =>
      selected ? [...new Set([...current, entryId])] : current.filter((id) => id !== entryId),
    );
  };

  // Later refreshes keep the rows on screen. A loader is only for the first
  // paint, when there is nothing to shift under the user's finger.
  const showInitialLoader = loading && entries.length === 0;

  return (
    <Stack gap={6} className="scan-page">
      <div className="scan-page-tools scan-toolbar">
        <BarcodeInputField onScan={(barcode) => void captureBarcode(barcode)} />
        <div className="scan-toolbar-primary">
          <Title order={1} className="scan-toolbar-title">Scan queue</Title>
          <Text
            size="xs"
            className={`scan-direction-hint scan-direction-hint--${scannerMode}`}
            role="status"
          >
            {scannerMode === 'stock_in' ? 'Scans add to stock' : 'Scans remove from stock'}
          </Text>
          {/* "Mode: stock_…" stays in the DOM for the queue banner matcher. */}
          <div role="alert" className={`scan-mode-switch scan-mode-switch--${scannerMode}`}>
            <span className="scan-mode-chip-key" aria-hidden="true">Mode: {scannerMode}</span>
          </div>
          <CameraScanner
            onScan={noteCameraCapture}
            onOpenChange={setCameraOpen}
          />
        </div>
        {scanError !== '' && (
          <Alert color="red" py={4}>
            {scanError}
          </Alert>
        )}
        {scannerConnected !== null && !cameraOpen && (
          <Text
            size="xs"
            className={scannerConnected ? 'scan-status scan-status--on' : 'scan-status scan-status--off'}
          >
            {scannerConnected ? 'Scanner connected' : (
              <>
                <span>Scanner disconnected</span>
                {'. You can scan with this device\'s camera instead.'}
              </>
            )}
          </Text>
        )}
        <Tabs
          value={scannerMode}
          onChange={handleViewChange}
          color={scannerMode === 'stock_in' ? 'blue' : 'orange'}
          className={`scan-view-tabs scan-view-tabs--${scannerMode}`}
        >
          <Tabs.List>
            <Tabs.Tab
              value="stock_in"
              rightSection={stockInCount > 0
                ? <Badge size="sm" className="scan-tab-count" aria-hidden>{formatReviewCount(stockInCount)}</Badge>
                : undefined}
            >
              Stock in
            </Tabs.Tab>
            <Tabs.Tab
              value="stock_out"
              rightSection={stockOutCount > 0
                ? <Badge size="sm" className="scan-tab-count" aria-hidden>{formatReviewCount(stockOutCount)}</Badge>
                : undefined}
            >
              Stock out
            </Tabs.Tab>
          </Tabs.List>
        </Tabs>
        {sessionEntries.length > 0 && (
          <Group className="scan-batch-row" justify="flex-start" align="center" wrap="wrap" gap="xs">
            <Checkbox
              size="xs"
              label="Select all"
              aria-label="Select all eligible scans for batch approval"
              checked={allEligibleSelected}
              disabled={eligibleEntries.length === 0}
              onChange={() => setSelectedIds((current) => toggleSelectAll(
                viewEntries.filter((entry) => !nothingOnHandIds.has(entry.id)),
                current,
              ))}
            />
            <BatchReviewPanel
              selectedIds={selectedIds.filter((id) => viewEntryIds.has(id) && !nothingOnHandIds.has(id))}
              onStart={() => setPartialLine(null)}
              onComplete={rememberBatchOutcome}
            />
            {skipLine !== null && (
              <p className="scan-nothing-skip">
                <span className="scan-nothing-skip-mark" aria-hidden="true" />
                {skipLine}
              </p>
            )}
          </Group>
        )}
      </div>
      <Stack gap={8} className="scan-page-queue" aria-label="Scan queue entries">
        {blockingFailures.length > 0 && (
          <Alert color="red" py={4} className="scan-batch-banner" style={{ flexShrink: 0 }}>
            <div>{batchFailureBannerText(blockingFailures, confirmedCount)}</div>
            {partialLine !== null && <p className="scan-batch-partial" style={{ flexShrink: 0 }}>{partialLine}</p>}
          </Alert>
        )}
        {/* Amber cards carry their own message. The teal banner is only the partial line, and only when there is one. */}
        {blockingFailures.length === 0 && partialLine !== null && (
          <Text
            role="status"
            className="scan-batch-banner scan-batch-banner--success scan-batch-partial"
            style={{ flexShrink: 0 }}
          >
            {partialLine}
          </Text>
        )}
        {showInitialLoader && <Loader size="sm" aria-label="Loading scan queue" />}
        {error !== '' && (
          <Alert color="red" py={4}>
            {error}
          </Alert>
        )}
        {!showInitialLoader && error === '' && viewEntries.length === 0 && processingForView.length === 0 && (
          <Text c="dimmed" size="sm">No pending scans.</Text>
        )}
        {qrEntries.map((entry) => (
          <QrScanCard
            key={entry.id}
            entry={entry}
            justCaptured={capturedBarcode !== '' && entry.barcode === capturedBarcode}
            onRescan={focusBarcodeScanner}
            onChanged={() => void loadQueue()}
          />
        ))}
        {processingForView.map((notice) => (
          <ProcessingScanCard
            key={notice.id}
            notice={notice}
            justCaptured={capturedBarcode !== '' && notice.barcode === capturedBarcode}
          />
        ))}
        {batches.map((batch, index) => {
          const key = sessionKeys[index] ?? '';
          const expanded = sessionExpansion[key] ?? index === 0;
          return (
            <ScanSessionCard
              key={key}
              entries={batch}
              expanded={expanded}
              onToggleExpanded={() => {
                setSessionExpansion((current) => ({
                  ...current,
                  [key]: !(current[key] ?? index === 0),
                }));
              }}
              directionLabel={scannerMode === 'stock_in' ? 'stock in' : 'stock out'}
              selectedIds={selectedIds}
              onSelectionChange={setSelectedIds}
              blockedIds={nothingOnHandIds}
              flat
            >
              {batch.map((entry) => {
                const notice = failureById.get(entry.id);
                return (
                <ScanEntryCard
                  key={entry.id}
                  entry={entry}
                  itemId={entry.productId === null ? undefined : itemIdByProductId.get(entry.productId)}
                  justCaptured={capturedBarcode !== '' && entry.barcode === capturedBarcode}
                  commitNotice={batchCardCommitNotice(notice?.code ?? '', notice?.message ?? '')}
                  commitCode={notice?.code ?? ''}
                  nothingOnHand={nothingOnHandIds.has(entry.id)}
                  cueNothingOnHand={cuedNothing.has(entry.id)}
                  selected={selectedIds.includes(entry.id) && !nothingOnHandIds.has(entry.id)}
                  onSelectedChange={(selected) => setEntrySelected(entry.id, selected)}
                  onConfirmStart={() => beginSingleConfirm(entry.id)}
                  onBlocked={() => rememberPin([entry.id])}
                  onStocked={(productId) => releasePins(new Set([productId]))}
                  onChanged={() => void loadQueue()}
                  userId={userId}
                />
                );
              })}
            </ScanSessionCard>
          );
        })}
      </Stack>
    </Stack>
  );
};
