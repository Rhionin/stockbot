import { useCallback, useEffect, useMemo, useState } from 'react';
import { HOUSEHOLD_SWITCH_EVENT } from '../household/liveHints';
import { Link, useSearchParams } from 'react-router-dom';
import {
  ActionIcon,
  Alert,
  Anchor,
  Badge,
  Button,
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
  previewCategoryTarget,
  setCategoryTarget,
  setItemTarget,
  listProviders,
  stage1AuthActive,
  markShoppingListItemPurchased,
  removeShoppingListItem,
  swapShoppingLine,
  saveItemDeal,
  setShoppingListAdjustment,
} from '../../api/client';
import type { ProviderListEntry } from '@stockbot/shared/providerList';
import { krogerDisabledOnProviderList, providerListEntries } from '@stockbot/shared/providerList';
import type { InventoryItem, ShoppingListEntry } from '@stockbot/shared/types';
import type { RestockSnapshot } from '../../restock/types';
import { restockCopy } from '../../restock/copy';
import { StopDialog } from '../restock/StopDialog';
import { TargetEditor } from '../restock/TargetEditor';
import { showUndo } from '../restock/undo';
import type { RestockPreview, RestockTarget } from '../../restock/types';
import { ruleLabel } from '../groups/copy';
import { useCredentialsRevision } from '../../credentialsRefresh';
import { stockState } from '../../stockState';
import { ProviderPanel } from './ProviderPanel';
import { ProvisionButton } from './ProvisionButton';
import { inlineFailure } from '@stockbot/shared/contract';
import { krogerCallbackIsError, krogerCallbackMessage, shoppingKrogerLegacyMessage, shoppingRedirectMismatchMessage, storeNameCtx } from '../household/storeApp/krogerAppMessages';
import {
  RESTOCK_DONT_UNDO_DETAIL,
  SHOPPING_LIST_DONT_RESTOCK_MENU_HINT,
  SHOPPING_LIST_REMOVE_VISIBLE_LABEL,
  shoppingListRemoveGroupAriaLabel,
  shoppingListRemoveProductAriaLabel,
} from '@stockbot/shared/shoppingListCopy';
import { ShoppingListRemovedStatus } from './ShoppingListRemovedStatus';

const requestErrorMessage = (error: unknown, fallback: string) =>
  inlineFailure(error, fallback) ?? '';

type RemovedRowSlot = {
  productName: string;
  groupName?: string;
  focusStatus?: boolean;
};

const isRowKey = (key: unknown): key is string => typeof key === 'string' && key.length > 0;

const entryRowKey = (entry: ShoppingListEntry): string => {
  const { id, itemId } = entry;
  if (id !== undefined && id !== '') return id;
  return `auto-${itemId}`;
};

export const ShoppingListPage = () => {
  const [searchParams, setSearchParams] = useSearchParams();
  const [entries, setEntries] = useState<ShoppingListEntry[]>([]);
  const [providers, setProviders] = useState<ProviderListEntry[]>([]);
  const [inventory, setInventory] = useState<InventoryItem[]>([]);
  const [restock, setRestock] = useState<RestockSnapshot | null>(null);
  const [editorId, setEditorId] = useState('');
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
  const [krogerNotice, setKrogerNotice] = useState('');
  const [krogerNoticeIsError, setKrogerNoticeIsError] = useState(false);
  const [rowOrder, setRowOrder] = useState<string[]>([]);
  const [removedRows, setRemovedRows] = useState<Record<string, RemovedRowSlot>>({});

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
    const message = krogerCallbackMessage(outcome, storeNameCtx(null), true);
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
  }, []);

  const refresh = useCallback(async () => {
    const providerList = await listProviders();
    const providerRows = providerListEntries(providerList);
    const target = providerRows.find((row) => row.credentialsConfigured) ?? providerRows[0];
    const [shoppingEntries, inventoryItems, snapshot] = await Promise.all([
      getShoppingList(target?.id),
      getInventoryList(),
      getRestock().catch(() => null),
    ]);
    setProviders(providerList);
    setEntries(shoppingEntries);
    setInventory(inventoryItems);
    setRestock(snapshot);
    const entryKeys = shoppingEntries.map(entryRowKey).filter(isRowKey);
    setRowOrder((prev) => {
      const next: string[] = [];
      for (const key of prev) {
        if (!isRowKey(key)) continue;
        if (key.startsWith('removed:')) next.push(key);
        else if (entryKeys.includes(key)) next.push(key);
      }
      for (const key of entryKeys) {
        if (!next.includes(key) && !next.includes(`removed:${key}`)) next.push(key);
      }
      return next.length > 0 ? next : entryKeys;
    });
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
      await loadShoppingList();
    } catch (requestError) {
      setError(requestErrorMessage(requestError, 'Unable to save the quantity adjustment.'));
    }
  };

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
  const removedRowCount = Object.keys(removedRows).length;
  const showDecisions = decisionsOpen || entries.length > 0 || removedRowCount > 0;
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
    setError('');
    try {
      await swapShoppingLine(entry.id, itemId);
      await refresh();
    } catch (requestError) {
      setError(requestErrorMessage(requestError, 'Unable to change the product on this line.'));
    }
  };

  const entryByKey = useMemo(() => {
    const map = new Map<string, ShoppingListEntry>();
    for (const entry of entries) map.set(entryRowKey(entry), entry);
    return map;
  }, [entries]);

  const updateEntry = async (entry: ShoppingListEntry, action: 'purchase' | 'remove') => {
    if (entry.id === '') return;
    setError('');
    const inventoryItem = inventoryByItemId.get(entry.itemId);
    const productName = inventoryItem?.item.product.name ?? `Item ${entry.itemId}`;
    const rowKey = entryRowKey(entry);
    try {
      if (action === 'purchase') {
        await markShoppingListItemPurchased(entry.id);
        await refresh();
        return;
      }
      await removeShoppingListItem(entry.id);
      setEntries((prev) => prev.filter((row) => row.id !== entry.id));
      setRemovedRows((prev) => ({
        ...prev,
        [`removed:${rowKey}`]: {
          productName,
          groupName: entry.group?.name,
          focusStatus: true,
        },
      }));
      setRowOrder((order) =>
        order.filter(isRowKey).map((key) => (key === rowKey ? `removed:${rowKey}` : key)),
      );
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
            />
          )}
        </div>
      </div>
      <ProviderPanel providers={providers} onChanged={() => void loadShoppingList()} />
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
      {!loading && showDecisions && (entries.length > 0 || removedRowCount > 0) && (
        <div className="shopping-entries-scroll">
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
              {rowOrder.filter(isRowKey).map((rowKey) => {
                if (rowKey.startsWith('removed:')) {
                  const slot = removedRows[rowKey];
                  if (slot === undefined) return null;
                  return (
                    <Table.Tr key={rowKey} className="shopping-removed-row">
                      <Table.Td colSpan={4}>
                        <ShoppingListRemovedStatus
                          productName={slot.productName}
                          groupName={slot.groupName}
                          focusOnMount={slot.focusStatus}
                          onFocused={() => {
                            setRemovedRows((prev) => {
                              const current = prev[rowKey];
                              if (current === undefined || !current.focusStatus) return prev;
                              return { ...prev, [rowKey]: { ...current, focusStatus: false } };
                            });
                          }}
                        />
                      </Table.Td>
                    </Table.Tr>
                  );
                }
                const entry = entryByKey.get(rowKey);
                if (entry === undefined) return null;
                const inventoryItem = inventoryByItemId.get(entry.itemId);
                const productName = inventoryItem?.item.product.name ?? `Item ${entry.itemId}`;
                const title = entry.group ? entry.group.name : productName;
                const unit = inventoryItem?.item.product.unitOfMeasure ?? 'units';
                const removeAria = entry.group
                  ? shoppingListRemoveGroupAriaLabel(entry.group.name)
                  : shoppingListRemoveProductAriaLabel(productName);
                return (
                  <Table.Tr key={rowKey}>
                    <Table.Td>
                      <Stack gap={2}>
                        <Text fw={600}>{title}</Text>
                        {entry.group && <Text size="sm">{productName}</Text>}
                        {entry.group && (
                          <Badge variant="light" color={entry.group.ruleConfirmed ? undefined : 'yellow'} w="fit-content">
                            {entry.group.ruleConfirmed ? ruleLabel(entry.group.rule) : 'Pick a rule'}
                          </Badge>
                        )}
                        {entry.note !== undefined && entry.note !== '' && (
                          <Text size="sm" c="dimmed">{entry.note}</Text>
                        )}
                        {entry.group && entry.group.members.length > 1 && (
                          <NativeSelect
                            size="xs"
                            label="This trip, buy"
                            value={entry.itemId}
                            data={entry.group.members.map((member) => ({ value: member.itemId, label: member.name }))}
                            onChange={(event) => void swapLine(entry, event.currentTarget.value)}
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
                          <NumberInput
                            size="xs"
                            aria-label={`Provision quantity for ${productName}`}
                            min={0}
                            max={999}
                            step={1}
                            allowDecimal={false}
                            defaultValue={entry.quantity}
                            key={`${entry.id}-${entry.quantity}`}
                            onBlur={(event) => void changeAdjustment(entry, event.currentTarget.value)}
                            w={120}
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
                          {restock?.items.some((item) => item.itemId === entry.itemId) && (
                            <Menu position="bottom-end">
                              <Menu.Target>
                                <ActionIcon variant="subtle" aria-label={`Actions for ${productName}`}>⋮</ActionIcon>
                              </Menu.Target>
                              <Menu.Dropdown>
                                <Menu.Item onClick={() => setEditorId(entry.itemId)}>Change target…</Menu.Item>
                                <Menu.Item onClick={() => {
                                  const view = restock.items.find((item) => item.itemId === entry.itemId);
                                  if (!view) return;
                                  void dontRestockItem(view.itemId).then((result) => {
                                    showUndo(result.title, RESTOCK_DONT_UNDO_DETAIL, () => {
                                      void (result.previous ? setItemTarget(view.itemId, result.previous) : clearItemTarget(view.itemId)).then(() => refresh());
                                    });
                                    void refresh();
                                  });
                                }}
                                >
                                  <span className="bin-menu-label">{`Don't restock ${productName}`}</span>
                                  <span className="bin-menu-hint">{SHOPPING_LIST_DONT_RESTOCK_MENU_HINT}</span>
                                </Menu.Item>
                                {restock.items.find((item) => item.itemId === entry.itemId)?.categories[0] && (
                                  <Menu.Item onClick={() => {
                                    const category = restock.items.find((item) => item.itemId === entry.itemId)?.categories[0];
                                    if (!category) return;
                                    void previewCategoryTarget(category.id, { kind: 'months', value: 0 }).then((next) => {
                                      setPendingCategory(category.id);
                                      setPreview(next);
                                    });
                                  }}
                                  >
                                    {`Don't restock ${restock.items.find((item) => item.itemId === entry.itemId)?.categories[0]?.name}…`}
                                  </Menu.Item>
                                )}
                              </Menu.Dropdown>
                            </Menu>
                          )}
                          <Button size="xs" onClick={() => void updateEntry(entry, 'purchase')}>
                            Mark {productName} purchased
                          </Button>
                          <Button
                            size="xs"
                            variant="subtle"
                            className="shopping-row-remove"
                            aria-label={removeAria}
                            onClick={() => void updateEntry(entry, 'remove')}
                          >
                            {SHOPPING_LIST_REMOVE_VISIBLE_LABEL}
                          </Button>
                        </Group>
                      )}
                    </Table.Td>
                  </Table.Tr>
                );
              })}
            </Table.Tbody>
          </Table>
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
        onClose={() => setEditorId('')}
        onSave={async (target: RestockTarget) => {
          await setItemTarget(editorId, target);
          await refresh();
        }}
      />
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
