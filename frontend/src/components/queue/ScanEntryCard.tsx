import { useEffect, useMemo, useRef, useState } from 'react';
import { Alert, Avatar, Badge, Button, Card, Checkbox, Combobox, Drawer, Group, NumberInput, Stack, Text, TextInput, Title, useCombobox } from '@mantine/core';
import { addGroupMembers, ApiError, commitScanEntry, listProducts, noteGroupFromScan, updateProduct, updateScanEntry } from '../../api/client';
import { commitConflictBehavior, formatPartialRemoval, NO_CLOSE_MATCH } from '../../api/contract';
import type { PantryLinkSuggestion, Product, ScanEntry } from '../../types';
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

const multipackName = /\d+\s*x\s*\d/i;

const LINK_OFFER_HEADING = 'Which item is this?';

const LinkProductSearch = ({
  busy,
  onPick,
}: {
  busy: boolean;
  onPick: (productId: string) => void;
}) => {
  const combobox = useCombobox({
    onDropdownClose: () => combobox.resetSelectedOption(),
  });
  const [products, setProducts] = useState<Product[]>([]);
  const [query, setQuery] = useState('');

  useEffect(() => {
    let active = true;
    void listProducts()
      .then((loaded) => {
        if (active) setProducts(loaded);
      })
      .catch(() => {
        if (active) setProducts([]);
      });
    return () => {
      active = false;
    };
  }, []);

  const matches = useMemo(() => {
    const normalized = query.trim().toLocaleLowerCase();
    if (normalized === '') return products;
    return products.filter((product) =>
      `${product.name} ${product.category}`.toLocaleLowerCase().includes(normalized),
    );
  }, [products, query]);

  return (
    <Combobox
      store={combobox}
      withinPortal={false}
      onOptionSubmit={(productId) => {
        combobox.closeDropdown();
        onPick(productId);
      }}
    >
      <Combobox.Target>
        <TextInput
          size="xs"
          label="Search products"
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
          {matches.map((product) => (
            <Combobox.Option key={product.id} value={product.id}>
              {product.name}
            </Combobox.Option>
          ))}
        </Combobox.Options>
      </Combobox.Dropdown>
    </Combobox>
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
  onSelectedChange: (selected: boolean) => void;
  onChanged: () => void;
  onCommitNote?: (note: string) => void;
}

export const ScanEntryCard = ({
  entry,
  selected,
  itemId,
  justCaptured = false,
  commitNotice = '',
  onSelectedChange,
  onChanged,
  onCommitNote,
}: ScanEntryCardProps) => {
  const [instanceId, setInstanceId] = useState('');
  const [localInstanceReload, setLocalInstanceReload] = useState(0);
  const [approveError, setApproveError] = useState('');
  const [approving, setApproving] = useState(false);
  const [linkOffer, setLinkOffer] = useState<PantryLinkSuggestion[] | null>(null);
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

  const changeIndicatorClass = [
    'scan-entry-card',
    flatRow ? 'scan-entry-row' : undefined,
    !showChangeIndicator
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

  const noteCommitted = (committed: ScanEntry) => {
    onCommitNote?.(formatPartialRemoval(entry.unitCount, committed) ?? '');
  };

  // Returns true when the failure was shown and the scan stays pending.
  const showCommitFailure = (requestError: unknown): boolean => {
    if (!(requestError instanceof ApiError)) return false;
    const behavior = commitConflictBehavior(requestError.code);
    if (behavior.linkOffer) {
      setLinkOffer(requestError.suggestions);
      return true;
    }
    if (behavior.refreshInstances) {
      setInstancePickerOpen(true);
      setInstanceId('');
      setLocalInstanceReload((current) => current + 1);
    }
    setApproveError(requestError.message || 'Unable to confirm scan.');
    return true;
  };

  const handleApprove = async () => {
    setApproving(true);
    setApproveError('');
    try {
      const committed = await commitScanEntry(entry.id, instanceId === '' ? undefined : instanceId);
      setLinkOffer(null);
      noteCommitted(committed);
      onChanged();
    } catch (requestError) {
      if (!showCommitFailure(requestError)) {
        setApproveError(requestError instanceof Error ? requestError.message : 'Unable to confirm scan.');
      }
    } finally {
      setApproving(false);
    }
  };

  const linkSuggestion = async (productId: string) => {
    setApproving(true);
    setApproveError('');
    try {
      const committed = await commitScanEntry(entry.id, instanceId === '' ? undefined : instanceId, productId);
      setLinkOffer(null);
      noteCommitted(committed);
      onChanged();
    } catch (requestError) {
      if (!showCommitFailure(requestError)) {
        setApproveError(requestError instanceof Error ? requestError.message : 'Unable to confirm scan.');
      }
    } finally {
      setApproving(false);
    }
  };

  const handleRemove = async () => {
    setRemoving(true);
    setRemoveError('');
    try {
      await updateScanEntry(entry.id, { status: 'cancelled' });
      onChanged();
    } catch (requestError) {
      setRemoveError(requestError instanceof Error ? requestError.message : 'Unable to remove scan.');
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
      onChanged();
    } catch {
      if (generation !== saveGeneration.current || unitCountRef.current !== draft) return;
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
        onChanged();
      } catch {
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
      onChanged();
    } catch {
      setExpiryError('Unable to update expiration date.');
    }
  };

  const addToGroup = async () => {
    if (hint?.groupId === undefined || entry.productId === null) return;
    setHintBusy(true);
    setHintError('');
    try {
      await addGroupMembers(hint.groupId, [entry.productId]);
      onChanged();
    } catch (requestError) {
      setHintError(requestError instanceof Error ? requestError.message : 'Unable to add this to the group.');
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
      onChanged();
    } catch (requestError) {
      setHintError(requestError instanceof Error ? requestError.message : 'Unable to save that for later.');
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
      onChanged();
    } catch (requestError) {
      setPackError(requestError instanceof Error ? requestError.message : 'Unable to remember the pack.');
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
              checked={selected}
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
              disabled={!isValidUnitCount(Number(unitCountDraft)) || Number(unitCountDraft) <= 1}
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
              onChange={handleUnitCountChange}
              onBlur={handleUnitCountBlur}
              onKeyDown={handleUnitCountKeyDown}
              aria-label="Unit count"
            />
            <button
              type="button"
              className="scan-entry-stepper-btn"
              aria-label="Increase unit count"
              onClick={() => adjustUnitCount(1)}
            >
              <span aria-hidden="true">+</span>
            </button>
          </div>
          {entry.direction !== null && (
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
          {isStockOut && (
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

      {instancePickerOpen && isStockOut && itemId !== undefined && (
        <div className="scan-entry-extra">
          <StockOutInstanceSelector
            itemId={itemId}
            value={instanceId}
            onChange={setInstanceId}
            reloadKey={localInstanceReload}
          />
        </div>
      )}
      {instancePickerOpen && isStockOut && itemId === undefined && (
        <Alert color="yellow" py={4} mt={4}>No inventory item is available for this product.</Alert>
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
          {linkOffer.length === 0 ? (
            <p className="scan-link-offer-copy">{NO_CLOSE_MATCH}</p>
          ) : (
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
          <LinkProductSearch busy={approving} onPick={(productId) => void linkSuggestion(productId)} />
          <div className="scan-link-offer-actions">
            <Button size="compact-xs" variant="default" onClick={() => setLinkOffer(null)}>
              Dismiss
            </Button>
          </div>
        </div>
      )}

      {(approveError || commitNotice) !== '' && (
        <Alert color="red" py={4} mt={4}>{approveError || commitNotice}</Alert>
      )}
      {removeError !== '' && <Alert color="red" py={4} mt={4}>{removeError}</Alert>}
      {hintError !== '' && <Alert color="red" py={4} mt={4}>{hintError}</Alert>}
      {packError !== '' && <Alert color="red" py={4} mt={4}>{packError}</Alert>}
    </>
  );

  if (flatRow) {
    return (
      <article className={changeIndicatorClass} aria-label={`Scan ${entry.barcode}`}>
        {queueItem}
      </article>
    );
  }

  return (
    <Card component="article" withBorder padding="xs" radius="md" className={changeIndicatorClass} aria-label={`Scan ${entry.barcode}`}>
      {queueItem}
    </Card>
  );
};
