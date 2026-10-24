import { useCallback, useEffect, useMemo, useState } from 'react';
import { Link } from 'react-router-dom';
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
  markShoppingListItemPurchased,
  removeShoppingListItem,
  swapShoppingLine,
  saveItemDeal,
  setShoppingListAdjustment,
} from '../../api/client';
import type { InventoryItem, ProviderInfo, ShoppingListEntry } from '@stockbot/shared/types';
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

const requestErrorMessage = (error: unknown, fallback: string) =>
  inlineFailure(error, fallback) ?? '';

export const ShoppingListPage = () => {
  const [entries, setEntries] = useState<ShoppingListEntry[]>([]);
  const [providers, setProviders] = useState<ProviderInfo[]>([]);
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

  const refresh = useCallback(async () => {
    const providerRows = await listProviders();
    const target = providerRows.find((row) => row.credentialsConfigured) ?? providerRows[0];
    const [shoppingEntries, inventoryItems, snapshot] = await Promise.all([
      getShoppingList(target?.id),
      getInventoryList(),
      getRestock().catch(() => null),
    ]);
    setProviders(providerRows);
    setEntries(shoppingEntries);
    setInventory(inventoryItems);
    setRestock(snapshot);
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

  const credentialsRevision = useCredentialsRevision();
  useEffect(() => {
    if (credentialsRevision === 0) return;
    void Promise.resolve().then(loadShoppingList);
  }, [credentialsRevision, loadShoppingList]);

  const targetProvider = providers.find((row) => row.credentialsConfigured) ?? providers[0] ?? null;

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
  const showDecisions = decisionsOpen || entries.length > 0;
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
          <ProvisionButton
            provider={targetProvider}
            entries={entries}
            explainEmpty={!loading}
            onFinished={() => void loadShoppingList()}
          />
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
      {error !== '' && (
        <Alert color="red" py="xs">
          {error}
        </Alert>
      )}
      {!loading && showDecisions && entries.length > 0 && (
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
              {entries.map((entry) => {
                const inventoryItem = inventoryByItemId.get(entry.itemId);
                const productName = inventoryItem?.item.product.name ?? `Item ${entry.itemId}`;
                const title = entry.group ? entry.group.name : productName;
                const unit = inventoryItem?.item.product.unitOfMeasure ?? 'units';
                return (
                  <Table.Tr key={entry.id === '' ? `auto-${entry.itemId}` : entry.id}>
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
                                    showUndo(result.title, result.detail, () => {
                                      void (result.previous ? setItemTarget(view.itemId, result.previous) : clearItemTarget(view.itemId)).then(() => refresh());
                                    });
                                    void refresh();
                                  });
                                }}
                                >
                                  {`Don't restock ${productName}`}
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
                            color="red"
                            variant="light"
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
