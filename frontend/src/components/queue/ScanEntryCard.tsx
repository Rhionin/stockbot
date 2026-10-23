import { useEffect, useId, useLayoutEffect, useMemo, useRef, useState } from 'react';
import { Alert, Avatar, Badge, Button, Card, Checkbox, Combobox, Drawer, Group, NumberInput, Stack, Text, TextInput, Title, useCombobox } from '@mantine/core';
import { addGroupMembers, ApiError, commitScanEntry, getInventoryList, noteGroupFromScan, updateProduct, updateScanEntry } from '../../api/client';
import { inlineFailure, CODE_NONE_ON_HAND, commitConflictBehavior, formatPartialRemoval, NO_CLOSE_MATCH, NOTHING_ON_HAND_BODY, NOTHING_ON_HAND_TITLE, ServerNoticeError, UnreachableError, type CommitScanResponse } from '../../api/contract';
import { showUndo } from '../restock/undo';
import { clearOfflineNotice } from './offlineNotice';
import type { InventoryItem, PantryLinkSuggestion, ScanEntry } from '../../types';
import { stockState } from '../../stockState';
import { FlaggedEntryResolver } from './FlaggedEntryResolver';
import { StockOutInstanceSelector } from './StockOutInstanceSelector';
import { ProvenanceBadge } from '../product/ProvenanceBadge';
import { expiryDateToISOString, formatExpiryDate, isValidUnitCount } from './queueUtils';

// A zero timestamp clears expires_at. JSON null leaves the stored date unchanged.
const CLEARED_EXPIRY_ISO = '0001-01-01T00:00:00.000Z';

// A burst of plus or minus taps updates the number immediately and saves the
// latest count once the taps pause.
const UNIT_COUNT_SAVE_DELAY_MS = 200;

function heldByBanner(error: unknown): boolean {
  return error instanceof UnreachableError || error instanceof ServerNoticeError;
}

const multipackName = /\d+\s*x\s*\d/i;

const LINK_OFFER_HEADING = 'Which item is this?';

const LinkProductSearch = ({
  busy,
  reloadKey,
  excludeProductId,
  userId,
  onPick,
}: {
  busy: boolean;
  reloadKey: number;
  excludeProductId: string;
  userId?: string;
  onPick: (productId: string) => void;
}) => {
  const searchId = useId();
  const rootRef = useRef<HTMLDivElement>(null);
  const combobox = useCombobox({
    onDropdownClose: () => combobox.resetSelectedOption(),
  });
  const [items, setItems] = useState<InventoryItem[]>([]);
  const [query, setQuery] = useState('');

  // The queue and the card both clip. Measure the open side of the queue and
  // pin the list there; floating-ui's first pass can lock it into the gap.
  useLayoutEffect(() => {
    const root = rootRef.current;
    if (!root) return;
    const rect = root.getBoundingClientRect();
    let clipTop = 0;
    let clipBottom = window.innerHeight;
    let node = root.parentElement;
    while (node) {
      const overflowY = getComputedStyle(node).overflowY;
      if (overflowY === 'auto' || overflowY === 'scroll') {
        const bounds = node.getBoundingClientRect();
        clipTop = bounds.top;
        clipBottom = bounds.bottom;
        break;
      }
      node = node.parentElement;
    }
    const below = clipBottom - rect.bottom;
    const above = rect.top - clipTop;
    const up = above > below;
    const room = Math.max(0, Math.floor((up ? above : below) - 8));
    root.dataset.openUp = up ? 'true' : 'false';
    root.style.setProperty('--scan-link-search-max-height', `${room}px`);
  });

  useEffect(() => {
    let active = true;
    void getInventoryList(undefined, userId)
      .then((loaded) => {
        if (active) setItems(loaded);
      })
      .catch(() => {
        if (active) setItems([]);
      });
    return () => {
      active = false;
    };
  }, [reloadKey, userId]);

  const matches = useMemo(() => {
    const normalized = query.trim().toLocaleLowerCase();
    return items.filter((row) => {
      if (excludeProductId !== '' && row.item.productId === excludeProductId) return false;
      if (normalized === '') return true;
      const product = row.item.product;
      return `${product.name} ${product.category}`.toLocaleLowerCase().includes(normalized);
    });
  }, [excludeProductId, items, query]);

  return (
    <div className="scan-link-search" ref={rootRef}>
      <Text component="label" htmlFor={searchId} size="xs" fw={500} className="scan-link-search-label">
        Search products
      </Text>
      <Combobox
        store={combobox}
        withinPortal={false}
        position="bottom"
        offset={4}
        middlewares={{ flip: false, shift: false }}
        onOptionSubmit={(productId) => {
          combobox.closeDropdown();
          onPick(productId);
        }}
      >
        <Combobox.Target>
          <TextInput
            id={searchId}
            size="xs"
            placeholder="Type a product name or category"
            value={query}
            autoComplete="off"
            disabled={busy}
            onChange={(event) => {
              setQuery(event.currentTarget.value);
              combobox.openDropdown();
              combobox.updateSelectedOptionIndex();
            }}
            onClick={() => combobox.openDropdown()}
            onFocus={() => combobox.openDropdown()}
            onBlur={() => combobox.closeDropdown()}
          />
        </Combobox.Target>
        <Combobox.Dropdown>
          <Combobox.Options>
            {matches.length === 0 && <Combobox.Empty>No matching products.</Combobox.Empty>}
            {matches.map((row) => (
              <Combobox.Option key={row.item.id} value={row.item.productId}>
                {row.item.product.name}
              </Combobox.Option>
            ))}
          </Combobox.Options>
        </Combobox.Dropdown>
      </Combobox>
    </div>
  );
};

const expiryDatePart = (expiresAt: string | null): string =>
  expiresAt === null ? '' : expiresAt.substring(0, 10);

export interface ScanEntryCardProps {
  entry: ScanEntry;
  selected: boolean;
  itemId?: string;
  justCaptured?: boolean;
  commitNotice?: string;
  commitCode?: string;
  nothingOnHand?: boolean;
  cueNothingOnHand?: boolean;
  onSelectedChange: (selected: boolean) => void;
  onChanged: () => void;
  onConfirmStart?: () => void;
  onBlocked?: () => void;
  onStocked?: (productId: string) => void;
  userId?: string;
}

export const ScanEntryCard = ({
  entry,
  selected,
  itemId,
  justCaptured = false,
  commitNotice = '',
  commitCode = '',
  nothingOnHand = false,
  cueNothingOnHand = false,
  onSelectedChange,
  onChanged,
  onConfirmStart,
  onBlocked,
  onStocked,
  userId,
}: ScanEntryCardProps) => {
  const blockedBodyId = useId();
  const articleRef = useRef<HTMLElement>(null);
  const [instanceId, setInstanceId] = useState('');
  const [localInstanceReload, setLocalInstanceReload] = useState(0);
  const [approving, setApproving] = useState(false);
  const [localOffer, setLocalOffer] = useState<{
    code: string;
    offer: PantryLinkSuggestion[] | null;
    hideEmpty: boolean;
  } | null>(null);
  const [removeError, setRemoveError] = useState('');
  const [removing, setRemoving] = useState(false);
  const [unitCountDraft, setUnitCountDraft] = useState(entry.unitCount);
  const [unitCountError, setUnitCountError] = useState('');
  const unitCountRef = useRef(entry.unitCount);
  const confirmedCountRef = useRef(entry.unitCount);
  const saveTimer = useRef(0);
  const saveGeneration = useRef(0);
  const [expiryDraft, setExpiryDraft] = useState(expiryDatePart(entry.expiresAt));
  const [expiryCommitted, setExpiryCommitted] = useState(expiryDatePart(entry.expiresAt));
  const [expiryEditorOpen, setExpiryEditorOpen] = useState(false);
  const [expiryError, setExpiryError] = useState('');
  const expiryInputRef = useRef<HTMLInputElement>(null);
  const [instancePickerOpen, setInstancePickerOpen] = useState(false);
  const [showChangeIndicator, setShowChangeIndicator] = useState(true);
  const [hintBusy, setHintBusy] = useState(false);
  const [hintError, setHintError] = useState('');
  const [whatOpen, setWhatOpen] = useState(false);
  const [packDraft, setPackDraft] = useState('');
  const [packError, setPackError] = useState('');
  const [packBusy, setPackBusy] = useState(false);
  const [householdReload, setHouseholdReload] = useState(0);
  const [excludedProductId, setExcludedProductId] = useState('');

  const prefersReducedMotion = () =>
    typeof window.matchMedia === 'function' && window.matchMedia('(prefers-reduced-motion: reduce)').matches;

  // A refresh can echo a count the user has already moved past. Applying that
  // echo snaps the stepper backward and shifts the control under their finger.
  useEffect(() => {
    if (unitCountRef.current !== confirmedCountRef.current && entry.unitCount !== unitCountRef.current) {
      return;
    }
    // Deliberate prop->draft sync: reset the controlled draft when the server value changes.
    setUnitCountDraft(entry.unitCount);
    unitCountRef.current = entry.unitCount;
    confirmedCountRef.current = entry.unitCount;
  }, [entry.unitCount]);

  useEffect(() => () => window.clearTimeout(saveTimer.current), []);

  useEffect(() => {
    // Deliberate prop->draft sync: reset the controlled draft when the server value changes.
    const next = expiryDatePart(entry.expiresAt);
    // eslint-disable-next-line react-hooks/set-state-in-effect
    setExpiryDraft(next);
    setExpiryCommitted(next);
  }, [entry.expiresAt]);

  useEffect(() => {
    if (!expiryEditorOpen) return;
    expiryInputRef.current?.focus();
  }, [expiryEditorOpen]);

  // Show change indicator on mount and when unitCount changes
  useEffect(() => {
    // Intentional transient animation trigger, cleared by the cleanup timer below.
    // eslint-disable-next-line react-hooks/set-state-in-effect
    setShowChangeIndicator(true);
    const timer = setTimeout(() => setShowChangeIndicator(false), 600);
    return () => clearTimeout(timer);
  }, [entry.unitCount]);

  const isStockIn = entry.direction === 'stock_in';
  const isStockOut = entry.direction === 'stock_out';
  // Pending confirmation is a list row. Flagged review stays a card.
  const flatRow = entry.status === 'pending';
  const blocked = nothingOnHand;
  useLayoutEffect(() => {
    if (!cueNothingOnHand) return;
    articleRef.current?.focus();
  }, [cueNothingOnHand]);
  const articleLabel = blocked
    ? `Scan ${entry.barcode}, ${NOTHING_ON_HAND_TITLE}`
    : `Scan ${entry.barcode}`;

  const changeIndicatorClass = [
    'scan-entry-card',
    flatRow ? 'scan-entry-row' : undefined,
    blocked ? 'scan-entry-card--nothing' : undefined,
    blocked || !showChangeIndicator
      ? undefined
      : prefersReducedMotion()
        ? 'scan-entry-card--changed-static'
        : 'scan-entry-card--changed-animated',
    justCaptured ? 'scan-entry-card--just-captured' : undefined,
  ].filter(Boolean).join(' ');
  const productName = entry.product?.name ?? 'Unknown product';
  const hint = entry.groupHint;
  const rememberedPack = entry.product?.packCount;
  const askPackCount = isStockIn
    && entry.status === 'pending'
    && entry.productId !== null
    && entry.product !== undefined
    && entry.product !== null
    && rememberedPack == null
    && multipackName.test(entry.product.name);
  const scannedAtLabel = new Date(entry.scannedAt).toLocaleString(undefined, {
    month: 'short',
    day: 'numeric',
    hour: 'numeric',
    minute: '2-digit',
  });

  const noteCommitted = (committed: ScanEntry & CommitScanResponse) => {
    const note = formatPartialRemoval(committed);
    if (note !== null) showUndo({ detail: note });
  };

  const showCommitSentence = (message: string) => {
    if (message === '') return;
    showUndo({ detail: message, color: 'red' });
  };

  const rememberOffer = (offer: PantryLinkSuggestion[] | null, hideEmpty: boolean) => {
    setLocalOffer({ code: commitCode, offer, hideEmpty });
  };

  // A batch code with no local answer yet opens search. A later confirm on
  // this card replaces that with the response, keyed to the same code.
  const batchBehavior = commitCode === '' ? null : commitConflictBehavior(commitCode);
  const matchingOffer = localOffer !== null && localOffer.code === commitCode ? localOffer : null;
  const batchOpensSearch = matchingOffer === null
    && batchBehavior !== null
    && (batchBehavior.refreshSuggestions || batchBehavior.hideEmptyMatch);
  const linkOffer = matchingOffer !== null ? matchingOffer.offer : (batchOpensSearch ? [] : null);
  const hideEmptyMatch = matchingOffer !== null ? matchingOffer.hideEmpty : batchOpensSearch;

  // The gone target is already in the suggestion list. Drop it locally and
  // reload the household list. Another commit could succeed and take stock out.
  const dropGoneTarget = (productId: string) => {
    const current = matchingOffer?.offer ?? [];
    rememberOffer(current.filter((suggestion) => suggestion.productId !== productId), false);
    setExcludedProductId(productId);
    setHouseholdReload((reload) => reload + 1);
    setInstancePickerOpen(false);
  };

  // Card state stays here (picker, link offer, search). The sentence itself
  // is published once, at the end, and only into the toast.
  const showCommitFailure = async (requestError: unknown, linkedProductId?: string): Promise<void> => {
    if (requestError instanceof ApiError && requestError.code === CODE_NONE_ON_HAND) {
      onBlocked?.();
      return;
    }
    let message = inlineFailure(requestError, 'Unable to confirm scan.') ?? '';
    if (requestError instanceof ApiError) {
      const behavior = commitConflictBehavior(requestError.code);
      if (behavior.refreshSuggestions) {
        if (linkedProductId !== undefined) dropGoneTarget(linkedProductId);
      } else if (behavior.linkOffer) {
        setInstancePickerOpen(false);
        rememberOffer(behavior.hideEmptyMatch ? [] : requestError.suggestions, behavior.hideEmptyMatch);
      } else {
        rememberOffer(null, false);
        if (behavior.refreshInstances) {
          setInstancePickerOpen(true);
          setInstanceId('');
          setLocalInstanceReload((current) => current + 1);
        }
      }
      message = behavior.showMessage ? (inlineFailure(requestError, 'Unable to confirm scan.') ?? '') : '';
    }
    if (heldByBanner(requestError)) return;
    showCommitSentence(message);
  };

  const handleApprove = async () => {
    setApproving(true);
    onConfirmStart?.();
    try {
      const committed = await commitScanEntry(entry.id, instanceId === '' ? undefined : instanceId);
      rememberOffer(null, false);
      clearOfflineNotice();
      setRemoveError('');
      noteCommitted(committed);
      if (isStockIn && entry.productId !== null) onStocked?.(entry.productId);
      onChanged();
    } catch (requestError) {
      await showCommitFailure(requestError);
    } finally {
      setApproving(false);
    }
  };

  const linkSuggestion = async (productId: string) => {
    setApproving(true);
    onConfirmStart?.();
    try {
      const committed = await commitScanEntry(entry.id, instanceId === '' ? undefined : instanceId, productId);
      rememberOffer(null, false);
      clearOfflineNotice();
      setRemoveError('');
      noteCommitted(committed);
      if (isStockIn) onStocked?.(productId);
      onChanged();
    } catch (requestError) {
      await showCommitFailure(requestError, productId);
    } finally {
      setApproving(false);
    }
  };

  const handleRemove = async () => {
    setRemoving(true);
    setRemoveError('');
    try {
      await updateScanEntry(entry.id, { status: 'cancelled' });
      clearOfflineNotice();
      onChanged();
    } catch (requestError) {
      if (heldByBanner(requestError)) return;
      setRemoveError(inlineFailure(requestError, 'Unable to remove scan.') ?? '');
    } finally {
      setRemoving(false);
    }
  };

  const clearSaveTimer = () => {
    window.clearTimeout(saveTimer.current);
    saveTimer.current = 0;
  };

  const persistUnitCount = async (draft: number) => {
    clearSaveTimer();
    if (!isValidUnitCount(draft)) {
      unitCountRef.current = confirmedCountRef.current;
      setUnitCountDraft(confirmedCountRef.current);
      return;
    }
    if (draft === confirmedCountRef.current) return;
    const generation = ++saveGeneration.current;
    try {
      await updateScanEntry(entry.id, { unitCount: draft });
      if (generation !== saveGeneration.current) return;
      // A newer tap landed while this save was in flight. Keep that number
      // and send it next. confirmedCountRef stays on the last count the queue
      // has actually shown, so an older refresh cannot snap the stepper back.
      if (unitCountRef.current !== draft) {
        void persistUnitCount(unitCountRef.current);
        return;
      }
      setUnitCountError('');
      clearOfflineNotice();
      onChanged();
    } catch (requestError) {
      if (generation !== saveGeneration.current || unitCountRef.current !== draft) return;
      if (heldByBanner(requestError)) {
        unitCountRef.current = confirmedCountRef.current;
        setUnitCountDraft(confirmedCountRef.current);
        return;
      }
      setUnitCountError('Unable to update unit count.');
      unitCountRef.current = confirmedCountRef.current;
      setUnitCountDraft(confirmedCountRef.current);
    }
  };

  const schedulePersist = () => {
    clearSaveTimer();
    saveTimer.current = window.setTimeout(() => {
      saveTimer.current = 0;
      void persistUnitCount(unitCountRef.current);
    }, UNIT_COUNT_SAVE_DELAY_MS);
  };

  const handleUnitCountBlur = () => {
    clearSaveTimer();
    void persistUnitCount(Number(unitCountRef.current));
  };

  const adjustUnitCount = (delta: number) => {
    const base = isValidUnitCount(unitCountRef.current) ? unitCountRef.current : confirmedCountRef.current;
    const next = base + delta;
    if (!isValidUnitCount(next)) return;
    unitCountRef.current = next;
    setUnitCountDraft(next);
    setUnitCountError('');
    schedulePersist();
  };

  const handleUnitCountKeyDown = (event: React.KeyboardEvent<HTMLInputElement>) => {
    if (event.key === 'Enter') {
      (event.target as HTMLInputElement).blur();
    }
  };

  const handleUnitCountChange = (value: number | string | null) => {
    const next = value === null || value === '' ? confirmedCountRef.current : Number(value);
    unitCountRef.current = next;
    setUnitCountDraft(next);
    if (value === null || value === '') return;
    setUnitCountError('');
    // Typing a count schedules the same save as +/−, including a value the
    // field accepts without blurring.
    if (isValidUnitCount(next)) schedulePersist();
  };

  const handleExpiryBlur = async () => {
    if (expiryDraft !== expiryCommitted) {
      try {
        const expiresAt = expiryDraft === '' ? CLEARED_EXPIRY_ISO : expiryDateToISOString(expiryDraft);
        await updateScanEntry(entry.id, { expiresAt });
        setExpiryCommitted(expiryDraft);
        setExpiryError('');
        clearOfflineNotice();
        onChanged();
      } catch (requestError) {
        if (heldByBanner(requestError)) {
          setExpiryDraft(expiryCommitted);
          return;
        }
        setExpiryError('Unable to update expiration date.');
        setExpiryDraft(expiryCommitted);
        return;
      }
    }
    if (entry.direction === 'stock_in') {
      setExpiryEditorOpen(false);
    }
  };

  const openExpiryEditor = () => {
    setExpiryError('');
    setExpiryEditorOpen(true);
  };

  const closeExpiryEditor = () => {
    setExpiryDraft(expiryCommitted);
    setExpiryError('');
    setExpiryEditorOpen(false);
  };

  const clearExpiry = async () => {
    setExpiryError('');
    try {
      await updateScanEntry(entry.id, { expiresAt: CLEARED_EXPIRY_ISO });
      setExpiryDraft('');
      setExpiryCommitted('');
      setExpiryEditorOpen(false);
      clearOfflineNotice();
      onChanged();
    } catch (requestError) {
      if (heldByBanner(requestError)) return;
      setExpiryError('Unable to update expiration date.');
    }
  };

  const addToGroup = async () => {
    if (hint?.groupId === undefined || entry.productId === null) return;
    setHintBusy(true);
    setHintError('');
    try {
      await addGroupMembers(hint.groupId, [entry.productId]);
      clearOfflineNotice();
      onChanged();
    } catch (requestError) {
      setHintError(inlineFailure(requestError, 'Unable to add this to the group.') ?? '');
    } finally {
      setHintBusy(false);
    }
  };

  const notNow = async () => {
    if (entry.productId === null) return;
    setHintBusy(true);
    setHintError('');
    try {
      await noteGroupFromScan(entry.productId);
      clearOfflineNotice();
      onChanged();
    } catch (requestError) {
      setHintError(inlineFailure(requestError, 'Unable to save that for later.') ?? '');
    } finally {
      setHintBusy(false);
    }
  };

  const rememberPack = async () => {
    if (entry.productId === null || entry.product === undefined || entry.product === null) return;
    const count = Number(packDraft);
    if (!Number.isInteger(count) || count < 1) {
      setPackError('Enter how many units are in the pack.');
      return;
    }
    setPackBusy(true);
    setPackError('');
    try {
      await updateProduct(entry.productId, {
        name: entry.product.name,
        category: entry.product.category,
        unitOfMeasure: entry.product.unitOfMeasure,
        packCount: count,
      });
      clearOfflineNotice();
      onChanged();
    } catch (requestError) {
      setPackError(inlineFailure(requestError, 'Unable to remember the pack.') ?? '');
    } finally {
      setPackBusy(false);
    }
  };

  const queueItem = (
    <>
      <div className="scan-entry-layout">
        <div className="scan-entry-check">
          {entry.status === 'pending' && (
            <Checkbox
              size="xs"
              aria-label="Select scan for batch approval"
              aria-describedby={blocked ? blockedBodyId : undefined}
              checked={blocked ? false : selected}
              disabled={blocked}
              onChange={(event) => onSelectedChange(event.currentTarget.checked)}
            />
          )}
        </div>
        <Avatar
          className="scan-entry-avatar"
          src={entry.product?.imageUrl}
          name={productName}
          radius="sm"
          size={32}
        />
        <div className="scan-entry-body">
          <div className="scan-entry-titleline">
            <Title order={3} size="sm" lineClamp={1} mb={0}>{productName}</Title>
            {entry.status === 'flagged' && <Badge size="xs" color="orange">Flagged</Badge>}
          </div>
          <div className="scan-entry-meta">
            <Text size="xs" c="dimmed" component="span" className="copyable-barcode">Barcode: {entry.barcode}</Text>
            <Text size="xs" c="dimmed" component="span">Scanned: {scannedAtLabel}</Text>
            <ProvenanceBadge externalSource={entry.product?.externalSource} />
          </div>
          {hint !== undefined && (
            <div className="scan-entry-hint">
              <Text size="xs">Looks like {hint.name}</Text>
              <Group gap={4} wrap="wrap">
                {hint.groupId !== undefined && entry.productId !== null && (
                  <Button size="compact-xs" loading={hintBusy} onClick={() => void addToGroup()}>
                    Add to group
                  </Button>
                )}
                {entry.productId !== null && (
                  <Button size="compact-xs" variant="default" loading={hintBusy} onClick={() => void notNow()}>
                    Not now
                  </Button>
                )}
              </Group>
            </div>
          )}
          {rememberedPack !== undefined && rememberedPack > 1 && (
            <Text size="xs" c="dimmed">Each barcode adds {rememberedPack} units</Text>
          )}
          {askPackCount && (
            <Group className="scan-entry-hint" gap={4} wrap="wrap" align="flex-end">
              <TextInput
                size="xs"
                label="How many units are in this pack?"
                value={packDraft}
                onChange={(event) => setPackDraft(event.currentTarget.value)}
                inputMode="numeric"
              />
              <Button size="compact-xs" loading={packBusy} onClick={() => void rememberPack()}>
                Remember
              </Button>
            </Group>
          )}
        </div>
      </div>

      {blocked && (
        <div className="scan-nothing">
          <span className="scan-nothing-mark" aria-hidden="true">!</span>
          <div className="scan-nothing-copy">
            <p className="scan-nothing-title" role={cueNothingOnHand ? 'status' : undefined}>{NOTHING_ON_HAND_TITLE}</p>
            <p className="scan-nothing-body" id={blockedBodyId}>{NOTHING_ON_HAND_BODY}</p>
          </div>
        </div>
      )}

      {entry.status === 'pending' && (
        <Group
          className={flatRow ? 'scan-entry-actions scan-entry-row-controls' : 'scan-entry-actions'}
          gap={flatRow ? 6 : 4}
          align="center"
          wrap="wrap"
          justify="flex-start"
        >
          <div className="scan-entry-stepper">
            <button
              type="button"
              className="scan-entry-stepper-btn"
              aria-label="Decrease unit count"
              disabled={blocked || !isValidUnitCount(Number(unitCountDraft)) || Number(unitCountDraft) <= 1}
              onClick={() => adjustUnitCount(-1)}
            >
              <span aria-hidden="true">−</span>
            </button>
            <NumberInput
              className="scan-entry-stepper-value"
              hideControls
              clampBehavior="none"
              min={1}
              step={1}
              allowDecimal={false}
              value={unitCountDraft}
              disabled={blocked}
              onChange={handleUnitCountChange}
              onBlur={handleUnitCountBlur}
              onKeyDown={handleUnitCountKeyDown}
              aria-label="Unit count"
            />
            <button
              type="button"
              className="scan-entry-stepper-btn"
              aria-label="Increase unit count"
              disabled={blocked}
              onClick={() => adjustUnitCount(1)}
            >
              <span aria-hidden="true">+</span>
            </button>
          </div>
          {entry.direction !== null && (
            blocked ? (
              // #294 replaces this Remove with "Add it and take it out".
              <div className="scan-entry-action-slot scan-entry-blocked-action">
                <Button
                  size="compact-xs"
                  variant="light"
                  color="red"
                  loading={removing}
                  onClick={() => void handleRemove()}
                >
                  Remove
                </Button>
              </div>
            ) : (
              <Group gap={4} wrap="nowrap">
                <Button
                  size="compact-xs"
                  loading={approving}
                  onClick={() => void handleApprove()}
                >
                  Confirm
                </Button>
                <Button
                  size="compact-xs"
                  variant="light"
                  color="red"
                  loading={removing}
                  onClick={() => void handleRemove()}
                >
                  Remove
                </Button>
              </Group>
            )
          )}
          {isStockIn && (
            <div className="scan-entry-expiry-slot">
              {expiryEditorOpen ? (
                <div className="scan-entry-expiry-editor">
                  <TextInput
                    ref={expiryInputRef}
                    className="scan-entry-expiry"
                    size="xs"
                    type="date"
                    placeholder="mm/dd/yyyy"
                    value={expiryDraft}
                    onChange={(event) => {
                      setExpiryDraft(event.currentTarget.value);
                      setExpiryError('');
                    }}
                    onBlur={handleExpiryBlur}
                    aria-label="Expiration date"
                  />
                  <button
                    type="button"
                    className="scan-entry-expiry-action"
                    aria-label="Cancel expiration"
                    onMouseDown={(event) => event.preventDefault()}
                    onClick={closeExpiryEditor}
                  >
                    Cancel
                  </button>
                </div>
              ) : expiryCommitted !== '' ? (
                <div className="scan-entry-expiry-summary">
                  <span>Expires {formatExpiryDate(`${expiryCommitted}T00:00:00.000Z`)}</span>
                  <button type="button" className="scan-entry-expiry-action" aria-label="Change expiration" onClick={openExpiryEditor}>
                    Change
                  </button>
                  <button type="button" className="scan-entry-expiry-action" aria-label="Clear expiration" onClick={() => void clearExpiry()}>
                    Clear
                  </button>
                </div>
              ) : (
                <button type="button" className="scan-entry-add-expiry" onClick={openExpiryEditor}>
                  Add expiration
                </button>
              )}
            </div>
          )}
          {isStockOut && linkOffer === null && !blocked && (
            <button
              type="button"
              className="scan-entry-more"
              aria-expanded={instancePickerOpen}
              onClick={() => setInstancePickerOpen((open) => !open)}
            >
              More
            </button>
          )}
        </Group>
      )}

      {(unitCountError !== '' || expiryError !== '') && (
        <Stack gap={4} mt={4}>
          {unitCountError !== '' && (
            <Alert color="red" py={4}>
              {unitCountError}
            </Alert>
          )}
          {expiryError !== '' && (
            <Alert color="red" py={4}>
              {expiryError}
            </Alert>
          )}
        </Stack>
      )}

      {instancePickerOpen && isStockOut && linkOffer === null && itemId !== undefined && (
        <div className="scan-entry-extra">
          <StockOutInstanceSelector
            itemId={itemId}
            value={instanceId}
            onChange={setInstanceId}
            reloadKey={localInstanceReload}
          />
        </div>
      )}
      {entry.status === 'flagged' && (
        <Group justify="flex-end" mt={4}>
          <Button size="compact-xs" onClick={() => setWhatOpen(true)}>
            What is it?
          </Button>
          <Button
            size="compact-xs"
            variant="light"
            color="red"
            loading={removing}
            onClick={() => void handleRemove()}
          >
            Remove
          </Button>
        </Group>
      )}

      <Drawer opened={whatOpen} onClose={() => setWhatOpen(false)} position="bottom" title="What is it?" size="auto">
        {whatOpen && (
          <FlaggedEntryResolver
            entry={entry}
            onResolved={() => {
              setWhatOpen(false);
              onChanged();
            }}
          />
        )}
      </Drawer>

      {linkOffer !== null && (
        <div className="scan-link-offer" role="region" aria-labelledby={`link-offer-${entry.id}`}>
          <p id={`link-offer-${entry.id}`} className="scan-link-offer-title">{LINK_OFFER_HEADING}</p>
          {linkOffer.length === 0 && !hideEmptyMatch ? (
            <p className="scan-link-offer-copy">{NO_CLOSE_MATCH}</p>
          ) : linkOffer.length === 0 ? null : (
            <ul className="scan-link-offer-list">
              {linkOffer.map((suggestion) => (
                <li key={suggestion.itemId} className="scan-link-offer-row">
                  <span className="scan-link-offer-name">{suggestion.name}</span>
                  <span className={`stock-note stock-note--${stockState(suggestion.onHand)}`}>
                    {suggestion.onHand} in stock
                  </span>
                  <Button
                    size="compact-xs"
                    loading={approving}
                    aria-label={`Link ${suggestion.name}`}
                    onClick={() => void linkSuggestion(suggestion.productId)}
                  >
                    Link
                  </Button>
                </li>
              ))}
            </ul>
          )}
          <LinkProductSearch
            busy={approving}
            reloadKey={householdReload}
            excludeProductId={excludedProductId}
            userId={userId}
            onPick={(productId) => void linkSuggestion(productId)}
          />
          <div className="scan-link-offer-actions">
            <Button size="compact-xs" variant="default" onClick={() => rememberOffer(null, false)}>
              Dismiss
            </Button>
          </div>
        </div>
      )}

      {commitNotice !== '' && commitCode !== CODE_NONE_ON_HAND && (
        <Alert color="red" py={4} mt={4}>{commitNotice}</Alert>
      )}
      {removeError !== '' && <Alert color="red" py={4} mt={4}>{removeError}</Alert>}
      {hintError !== '' && <Alert color="red" py={4} mt={4}>{hintError}</Alert>}
      {packError !== '' && <Alert color="red" py={4} mt={4}>{packError}</Alert>}
    </>
  );

  if (flatRow) {
    return (
      <article ref={articleRef} tabIndex={-1} className={changeIndicatorClass} aria-label={articleLabel}>
        {queueItem}
      </article>
    );
  }

  return (
    <Card component="article" withBorder padding="xs" radius="md" className={changeIndicatorClass} aria-label={articleLabel}>
      {queueItem}
    </Card>
  );
};
