import { useCallback, useEffect, useMemo, useState } from 'react';
import { HOUSEHOLD_SWITCH_EVENT } from '../household/liveHints';
import { Link, useSearchParams } from 'react-router-dom';
import {
  ActionIcon,
  Alert,
  Anchor,
  Badge,
  Button,
  Collapse,
  Group,
  Loader,
  Menu,
  NativeSelect,
  NumberInput,
  Stack,
  Table,
  Text,
  Title,
} from '@mantine/core';
import {
  addShoppingListItem,
  clearItemDeal,
  fillShoppingCart,
  clearItemTarget,
  dontRestockItem,
  getInventoryList,
  getRestock,
  getShoppingList,
  getShoppingListStatus,
  previewCategoryTarget,
  setCategoryTarget,
  setItemTarget,
  listProviders,
  listStores,
  stage1AuthActive,
  markShoppingListItemPurchased,
  removeShoppingListItem,
  swapShoppingLine,
  saveItemDeal,
  setShoppingListAdjustment,
  setGroupTarget,
  listGroups,
  currentAuthSession,
} from '../../api/client';
import type { ProviderListEntry } from '@stockbot/shared/providerList';
import { krogerDisabledOnProviderList, providerListEntries } from '@stockbot/shared/providerList';
import type { InGroupErrorMembers, InventoryItem, ProductGroup, ShoppingListEntry, ShoppingListEntryW3, ShoppingListStatusResponse } from '@stockbot/shared/types';
import { restockItemIsGrouped } from '../groups/groupedProductGoal';
import { groupGoalCopy } from '../groups/groupGoalCopy';
import { ShoppingListEditedTag } from './ShoppingListEditedTag';
import { InGroupInlineAlert } from '../groups/InGroupInlineAlert';
import { ApiError } from '../../api/client';
import type { RestockSnapshot } from '../../restock/types';
import { restockCopy } from '../../restock/copy';
import { StopDialog } from '../restock/StopDialog';
import { TargetEditor } from '../restock/TargetEditor';
import { showUndo } from '../restock/undo';
import type { RestockPreview, RestockTarget } from '../../restock/types';
import { ruleLabel } from '../groups/copy';
import { TripBuyPicker, SWAP_ERROR } from './TripBuyPicker';
import { ChangeAmountWithSkipConfirm } from './ChangeAmountWithSkipConfirm';
import { ZeroForTripStatusBlock } from './ZeroForTripStatusBlock';
import { ZERO_FOR_TRIP_DONT_RESTOCK_ERROR, ZERO_FOR_TRIP_DONT_RESTOCK_UNDO_DETAIL, zeroForTripDisplayName } from '@stockbot/shared/zeroForTripCopy';
import type { RestockNudgeTarget } from '@stockbot/shared/zeroForTripNudge';
import { zeroForTripActorRole } from './zeroForTripActorRole';
import { shoppingListHasRestockRow } from '@stockbot/shared/shoppingRestockGate';
import { useCredentialsRevision } from '../../credentialsRefresh';
import { stockState } from '../../stockState';
import { ProviderPanel } from './ProviderPanel';
import { ProvisionButton } from './ProvisionButton';
import { inlineFailure } from '@stockbot/shared/contract';
import {
  krogerCallbackIsError,
  krogerCallbackMessage,
  linkedCartStoreName,
  shoppingKrogerLegacyMessage,
  shoppingRedirectMismatchMessage,
  storeNameCtx,
} from '../household/storeApp/krogerAppMessages';

const requestErrorMessage = (error: unknown, fallback: string) =>
  inlineFailure(error, fallback) ?? '';

export const ShoppingListPage = () => {
  const [searchParams, setSearchParams] = useSearchParams();
  const [entries, setEntries] = useState<ShoppingListEntry[]>([]);
  const [providers, setProviders] = useState<ProviderListEntry[]>([]);
  const [inventory, setInventory] = useState<InventoryItem[]>([]);
  const [restock, setRestock] = useState<RestockSnapshot | null>(null);
  const [groups, setGroups] = useState<ProductGroup[]>([]);
  const [editorId, setEditorId] = useState('');
  const [targetInGroup, setTargetInGroup] = useState<InGroupErrorMembers | null>(null);
  const [preview, setPreview] = useState<RestockPreview | null>(null);
  const [pendingCategory, setPendingCategory] = useState('');
  const [selectedItemId, setSelectedItemId] = useState('');
  const [quantity, setQuantity] = useState<number | string>(1);
  const [saleItemId, setSaleItemId] = useState('');
  const [salePrice, setSalePrice] = useState<number | string>('');
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [filling, setFilling] = useState(false);
  const [decisionsOpen, setDecisionsOpen] = useState(false);
  const [error, setError] = useState('');
  const [swapBusyByLine, setSwapBusyByLine] = useState<Record<string, boolean>>({});
  const [swapErrorByLine, setSwapErrorByLine] = useState<Record<string, string>>({});
  const [krogerNotice, setKrogerNotice] = useState('');
  const [krogerNoticeIsError, setKrogerNoticeIsError] = useState(false);
  const [listStatus, setListStatus] = useState<ShoppingListStatusResponse | null>(null);
  const [notOnListOpen, setNotOnListOpen] = useState(false);
  const [inlineSkipConfirm, setInlineSkipConfirm] = useState<Record<string, boolean>>({});
  const [nudgeLoadingKey, setNudgeLoadingKey] = useState<string | null>(null);
  const [nudgeErrorByRow, setNudgeErrorByRow] = useState<Record<string, string>>({});
  const [offline, setOffline] = useState(
    typeof navigator !== 'undefined' ? !navigator.onLine : false,
  );
  const [storeCanConnect, setStoreCanConnect] = useState(true);
  const [linkedStoreName, setLinkedStoreName] = useState<string | null>(null);
  const connectNoticeCtx = useMemo(() => storeNameCtx(linkedStoreName), [linkedStoreName]);

  useEffect(() => {
    const legacy = searchParams.get('connected');
    if (legacy === 'kroger') {
      // eslint-disable-next-line react-hooks/set-state-in-effect -- one-shot OAuth return handling
      setKrogerNotice(shoppingKrogerLegacyMessage());
      const next = new URLSearchParams(searchParams);
      next.delete('connected');
      setSearchParams(next, { replace: true });
      return;
    }
    const outcome = searchParams.get('kroger');
    if (outcome === null) return;
    const message = krogerCallbackMessage(outcome, connectNoticeCtx, true);
    if (outcome === 'redirect_mismatch') {
      setKrogerNotice(shoppingRedirectMismatchMessage());
      setKrogerNoticeIsError(true);
    } else if (message !== '') {
      setKrogerNotice(message);
      setKrogerNoticeIsError(krogerCallbackIsError(outcome));
    }
    const next = new URLSearchParams(searchParams);
    next.delete('kroger');
    setSearchParams(next, { replace: true });
    // OAuth return params are read once on landing; clearing them should not re-run this logic.
    // eslint-disable-next-line react-hooks/exhaustive-deps -- intentionally omits searchParams after first pass
  }, [connectNoticeCtx]);

  const refresh = useCallback(async () => {
    const providerList = await listProviders();
    const providerRows = providerListEntries(providerList);
    const target = providerRows.find((row) => row.credentialsConfigured) ?? providerRows[0];
    const statusPromise = target?.id
      ? getShoppingListStatus(target.id).catch(() => null)
      : Promise.resolve(null);
    const [shoppingEntries, inventoryItems, snapshot, status, storesResult] = await Promise.all([
      getShoppingList(target?.id),
      getInventoryList(),
      getRestock().catch(() => null),
      statusPromise,
      listStores()
        .then((stores) => ({ ok: true as const, stores }))
        .catch(() => ({ ok: false as const })),
    ]);
    // A failed stores read is not a member. Keep the last canConnect (the pre-load default is true).
    if (storesResult.ok) {
      setStoreCanConnect(storesResult.stores.canConnect);
      setLinkedStoreName(linkedCartStoreName(storesResult.stores.stores));
    }
    setProviders(providerList);
    setEntries(shoppingEntries);
    setInventory(inventoryItems);
    setRestock(snapshot);
    setListStatus(status !== null && typeof status === 'object' && !Array.isArray(status) ? status : null);
    void listGroups()
      .then((data) => setGroups(Array.isArray(data) ? data : []))
      .catch(() => setGroups([]));
  }, []);

  useEffect(() => {
    const onOnline = () => setOffline(false);
    const onOffline = () => setOffline(true);
    window.addEventListener('online', onOnline);
    window.addEventListener('offline', onOffline);
    return () => {
      window.removeEventListener('online', onOnline);
      window.removeEventListener('offline', onOffline);
    };
  }, []);

  const loadShoppingList = useCallback(async () => {
    setLoading(true);
    setError('');
    try {
      await refresh();
    } catch (requestError) {
      setError(requestErrorMessage(requestError, 'Unable to load the shopping list.'));
    } finally {
      setLoading(false);
    }
  }, [refresh]);

  useEffect(() => {
    void Promise.resolve().then(loadShoppingList);
  }, [loadShoppingList]);

  useEffect(() => {
    if (!stage1AuthActive() || typeof EventSource !== 'function') return undefined;
    let source: EventSource | null = null;
    let poll = 0;
    let stopped = false;
    const connect = () => {
      if (stopped) return;
      if (poll !== 0) window.clearInterval(poll);
      poll = 0;
      source?.close();
      try {
        source = new EventSource('/api/events');
      } catch {
        source = null;
        return;
      }
      source.addEventListener('hint', (event) => {
        let hint: { type?: string };
        try {
          hint = JSON.parse((event as MessageEvent).data) as { type?: string };
        } catch {
          return;
        }
        if (hint.type !== 'list' && hint.type !== 'rekey') return;
        if (document.visibilityState === 'hidden') return;
        void loadShoppingList();
      });
      source.onerror = () => {
        source?.close();
        source = null;
        if (poll !== 0 || document.visibilityState === 'hidden') return;
        poll = window.setInterval(() => {
          if (document.visibilityState === 'hidden') return;
          void loadShoppingList();
        }, 10_000);
      };
    };
    const onSwitch = () => {
      void loadShoppingList();
      if (stage1AuthActive()) connect();
    };
    connect();
    window.addEventListener(HOUSEHOLD_SWITCH_EVENT, onSwitch);
    return () => {
      stopped = true;
      window.removeEventListener(HOUSEHOLD_SWITCH_EVENT, onSwitch);
      if (poll !== 0) window.clearInterval(poll);
      source?.close();
    };
  }, [loadShoppingList]);

  const credentialsRevision = useCredentialsRevision();
  useEffect(() => {
    if (credentialsRevision === 0) return;
    void Promise.resolve().then(loadShoppingList);
  }, [credentialsRevision, loadShoppingList]);

  const activeProviders = providerListEntries(providers);
  const targetProvider = activeProviders.find((row) => row.credentialsConfigured) ?? activeProviders[0] ?? null;
  const hideOnlineStoreControls = krogerDisabledOnProviderList(providers);

  const changeAdjustment = async (entry: ShoppingListEntry, value: number | string) => {
    if (entry.id === '' || targetProvider === null) return;
    const nextQuantity = typeof value === 'number' ? value : Number(value);
    if (!Number.isInteger(nextQuantity) || nextQuantity < 0 || nextQuantity > 999) return;
    setError('');
    try {
      await setShoppingListAdjustment(entry.id, entry.provider || targetProvider.id, nextQuantity);
      if (nextQuantity === 0) {
        setInlineSkipConfirm((prev) => ({ ...prev, [entry.id]: true }));
      } else {
        setInlineSkipConfirm((prev) => {
          const next = { ...prev };
          delete next[entry.id];
          return next;
        });
      }
      await refresh();
    } catch (requestError) {
      setError(requestErrorMessage(requestError, 'Unable to save the quantity adjustment.'));
    }
  };

  const zeroAdjustmentEntryIds = useMemo(
    () => new Set(entries.filter((row) => row.id !== '' && row.adjustment === 0).map((row) => row.id)),
    [entries],
  );
  const zeroForTripOffList = useMemo(
    () => listStatus?.offList?.filter((row) => {
      if (row.offListReason !== 'zero_for_trip') return false;
      const entryId = row.zeroForTrip?.entryId;
      return entryId == null || entryId === '' || !zeroAdjustmentEntryIds.has(entryId);
    }) ?? [],
    [listStatus, zeroAdjustmentEntryIds],
  );

  const inlineSkipVisible = useCallback(
    (entry: ShoppingListEntry) => entry.adjustment === 0 || Boolean(inlineSkipConfirm[entry.id]),
    [inlineSkipConfirm],
  );

  const runDontRestockNudge = useCallback(async (line: RestockNudgeTarget, rowKey: string) => {
    if (offline || nudgeLoadingKey !== null) return;
    setNudgeLoadingKey(rowKey);
    setNudgeErrorByRow((prev) => {
      const next = { ...prev };
      delete next[rowKey];
      return next;
    });
    const productName = zeroForTripDisplayName(line.productName);
    const groupName = line.groupName?.trim();
    try {
      if (line.groupId != null && line.groupId !== '' && groupName) {
        const previousMonths = groups.find((group) => group.id === line.groupId)?.windowMonths;
        await setGroupTarget(line.groupId, { windowMonths: 0 });
        showUndo(groupName, ZERO_FOR_TRIP_DONT_RESTOCK_UNDO_DETAIL, () => {
          void setGroupTarget(
            line.groupId!,
            previousMonths !== undefined ? { windowMonths: previousMonths } : { clear: true },
          ).then(() => refresh());
        });
      } else if (line.itemId != null && line.itemId !== '') {
        const result = await dontRestockItem(line.itemId);
        showUndo(result.title ?? productName, result.detail, () => {
          void (result.previous ? setItemTarget(line.itemId!, result.previous) : clearItemTarget(line.itemId!)).then(() => refresh());
        });
      }
      await refresh();
    } catch {
      setNudgeErrorByRow((prev) => ({ ...prev, [rowKey]: ZERO_FOR_TRIP_DONT_RESTOCK_ERROR }));
    } finally {
      setNudgeLoadingKey(null);
    }
  }, [offline, nudgeLoadingKey, refresh, groups]);

  const groupTargets = useMemo(
    () => groups.map((group) => ({ id: group.id, windowMonths: group.windowMonths, quantity: group.quantity })),
    [groups],
  );

  const inventoryByItemId = useMemo(
    () => new Map(inventory.map((inventoryItem) => [inventoryItem.item.id, inventoryItem])),
    [inventory],
  );
  const saleBrands = useMemo(
    () => inventory.map((inventoryItem) => ({
      value: inventoryItem.item.id,
      label: inventoryItem.item.product.name,
    })),
    [inventory],
  );
  const showDecisions = decisionsOpen || entries.length > 0 || zeroForTripOffList.length > 0;
  const listAction = entries.length > 0 ? 'Update the list' : 'Build the list';
  const manualQuantity = typeof quantity === 'number' ? quantity : Number(quantity);
  const manualQuantityIsValid = quantity !== '' && Number.isInteger(manualQuantity) && manualQuantity >= 1;
  const salePriceNumber = typeof salePrice === 'number' ? salePrice : Number(salePrice);
  const salePriceIsValid = salePrice !== '' && Number.isInteger(salePriceNumber) && salePriceNumber >= 0;

  const fillCart = async () => {
    setFilling(true);
    setError('');
    try {
      await fillShoppingCart();
      setDecisionsOpen(true);
      await refresh();
    } catch (requestError) {
      setError(requestErrorMessage(requestError, 'Unable to build the list.'));
    } finally {
      setFilling(false);
    }
  };

  const addManualItem = async () => {
    if (selectedItemId === '' || !manualQuantityIsValid) return;
    setSaving(true);
    setError('');
    try {
      await addShoppingListItem(selectedItemId, manualQuantity);
      setSelectedItemId('');
      setQuantity(1);
      await refresh();
    } catch (requestError) {
      setError(requestErrorMessage(requestError, 'Unable to add the shopping list item.'));
    } finally {
      setSaving(false);
    }
  };

  const swapLine = async (entry: ShoppingListEntry, itemId: string) => {
    if (entry.id === '' || itemId === entry.itemId) return;
    const lineId = entry.id;
    setSwapBusyByLine((current) => ({ ...current, [lineId]: true }));
    try {
      const updated = await swapShoppingLine(lineId, itemId);
      setEntries((current) =>
        current.map((row) => (row.id === lineId ? { ...row, ...updated } : row)),
      );
      setSwapErrorByLine((current) => {
        const next = { ...current };
        delete next[lineId];
        return next;
      });
      try {
        await refresh();
      } catch {
        // The swap succeeded; a refresh failure must not read as a save failure.
      }
    } catch {
      setSwapErrorByLine((current) => ({ ...current, [lineId]: SWAP_ERROR }));
    } finally {
      setSwapBusyByLine((current) => ({ ...current, [lineId]: false }));
    }
  };

  const updateEntry = async (entry: ShoppingListEntry, action: 'purchase' | 'remove') => {
    if (entry.id === '') return;
    setError('');
    try {
      if (action === 'purchase') await markShoppingListItemPurchased(entry.id);
      else await removeShoppingListItem(entry.id);
      await refresh();
    } catch (requestError) {
      setError(requestErrorMessage(requestError, `Unable to ${action} the shopping list item.`));
    }
  };

  const noteSale = async () => {
    if (saleItemId === '' || !salePriceIsValid) return;
    setError('');
    try {
      await saveItemDeal(saleItemId, salePriceNumber, 'Noted sale');
      setSalePrice('');
      await refresh();
    } catch (requestError) {
      setError(requestErrorMessage(requestError, 'Unable to note the sale.'));
    }
  };

  const clearSale = async () => {
    if (saleItemId === '') return;
    setError('');
    try {
      await clearItemDeal(saleItemId);
      await refresh();
    } catch (requestError) {
      setError(requestErrorMessage(requestError, 'Unable to clear the sale.'));
    }
  };

  return (
    <Stack gap="sm" className="page-wide">
      <div className="shopping-toolbar">
        <Title order={1} size="h3" className="shopping-toolbar-title">Shopping plan</Title>
        <div className="shopping-toolbar-actions">
          <Button
            className="shopping-fill"
            loading={filling}
            disabled={loading}
            onClick={() => void fillCart()}
          >
            {listAction}
          </Button>
          {!hideOnlineStoreControls && (
            <ProvisionButton
              provider={targetProvider}
              entries={entries}
              explainEmpty={!loading}
              onFinished={() => void loadShoppingList()}
              canConnect={storeCanConnect}
              connectNoticeCtx={connectNoticeCtx}
            />
          )}
        </div>
      </div>
      <ProviderPanel
        providers={providers}
        onChanged={() => void loadShoppingList()}
        canConnect={storeCanConnect}
        connectNoticeCtx={connectNoticeCtx}
      />
      {!loading && showDecisions && (
        <Alert className="page-cluster" variant="light" color="teal" title="Note a sale">
          <Group align="end" gap="xs" wrap="wrap">
            <NativeSelect
              size="xs"
              label="Brand on sale"
              value={saleItemId}
              onChange={(event) => setSaleItemId(event.currentTarget.value)}
              data={[{ value: '', label: 'Choose a brand' }, ...saleBrands]}
            />
            <NumberInput
              size="xs"
              label="Sale price in cents"
              min={0}
              step={1}
              allowDecimal={false}
              value={salePrice}
              onChange={setSalePrice}
              w={160}
            />
            <Button size="xs" variant="light" disabled={saleItemId === '' || !salePriceIsValid} onClick={() => void noteSale()}>
              Note sale
            </Button>
            <Button
              size="xs"
              variant="default"
              disabled={saleItemId === ''}
              onClick={() => void clearSale()}
            >
              Clear sale
            </Button>
          </Group>
        </Alert>
      )}
      {showDecisions && <Stack className="page-cluster" component="form" gap="xs" onSubmit={(event) => {
        event.preventDefault();
        void addManualItem();
      }}>
        <Title order={2} size="h5">Add an item</Title>
        <Group align="end" gap="xs" wrap="wrap">
          <NativeSelect
            size="xs"
            label="Pantry item"
            value={selectedItemId}
            onChange={(event) => setSelectedItemId(event.currentTarget.value)}
            data={[
              { value: '', label: 'Choose an item' },
              ...inventory.map((inventoryItem) => ({
                value: inventoryItem.item.id,
                label: inventoryItem.item.product.name,
              })),
            ]}
          />
          <NumberInput
            size="xs"
            label="Quantity"
            min={1}
            step={1}
            allowDecimal={false}
            value={quantity}
            onChange={setQuantity}
            w={100}
          />
          <Button
            size="xs"
            type="submit"
            loading={saving}
            disabled={selectedItemId === '' || !manualQuantityIsValid}
          >
            Add to shopping list
          </Button>
        </Group>
      </Stack>}
      {loading && <Loader aria-label="Loading shopping plan" />}
      {krogerNotice !== '' && (
        <Alert
          color={krogerNoticeIsError ? 'red' : 'teal'}
          variant="light"
          role={krogerNoticeIsError ? 'alert' : 'status'}
        >
          {krogerNotice}
        </Alert>
      )}
      {error !== '' && (
        <Alert color="red" py="xs">
          {error}
        </Alert>
      )}
      {!loading && showDecisions && (entries.length > 0 || zeroForTripOffList.length > 0) && (
        <div className="shopping-entries-scroll">
          {entries.length > 0 && (
          <Table className="shopping-entries" aria-label="Shopping plan entries">
            <Table.Thead>
              <Table.Tr>
                <Table.Th>Item</Table.Th>
                <Table.Th>Quantity</Table.Th>
                <Table.Th>Source</Table.Th>
                <Table.Th>Actions</Table.Th>
              </Table.Tr>
            </Table.Thead>
            <Table.Tbody>
              {entries.map((entry) => {
                const inventoryItem = inventoryByItemId.get(entry.itemId);
                const productName = inventoryItem?.item.product.name ?? `Item ${entry.itemId}`;
                const title = entry.group ? entry.group.name : productName;
                const unit = inventoryItem?.item.product.unitOfMeasure ?? 'units';
                const restockRow = restock?.items.find((item) => item.itemId === entry.itemId);
                const groupedLine = Boolean(entry.group) || (restockRow != null && restockItemIsGrouped(restockRow));
                const groupSettingsId = entry.group?.id ?? restockRow?.groupId;
                return (
                  <Table.Tr key={entry.id === '' ? `auto-${entry.itemId}` : entry.id}>
                    <Table.Td>
                      <Stack gap={2}>
                        <Group gap="xs" align="center">
                          <Text fw={600}>{title}</Text>
                          {(entry as ShoppingListEntryW3).touched && (entry as ShoppingListEntryW3).edited && (
                            <ShoppingListEditedTag
                              edited={(entry as ShoppingListEntryW3).edited!}
                              session={currentAuthSession()}
                            />
                          )}
                        </Group>
                        {entry.group && <Text size="sm">{productName}</Text>}
                        {entry.group && (
                          <Badge variant="light" color={entry.group.ruleConfirmed ? undefined : 'yellow'} w="fit-content">
                            {entry.group.ruleConfirmed ? ruleLabel(entry.group.rule) : 'Pick a rule'}
                          </Badge>
                        )}
                        {entry.note !== undefined && entry.note !== '' && (
                          <Text size="sm" c="dimmed">{entry.note}</Text>
                        )}
                        {entry.group && entry.group.members.length > 1 && entry.id !== '' && (
                          <TripBuyPicker
                            members={entry.group.members}
                            selectedItemId={entry.itemId}
                            busy={Boolean(swapBusyByLine[entry.id])}
                            errorMessage={swapErrorByLine[entry.id]}
                            onSelect={(itemId) => void swapLine(entry, itemId)}
                          />
                        )}
                      </Stack>
                    </Table.Td>
                    <Table.Td data-label="Quantity">
                      <Stack gap={4}>
                        <Text>{entry.quantity} {unit}</Text>
                        {entry.adjustment !== undefined && entry.computedQuantity !== undefined && (
                          <Text size="sm" c="dimmed">Computed {entry.computedQuantity} {unit}</Text>
                        )}
                        {entry.id !== '' && targetProvider !== null && (
                          <ChangeAmountWithSkipConfirm
                            entryId={entry.id}
                            productName={productName}
                            line={{
                              itemId: entry.itemId,
                              groupId: entry.group?.id,
                              productName,
                              groupName: entry.group?.name,
                            }}
                            restock={restock}
                            groups={groupTargets}
                            defaultQuantity={entry.quantity}
                            inputKey={`${entry.id}-${entry.quantity}-${inlineSkipVisible(entry)}`}
                            showInlineConfirm={inlineSkipVisible(entry)}
                            onBlurSave={(value) => void changeAdjustment(entry, value)}
                            nudge={{
                              offline,
                              loading: nudgeLoadingKey === entry.id,
                              error: nudgeErrorByRow[entry.id] ?? '',
                              onDontRestock: () => void runDontRestockNudge({
                                itemId: entry.itemId,
                                groupId: entry.group?.id,
                                productName,
                                groupName: entry.group?.name,
                              }, entry.id),
                            }}
                          />
                        )}
                        {entry.basis && (
                          <Text
                            size="xs"
                            className={`stock-note stock-note--${stockState(entry.basis.instanceCount, entry.basis.instanceCount > 0)}`}
                          >
                            {`${entry.basis.instanceCount} on hand`}
                            {restock?.items.find((item) => item.itemId === entry.itemId)?.rowSource
                              ? ` · ${restock.items.find((item) => item.itemId === entry.itemId)?.rowSource}`
                              : ''}
                          </Text>
                        )}
                      </Stack>
                    </Table.Td>
                    <Table.Td data-label="Source"><Badge variant="light">{entry.source === 'auto' ? 'Derived' : 'Manual'}</Badge></Table.Td>
                    <Table.Td data-label="Actions">
                      {entry.id === '' ? (
                        <Text size="sm" c="dimmed">Updates when inventory changes</Text>
                      ) : (
                        <Group gap="xs">
                          {restock !== null && shoppingListHasRestockRow({ itemId: entry.itemId }, restock)
                            ? (() => {
                              const snapshot = restock;
                              const row = snapshot.items.find((item) => item.itemId === entry.itemId);
                              const category = row?.categories[0];
                              return (
                                <Menu position="bottom-end">
                                  <Menu.Target>
                                    <ActionIcon variant="subtle" aria-label={`Actions for ${productName}`}>⋮</ActionIcon>
                                  </Menu.Target>
                                  <Menu.Dropdown>
                                    {groupedLine && groupSettingsId ? (
                                      <Menu.Item component={Link} to={`/groups/${groupSettingsId}#keep`}>
                                        {groupGoalCopy.openGroupSettings}
                                      </Menu.Item>
                                    ) : (
                                      <Menu.Item onClick={() => setEditorId(entry.itemId)}>Change target…</Menu.Item>
                                    )}
                                    <Menu.Item onClick={() => {
                                      if (row == null) return;
                                      void dontRestockItem(row.itemId).then((result) => {
                                        showUndo(result.title, result.detail, () => {
                                          void (result.previous ? setItemTarget(row.itemId, result.previous) : clearItemTarget(row.itemId)).then(() => refresh());
                                        });
                                        void refresh();
                                      });
                                    }}
                                    >
                                      {`Don't restock ${productName}`}
                                    </Menu.Item>
                                    {category && (
                                      <Menu.Item onClick={() => {
                                        void previewCategoryTarget(category.id, { kind: 'months', value: 0 }).then((next) => {
                                          setPendingCategory(category.id);
                                          setPreview(next);
                                        });
                                      }}
                                      >
                                        {`Don't restock ${category.name}…`}
                                      </Menu.Item>
                                    )}
                                  </Menu.Dropdown>
                                </Menu>
                              );
                            })()
                            : null}
                          <Button size="xs" onClick={() => void updateEntry(entry, 'purchase')}>
                            Mark {productName} purchased
                          </Button>
                          <Button
                            size="xs"
                            color="red"
                            variant="light"
                            className="shopping-row-remove"
                            onClick={() => void updateEntry(entry, 'remove')}
                          >
                            Remove {productName}
                          </Button>
                        </Group>
                      )}
                    </Table.Td>
                  </Table.Tr>
                );
              })}
            </Table.Tbody>
          </Table>
          )}
          {zeroForTripOffList.length > 0 && (
            <section className="shopping-not-on-list" aria-label="Not on the list">
              <Button
                type="button"
                variant="subtle"
                className="shopping-not-on-list-toggle"
                aria-expanded={notOnListOpen}
                onClick={() => setNotOnListOpen((open) => !open)}
              >
                {`Not on the list (${zeroForTripOffList.length})`}
              </Button>
              <Collapse expanded={notOnListOpen}>
                <Stack gap="md" className="shopping-not-on-list-rows">
                  {zeroForTripOffList.map((row) => {
                    const zft = row.zeroForTrip;
                    const actorFields = {
                      actorUserId: zft?.actorUserId,
                      actorDisplayName: zft?.actorDisplayName,
                      setAt: zft?.setAt,
                      groupName: row.groupId ? row.name : undefined,
                      actorRole: zeroForTripActorRole(zft?.actorUserId, currentAuthSession()),
                    };
                    const rowKey = row.id;
                    const itemId = row.itemId ?? '';
                    const lineTitle = zeroForTripDisplayName(row.name);
                    const nudgeLine: RestockNudgeTarget = {
                      itemId: row.itemId,
                      groupId: row.groupId,
                      productName: lineTitle,
                      groupName: row.groupId ? row.name?.trim() || undefined : undefined,
                    };
                    const entryId = zft?.entryId ?? '';
                    const providerId = zft?.providerId ?? targetProvider?.id ?? '';
                    return (
                      <article key={rowKey} className="shopping-not-on-list-row" data-row-key={rowKey}>
                        <Text fw={600}>{lineTitle}</Text>
                        <ZeroForTripStatusBlock
                          row={row}
                          actorFields={actorFields}
                          restock={restock}
                          groups={groupTargets}
                          changeAmount={entryId !== '' && providerId !== '' ? {
                            entryId,
                            defaultQuantity: 0,
                            inputKey: `offlist-${entryId}-${rowKey}`,
                            productName: lineTitle,
                            onBlurSave: (value) => {
                              const provider = targetProvider?.id ?? providerId;
                              void setShoppingListAdjustment(entryId, provider, typeof value === 'number' ? value : Number(value)).then(() => refresh());
                            },
                          } : undefined}
                          nudge={(itemId !== '' || row.groupId) ? {
                            offline,
                            loading: nudgeLoadingKey === rowKey,
                            error: nudgeErrorByRow[rowKey] ?? '',
                            onDontRestock: () => void runDontRestockNudge(nudgeLine, rowKey),
                          } : undefined}
                        />
                      </article>
                    );
                  })}
                </Stack>
              </Collapse>
            </section>
          )}
        </div>
      )}
      {restock && restock.notRestocked > 0 && (
        <Text size="sm" c="dimmed">
          {restockCopy.notRestocked(restock.notRestocked)}{' '}
          <Anchor component={Link} to="/inventory?filter=restock">{restockCopy.review}</Anchor>
        </Text>
      )}
      <TargetEditor
        opened={editorId !== ''}
        title="Restock target"
        initial={restock?.items.find((item) => item.itemId === editorId)?.target}
        onClose={() => {
          setEditorId('');
          setTargetInGroup(null);
        }}
        onSave={async (target: RestockTarget) => {
          setTargetInGroup(null);
          try {
            await setItemTarget(editorId, target);
            await refresh();
          } catch (requestError) {
            if (requestError instanceof ApiError && requestError.code === 'in_group' && requestError.inGroup) {
              setTargetInGroup(requestError.inGroup);
              throw requestError;
            }
            throw requestError;
          }
        }}
      />
      {targetInGroup && <InGroupInlineAlert members={targetInGroup} />}
      <StopDialog
        opened={preview !== null}
        preview={preview}
        onClose={() => { setPreview(null); setPendingCategory(''); }}
        onConfirm={() => {
          void setCategoryTarget(pendingCategory, { kind: 'months', value: 0 }).then(() => {
            setPreview(null);
            setPendingCategory('');
            void refresh();
          });
        }}
      />
    </Stack>
  );
};
