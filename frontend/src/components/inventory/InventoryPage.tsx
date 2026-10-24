import { useCallback, useEffect, useMemo, useRef, useState, type ReactNode } from 'react';
import { useNavigate, useSearchParams } from 'react-router-dom';
import { trackEventSource } from '../../telemetry/client';
import { Alert, Button, Drawer, Group, Loader, Stack, Text, TextInput, Title, UnstyledButton } from '@mantine/core';
import {
  addItemCategory, clearItemTarget, createGroup, dontRestockItem, getInventoryList, getProduct, getRestock, getSupplySettings,
  listGroups, listProducts, listSuggestions, removeItemCategory, rescanSuggestions, setItemTarget, stage1AuthActive,
} from '../../api/client';
import { inlineFailure, offlineBannerActive, subscribeOfflineBanner } from '@stockbot/shared/contract';
import type { GroupSuggestion, InventoryItem, Product, ProductGroup } from '@stockbot/shared/types';
import { HOUSEHOLD_SWITCH_EVENT } from '../household/liveHints';
import { InboxPage } from '../groups/InboxPage';
import { ProductEditor } from '../product/ProductEditor';
import { AddToGroupSheet } from './AddToGroupSheet';
import { GroupRow } from './GroupRow';
import { GroupSelectBar } from './GroupSelectBar';
import { ItemInstanceList } from './ItemInstanceList';
import { ItemRow } from './ItemRow';
import { OpeningBanner } from './OpeningBanner';
import { mergeInventoryEvent } from './inventoryUtils';
import {
  asCatalog, asSuggestions, buildShelf, filterShelf, groupsForShelf, parseShelfFilter, type ShelfFilter, type ShelfMember,
} from './shelf';
import type { RestockItem, RestockSnapshot, RestockTarget } from '../../restock/types';
import { restockCopy } from '../../restock/copy';
import { TargetCard } from '../restock/TargetCard';
import { TargetEditor } from '../restock/TargetEditor';
import { showUndo } from '../restock/undo';
import '../restock/restock.css';
import '../groups/groups.css';
import './shelf.css';
import '../history/history.css';

const ProductBarcodeLine = ({ productId }: { productId: string }) => {
  const [barcodes, setBarcodes] = useState<string[]>([]);

  useEffect(() => {
    let active = true;
    void getProduct(productId)
      .then((product) => {
        if (active) setBarcodes(product.barcodes ?? []);
      })
      .catch(() => {
        if (active) setBarcodes([]);
      });
    return () => {
      active = false;
    };
  }, [productId]);

  if (barcodes.length === 0) return null;

  return (
    <Text size="xs" c="dimmed" className="copyable-barcode">
      Barcode: {barcodes.join(', ')}
    </Text>
  );
};

const chips: { id: ShelfFilter; label: string }[] = [
  { id: 'all', label: 'All' },
  { id: 'groups', label: 'Groups' },
  { id: 'categories', label: 'Categories' },
  { id: 'low', label: 'Running low' },
  { id: 'restock', label: 'Not restocked' },
];

export const InventoryPage = () => {
  const navigate = useNavigate();
  const [params, setParams] = useSearchParams();
  const filter = parseShelfFilter(params.get('filter'));
  const reviewOpen = params.get('review') === '1' || params.get('suggestion') !== null;
  const suggestionId = params.get('suggestion') ?? undefined;
  const [inventoryItems, setInventoryItems] = useState<InventoryItem[]>([]);
  const [groups, setGroups] = useState<ProductGroup[]>([]);
  const [suggestions, setSuggestions] = useState<GroupSuggestion[]>([]);
  const [catalog, setCatalog] = useState<Product[]>([]);
  const [householdMonths, setHouseholdMonths] = useState<number | undefined>();
  const [restock, setRestock] = useState<RestockSnapshot | null>(null);
  const [editorItem, setEditorItem] = useState<RestockItem | null>(null);
  const [searchQuery, setSearchQuery] = useState('');
  const [selectedItemId, setSelectedItemId] = useState<string | null>(null);
  const [expandedGroupId, setExpandedGroupId] = useState<string | null>(null);
  const [selecting, setSelecting] = useState(false);
  const [checked, setChecked] = useState<string[]>([]);
  const [sheet, setSheet] = useState<{ productId: string; name: string; fromGroupId?: string } | null>(null);
  const [loading, setLoading] = useState(true);
  const [offlineLoad, setOfflineLoad] = useState(false);
  const [scanning, setScanning] = useState(false);
  const [notice, setNotice] = useState('');
  const [error, setError] = useState('');
  const [opening, setOpening] = useState(false);
  const itemsRef = useRef<InventoryItem[]>([]);
  const rescanned = useRef(false);
  useEffect(() => {
    itemsRef.current = inventoryItems;
  }, [inventoryItems]);

  const loadInventory = useCallback(async (quiet = false) => {
    if (!quiet) setLoading(true);
    setError('');
    try {
      const [items, settings, listed, cards, products, snapshot] = await Promise.all([
        getInventoryList(),
        getSupplySettings().catch(() => null),
        listGroups().catch(() => [] as ProductGroup[]),
        listSuggestions().catch(() => [] as GroupSuggestion[]),
        listProducts().catch(() => [] as Product[]),
        getRestock().catch(() => null),
      ]);
      setInventoryItems(items);
      setRestock(snapshot);
      setOpening(settings?.opening === true);
      setHouseholdMonths(settings?.months);
      setGroups(groupsForShelf(listed, items));
      setSuggestions(asSuggestions(cards));
      setCatalog(asCatalog(products));
      setOfflineLoad(false);
    } catch (requestError) {
      const message = inlineFailure(requestError, 'Unable to load inventory.');
      setOfflineLoad(message === null);
      setError(message ?? '');
    } finally {
      if (!quiet) setLoading(false);
    }
  }, []);

  useEffect(() => {
    void Promise.resolve().then(() => loadInventory());
  }, [loadInventory]);

  useEffect(() => {
    if (!offlineLoad) return undefined;
    return subscribeOfflineBanner(() => {
      if (!offlineBannerActive()) void loadInventory(true);
    });
  }, [offlineLoad, loadInventory]);

  useEffect(() => {
    let eventSource: EventSource | null = null;
    let poll = 0;
    let stopped = false;
    const connect = () => {
      if (stopped) return;
      if (poll !== 0) window.clearInterval(poll);
      poll = 0;
      eventSource?.close();
      eventSource = new EventSource('/api/events');
      trackEventSource(eventSource);
    eventSource.addEventListener('inventory', (message) => {
      const inventoryItem = JSON.parse((message as MessageEvent).data) as InventoryItem;
      const current = itemsRef.current;
      const touchesGroup = inventoryItem.group != null || current.some((item) =>
        item.group != null && item.item.productId === inventoryItem.item.productId);
      if (touchesGroup) {
        void loadInventory(true);
        return;
      }
      setInventoryItems((rows) => mergeInventoryEvent(rows, inventoryItem));
    });
    eventSource.addEventListener('hint', (message) => {
      let hint: { type?: string };
      try {
        hint = JSON.parse((message as MessageEvent).data) as { type?: string };
      } catch {
        return;
      }
      if (hint.type !== 'stock' && hint.type !== 'rekey') return;
      if (document.visibilityState === 'hidden') return;
      void loadInventory(true);
    });
    eventSource.onerror = () => {
      if (!stage1AuthActive() || poll !== 0 || document.visibilityState === 'hidden') return;
      poll = window.setInterval(() => {
        if (document.visibilityState === 'hidden') return;
        void loadInventory(true);
      }, 10_000);
    };
    };
    const onSwitch = () => {
      void loadInventory(true);
      if (stage1AuthActive()) connect();
    };
    connect();
    window.addEventListener(HOUSEHOLD_SWITCH_EVENT, onSwitch);
    return () => {
      stopped = true;
      window.removeEventListener(HOUSEHOLD_SWITCH_EVENT, onSwitch);
      if (poll !== 0) window.clearInterval(poll);
      eventSource?.close();
    };
  }, [loadInventory]);

  const rescan = useCallback(async () => {
    setScanning(true);
    setNotice('');
    setError('');
    try {
      const created = await rescanSuggestions();
      if (created.length === 0) setNotice('No new groups found');
      await loadInventory(true);
    } catch (requestError) {
      setError(inlineFailure(requestError, 'Unable to look for groups.') ?? '');
    } finally {
      setScanning(false);
    }
  }, [loadInventory]);

  useEffect(() => {
    if (params.get('rescan') !== '1' || rescanned.current) return;
    rescanned.current = true;
    const next = new URLSearchParams(params);
    next.delete('rescan');
    setParams(next, { replace: true });
    void rescan();
  }, [params, rescan, setParams]);

  const restockByItem = useMemo(() => {
    const map = new Map<string, RestockItem>();
    for (const item of restock?.items ?? []) map.set(item.itemId, item);
    return map;
  }, [restock]);

  const rows = useMemo(() => {
    const shelfFilter = filter === 'categories' || filter === 'restock' ? 'all' : filter;
    const off = new Set((restock?.items ?? []).filter((item) => item.target.value === 0).map((item) => item.productId));
    const items = filter === 'restock'
      ? inventoryItems.filter((item) => restockByItem.get(item.item.id)?.target.value === 0)
      : inventoryItems;
    const visibleGroups = filter === 'restock'
      ? groups.filter((group) => group.members.some((member) => off.has(member.productId)))
      : groups;
    return filterShelf(buildShelf({
      items,
      groups: visibleGroups,
      suggestions,
      catalog,
      householdMonths,
    }), searchQuery, shelfFilter);
  }, [inventoryItems, groups, suggestions, catalog, householdMonths, searchQuery, filter, restock, restockByItem]);

  const setFilter = (next: ShelfFilter) => {
    const updated = new URLSearchParams(params);
    if (next === 'all') updated.delete('filter');
    else updated.set('filter', next);
    setParams(updated, { replace: true });
  };

  const closeReview = () => {
    const updated = new URLSearchParams(params);
    updated.delete('review');
    updated.delete('suggestion');
    setParams(updated, { replace: true });
  };

  const openSuggestion = (id: string) => {
    const updated = new URLSearchParams(params);
    updated.set('suggestion', id);
    setParams(updated, { replace: true });
  };

  const toggleChecked = (productId: string, next: boolean) => {
    setChecked((current) => next ? [...new Set([...current, productId])] : current.filter((id) => id !== productId));
  };

  const startSelecting = (productId?: string) => {
    setSelecting(true);
    if (productId) toggleChecked(productId, true);
  };

  const selectItem = (itemId: string) => {
    setSelectedItemId((current) => current === itemId ? null : itemId);
  };

  const shiftOnHand = (itemId: string, delta: number) => {
    setInventoryItems((current) => current.map((entry) => (
      entry.item.id === itemId
        ? { ...entry, instanceCount: Math.max(0, entry.instanceCount + delta) }
        : entry
    )));
  };

  const startGroup = async (productId: string, name: string) => {
    setError('');
    try {
      const created = await createGroup(name, [productId]);
      navigate(`/groups/${created.id}`);
    } catch (requestError) {
      setError(inlineFailure(requestError, 'Unable to create the group.') ?? '');
    }
  };

  const saveItemTarget = async (item: RestockItem, target: RestockTarget) => {
    await setItemTarget(item.itemId, target);
    await loadInventory(true);
  };

  const stopItem = async (item: RestockItem) => {
    const result = await dontRestockItem(item.itemId);
    await loadInventory(true);
    showUndo(result.title, result.detail, () => {
      void (result.previous
        ? setItemTarget(item.itemId, result.previous)
        : clearItemTarget(item.itemId)
      ).then(() => loadInventory(true));
    });
  };

  const renderExpanded = (inventoryItem: InventoryItem): ReactNode => {
    const view = restockByItem.get(inventoryItem.item.id);
    return (
    <Stack gap="sm">
      {view && (
        <TargetCard
          item={view}
          onEdit={() => setEditorItem(view)}
          onUseBroader={view.useAction ? () => { void clearItemTarget(view.itemId).then(() => loadInventory(true)); } : undefined}
          onOpen={view.level === 'category' && view.categories[0] ? () => navigate(`/inventory/categories/${view.categories.find((category) => category.name === view.levelName)?.id ?? view.categories[0].id}`) : undefined}
        />
      )}
      {view && (
        <Group gap={6}>
          {view.categories.map((category) => (
            <Button key={category.id} size="compact-xs" variant="default" radius="xl" onClick={() => navigate(`/inventory/categories/${category.id}`)}>
              {category.name}
            </Button>
          ))}
          <Button
            size="compact-xs"
            variant="default"
            radius="xl"
            onClick={() => {
              const next = restock?.categories.find((category) => !view.categories.some((member) => member.id === category.id));
              if (next) void addItemCategory(view.itemId, next.id).then(() => loadInventory(true));
            }}
          >
            + Category
          </Button>
          {view.categories.map((category) => (
            <Button key={`remove-${category.id}`} size="compact-xs" variant="subtle" onClick={() => void removeItemCategory(view.itemId, category.id).then(() => loadInventory(true))}>
              {`Remove ${category.name}`}
            </Button>
          ))}
        </Group>
      )}
      <ProductBarcodeLine productId={inventoryItem.item.productId} />
      <ItemInstanceList
        itemId={inventoryItem.item.id}
        productName={inventoryItem.item.product.name}
        onHand={inventoryItem.instanceCount}
        onHandChange={(delta) => shiftOnHand(inventoryItem.item.id, delta)}
        onInventoryChanged={() => { void loadInventory(true); }}
      />
      <ProductEditor
        productId={inventoryItem.item.productId}
        onSaved={() => void loadInventory()}
      />
    </Stack>
    );
  };

  const empty = !loading && !offlineLoad && error === '' && inventoryItems.length === 0 && groups.length === 0;

  return (
    <Stack gap="sm" className="page-wide bin-page shelf-page">
      <Title order={1} size="h3" className="page-cluster">Inventory</Title>
      {opening && <OpeningBanner onComplete={() => setOpening(false)} />}
      <Group className="page-cluster" gap="xs" align="flex-end" wrap="nowrap">
        <TextInput
          size="xs"
          label="Search products or groups"
          placeholder="Search products or groups"
          value={searchQuery}
          onChange={(event) => setSearchQuery(event.currentTarget.value)}
          style={{ flex: 1 }}
        />
        <Button size="xs" variant="default" className="shelf-select" onClick={() => {
          if (selecting) {
            setSelecting(false);
            setChecked([]);
            return;
          }
          startSelecting();
        }}
        >
          {selecting ? 'Done' : 'Select'}
        </Button>
      </Group>
      <div className="shelf-filters" role="group" aria-label="Filter inventory">
        {chips.map((chip) => (
          <UnstyledButton
            key={chip.id}
            className="bin-chip"
            aria-pressed={filter === chip.id}
            onClick={() => setFilter(chip.id)}
          >
            {chip.label}
            {chip.id === 'restock' && restock && restock.notRestocked > 0 ? ` ${restock.notRestocked}` : ''}
          </UnstyledButton>
        ))}
      </div>
      {filter === 'groups' && (
        <Button size="sm" variant="default" className="suggestion-rescan" loading={scanning} onClick={() => void rescan()}>
          Look for more groups
        </Button>
      )}
      {notice !== '' && <p className="shelf-notice">{notice}</p>}
      {loading && <Loader aria-label="Loading inventory" />}
      {error !== '' && <Alert color="red" py="xs">{error}</Alert>}
      {empty && <Text c="dimmed">Your inventory is empty.</Text>}
      {!loading && !offlineLoad && error === '' && !empty && rows.length === 0 && (
        <Text c="dimmed">No products or groups match.</Text>
      )}
      {filter === 'categories' && restock && (
        <Stack gap={0}>
          {restock.categories.map((category) => (
            <UnstyledButton key={category.id} className="restock-row" onClick={() => navigate(`/inventory/categories/${category.id}`)}>
              <span className="name"><b>{category.name}</b><span>{`${category.items} items`}</span></span>
              <span className="value">{category.valueLabel}<span>{category.sourceLabel}</span></span>
            </UnstyledButton>
          ))}
        </Stack>
      )}
      {filter !== 'categories' && rows.length > 0 && (
        <div className="shelf-list">
          {rows.map((row) => {
            if (row.kind === 'group') {
              return (
                <GroupRow
                  key={row.id}
                  row={row}
                  expanded={expandedGroupId === row.id}
                  onToggle={() => setExpandedGroupId((current) => current === row.id ? null : row.id)}
                  onMove={(member: ShelfMember) => setSheet({
                    productId: member.productId,
                    name: member.name,
                    fromGroupId: row.id,
                  })}
                  onChanged={() => { void loadInventory(true); }}
                  onHandChange={shiftOnHand}
                />
              );
            }
            const inventoryItem = row.item;
            const selected = selectedItemId === inventoryItem.item.id;
            return (
              <ItemRow
                key={row.id}
                inventoryItem={inventoryItem}
                hand={row.hand}
                suggestion={row.suggestion}
                selected={selected}
                controlsId={`inventory-item-${inventoryItem.item.id}`}
                onSelect={() => selectItem(inventoryItem.item.id)}
                onAdd={() => setSheet({ productId: inventoryItem.item.productId, name: inventoryItem.item.product.name })}
                onStartGroup={() => void startGroup(inventoryItem.item.productId, inventoryItem.item.product.name)}
                onOpenSuggestion={row.suggestion ? () => openSuggestion(row.suggestion?.id ?? '') : undefined}
                selecting={selecting}
                checked={checked.includes(inventoryItem.item.productId)}
                onChecked={(next) => toggleChecked(inventoryItem.item.productId, next)}
                onLongPress={() => startSelecting(inventoryItem.item.productId)}
                onViewHistory={() => navigate(`/inventory/${inventoryItem.item.id}/history`)}
                quietLine={restockByItem.get(inventoryItem.item.id)?.quietLine}
                onRestockTarget={restockByItem.get(inventoryItem.item.id) ? () => setEditorItem(restockByItem.get(inventoryItem.item.id) ?? null) : undefined}
                onDontRestock={restockByItem.get(inventoryItem.item.id) ? () => { void stopItem(restockByItem.get(inventoryItem.item.id)!); } : undefined}
                dontRestockHint={restockCopy.dontItemHint}
                useBroaderLabel={restockByItem.get(inventoryItem.item.id)?.useAction}
                useBroaderHint={restockByItem.get(inventoryItem.item.id)?.useHint}
                onUseBroader={restockByItem.get(inventoryItem.item.id)?.useAction ? () => { void clearItemTarget(inventoryItem.item.id).then(() => loadInventory(true)); } : undefined}
                onCategories={() => selectItem(inventoryItem.item.id)}
              >
                {selected ? renderExpanded(inventoryItem) : null}
              </ItemRow>
            );
          })}
        </div>
      )}
      {selecting && checked.length > 0 && (
        <GroupSelectBar
          productIds={checked}
          groups={groups}
          onDone={() => {
            setSelecting(false);
            setChecked([]);
            void loadInventory(true);
          }}
        />
      )}
      {sheet && (
        <AddToGroupSheet
          opened
          productId={sheet.productId}
          productName={sheet.name}
          fromGroupId={sheet.fromGroupId}
          groups={groups}
          suggestions={suggestions}
          onClose={() => setSheet(null)}
          onChanged={() => {
            setSheet(null);
            void loadInventory(true);
          }}
        />
      )}
      <TargetEditor
        opened={editorItem !== null}
        title={editorItem ? `Restock target for ${editorItem.name}` : 'Restock target'}
        initial={editorItem?.target}
        onClose={() => setEditorItem(null)}
        onSave={async (target) => {
          if (!editorItem) return;
          await saveItemTarget(editorItem, target);
        }}
      />
      <Drawer.Root opened={reviewOpen} onClose={closeReview} position="right" size="md">
        <Drawer.Overlay />
        <Drawer.Content className="bin-page">
          {/* A header element here is a second banner landmark beside the app bar. */}
          <Drawer.Header component="div">
            <Drawer.Title>Suggestions</Drawer.Title>
            <Drawer.CloseButton aria-label="Close" />
          </Drawer.Header>
          <Drawer.Body>
            <InboxPage embedded focusId={suggestionId} />
          </Drawer.Body>
        </Drawer.Content>
      </Drawer.Root>
    </Stack>
  );
};
