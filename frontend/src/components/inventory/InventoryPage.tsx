import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState } from 'react';
import { useLocation, useNavigate, useSearchParams } from 'react-router-dom';
import { trackEventSource } from '../../telemetry/client';
import { Alert, Button, Drawer, Group, Stack, Text, TextInput, Title, UnstyledButton } from '@mantine/core';
import {
  addItemCategory, clearItemTarget, createGroup, dontRestockItem, getInventoryList, getRestock, getSupplySettings,
  listGroups, listProducts, listSuggestions, removeItemCategory, rescanSuggestions, setItemTarget, stage1AuthActive,
} from '../../api/client';
import { inlineFailure, offlineBannerActive, subscribeOfflineBanner } from '@stockbot/shared/contract';
import type { GroupSuggestion, InventoryItem, Product, ProductGroup } from '@stockbot/shared/types';
import { HOUSEHOLD_SWITCH_EVENT } from '../household/liveHints';
import { InboxPage } from '../groups/InboxPage';
import { AddItemSheet } from './AddItemSheet';
import { AddToGroupSheet } from './AddToGroupSheet';
import { ADD_ITEM_BUTTON, addedItemToast } from './noBarcodeCopy';
import { GroupRow } from './GroupRow';
import { GroupSelectBar } from './GroupSelectBar';
import { ItemDetailSheet } from './ItemDetailSheet';
import { ItemRow } from './ItemRow';
import { useListView } from './listViewContext';
import {
  inventoryScroller,
  neighborIds,
  restorationTarget,
  scrollAfterLoad,
  type ListViewSnapshot,
} from './listViewState';
import { OpeningBanner } from './OpeningBanner';
import { mergeInventoryEvent } from './inventoryUtils';
import {
  asCatalog, asSuggestions, buildShelf, filterShelf, groupsForShelf, itemHand, parseShelfFilter, type ShelfFilter, type ShelfMember,
} from './shelf';
import type { RestockItem, RestockSnapshot } from '../../restock/types';
import { restockCopy } from '../../restock/copy';
import { TargetEditor } from '../restock/TargetEditor';
import { showUndo } from '../restock/undo';
import '../restock/restock.css';
import '../groups/groups.css';
import './shelf.css';
import '../history/history.css';

interface InventoryNavState {
  fromList?: boolean;
  overlay?: 'target' | 'group';
  section?: 'instances' | 'categories';
}

const formatKnownAt = (timestamp: number): string =>
  new Date(timestamp).toLocaleTimeString(undefined, { hour: 'numeric', minute: '2-digit' });

const chips: { id: ShelfFilter; label: string }[] = [
  { id: 'all', label: 'All' },
  { id: 'groups', label: 'Groups' },
  { id: 'categories', label: 'Categories' },
  { id: 'low', label: 'Running low' },
  { id: 'restock', label: 'Not restocked' },
];

export const InventoryPage = () => {
  const navigate = useNavigate();
  const location = useLocation();
  const listView = useListView();
  const navState = (location.state ?? null) as InventoryNavState | null;
  const initialView = listView.peek();
  const pendingSnapshot = useRef<ListViewSnapshot | null>(initialView);
  const restorePending = useRef(initialView !== null);
  const userMoved = useRef(false);
  const [params, setParams] = useSearchParams();
  const filter = parseShelfFilter(params.get('filter'));
  const itemParam = params.get('item');
  const reviewOpen = itemParam === null && (params.get('review') === '1' || params.get('suggestion') !== null);
  const suggestionId = itemParam === null ? params.get('suggestion') ?? undefined : undefined;
  const [inventoryItems, setInventoryItems] = useState<InventoryItem[]>([]);
  const [groups, setGroups] = useState<ProductGroup[]>([]);
  const [suggestions, setSuggestions] = useState<GroupSuggestion[]>([]);
  const [catalog, setCatalog] = useState<Product[]>([]);
  const [householdMonths, setHouseholdMonths] = useState<number | undefined>();
  const [restock, setRestock] = useState<RestockSnapshot | null>(null);
  const [editorItem, setEditorItem] = useState<RestockItem | null>(null);
  const [searchQuery, setSearchQuery] = useState(initialView?.search ?? '');
  const [expandedGroupId, setExpandedGroupId] = useState<string | null>(initialView?.expandedGroupId ?? null);
  const [placeholderHeight, setPlaceholderHeight] = useState(initialView?.listHeight ?? 0);
  const [loadedAt, setLoadedAt] = useState<number | null>(null);
  const [highlightId, setHighlightId] = useState<string | null>(null);
  const [offlineNow, setOfflineNow] = useState(() => offlineBannerActive());
  const [browserOffline, setBrowserOffline] = useState(() => typeof navigator !== 'undefined' && navigator.onLine === false);
  const [selecting, setSelecting] = useState(false);
  const [checked, setChecked] = useState<string[]>([]);
  const [sheet, setSheet] = useState<{ productId: string; name: string; fromGroupId?: string } | null>(null);
  const [loading, setLoading] = useState(true);
  const [offlineLoad, setOfflineLoad] = useState(false);
  const [scanning, setScanning] = useState(false);
  const [notice, setNotice] = useState('');
  const [error, setError] = useState('');
  const [opening, setOpening] = useState(false);
  const [addItemOpen, setAddItemOpen] = useState(false);
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
      setLoadedAt(Date.now());
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

  useEffect(() => subscribeOfflineBanner(() => setOfflineNow(offlineBannerActive())), []);

  useEffect(() => {
    const update = () => setBrowserOffline(navigator.onLine === false);
    window.addEventListener('online', update);
    window.addEventListener('offline', update);
    return () => {
      window.removeEventListener('online', update);
      window.removeEventListener('offline', update);
    };
  }, []);

  const resetNonceRef = useRef(0);
  const handledReset = useRef(0);
  const [appliedReset, setAppliedReset] = useState(listView.resetNonce);
  if (listView.resetNonce !== appliedReset) {
    setAppliedReset(listView.resetNonce);
    if (listView.resetNonce !== 0) {
      setSearchQuery('');
      setExpandedGroupId(null);
      setSelecting(false);
      setChecked([]);
      setHighlightId(null);
      setPlaceholderHeight(0);
    }
  }

  useEffect(() => {
    if (listView.resetNonce === 0) return undefined;
    resetNonceRef.current = listView.resetNonce;
    restorePending.current = false;
    pendingSnapshot.current = null;
    position.current.scrollTop = 0;
    const pin = () => {
      const scroller = inventoryScroller();
      if (scroller) scroller.scrollTop = 0;
    };
    pin();
    const frame = window.requestAnimationFrame(pin);
    return () => window.cancelAnimationFrame(frame);
  }, [listView.resetNonce]);

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
    updated.delete('item');
    updated.set('suggestion', id);
    updated.delete('review');
    setParams(updated, { replace: true });
  };

  const openItem = (id: string, section?: 'instances' | 'categories') => {
    const updated = new URLSearchParams(params);
    updated.set('item', id);
    updated.delete('review');
    updated.delete('suggestion');
    navigate(
      { pathname: '/inventory', search: updated.toString() },
      { state: { fromList: true, section } satisfies InventoryNavState },
    );
  };

  const openChild = (overlay: 'target' | 'group') => {
    navigate(
      { pathname: '/inventory', search: params.toString() },
      { state: { fromList: navState?.fromList ?? true, section: navState?.section, overlay } satisfies InventoryNavState },
    );
  };

  const closeSheet = () => {
    if (navState?.overlay) {
      navigate(-1);
      return;
    }
    if (navState?.fromList) {
      navigate(-1);
      return;
    }
    const updated = new URLSearchParams(params);
    updated.delete('item');
    setParams(updated, { replace: true });
  };

  const toggleChecked = (productId: string, next: boolean) => {
    setChecked((current) => next ? [...new Set([...current, productId])] : current.filter((id) => id !== productId));
  };

  const startSelecting = (productId?: string) => {
    setSelecting(true);
    if (productId) toggleChecked(productId, true);
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

  const detailItem = itemParam === null ? null : inventoryItems.find((entry) => entry.item.id === itemParam) ?? null;
  const detailView = detailItem ? restockByItem.get(detailItem.item.id) ?? null : null;
  const childOverlay = itemParam !== null ? navState?.overlay ?? null : null;
  const sheetEditorOpen = childOverlay === 'target';
  const lastItemRef = useRef<string | null>(initialView?.lastItemId ?? null);
  const itemWas = useRef<string | null>(itemParam);
  const openedFromList = useRef(false);

  const empty = !loading && !offlineLoad && error === '' && inventoryItems.length === 0 && groups.length === 0;

  const live = useRef({
    search: searchQuery,
    filter,
    expandedGroupId,
    rowIds: [] as string[],
  });
  useEffect(() => {
    live.current = {
      search: searchQuery,
      filter,
      expandedGroupId,
      rowIds: rows.map((row) => row.id),
    };
  });

  useEffect(() => {
    if (!highlightId) return undefined;
    const timer = window.setTimeout(() => setHighlightId(null), 1500);
    return () => window.clearTimeout(timer);
  }, [highlightId]);

  useEffect(() => {
    if (!itemParam) return;
    lastItemRef.current = itemParam;
    openedFromList.current = navState?.fromList === true;
  }, [itemParam, navState]);

  useEffect(() => {
    const previous = itemWas.current;
    itemWas.current = itemParam;
    const fromReset = resetNonceRef.current !== 0 && resetNonceRef.current !== handledReset.current;
    if (fromReset) handledReset.current = resetNonceRef.current;
    if (previous && !itemParam && !childOverlay) {
      if (fromReset) {
        document.getElementById('inventory-heading')?.focus({ preventScroll: true });
        return;
      }
      setHighlightId(previous);
      const opener = openedFromList.current ? document.getElementById(`shelf-open-${previous}`) : null;
      if (opener) opener.focus({ preventScroll: true });
      else document.getElementById('inventory-heading')?.focus({ preventScroll: true });
    }
  }, [itemParam, childOverlay, listView.resetNonce]);

  const saveView = useRef(listView.save);
  const position = useRef({ scrollTop: initialView?.scrollTop ?? 0, listHeight: initialView?.listHeight ?? 0, itemOffset: initialView?.itemOffset ?? 0 });
  useEffect(() => {
    saveView.current = listView.save;
  });
  useEffect(() => {
    let frame = 0;
    const remember = () => {
      const scroller = inventoryScroller();
      const list = document.querySelector('.shelf-list');
      if (scroller && list) {
        position.current.scrollTop = scroller.scrollTop;
        position.current.listHeight = list.getBoundingClientRect().height;
        const id = lastItemRef.current;
        const row = id ? document.getElementById(`shelf-row-${id}`) : null;
        if (row) position.current.itemOffset = row.getBoundingClientRect().top - scroller.getBoundingClientRect().top;
      }
      frame = window.requestAnimationFrame(remember);
    };
    frame = window.requestAnimationFrame(remember);
    return () => window.cancelAnimationFrame(frame);
  }, []);
  useEffect(() => () => {
    if (restorePending.current) return;
    const current = live.current;
    const lastItemId = lastItemRef.current;
    const neighbors = neighborIds(current.rowIds, lastItemId);
    saveView.current({
      scrollTop: position.current.scrollTop,
      listHeight: position.current.listHeight,
      search: current.search,
      filter: current.filter === 'all' ? '' : current.filter,
      expandedGroupId: current.expandedGroupId,
      lastItemId,
      before: neighbors.before,
      after: neighbors.after,
      itemOffset: position.current.itemOffset,
      savedAt: Date.now(),
    });
  }, []);

  useLayoutEffect(() => {
    if (!loading || !restorePending.current || userMoved.current) return;
    const saved = pendingSnapshot.current;
    const scroller = inventoryScroller();
    if (saved && scroller) scroller.scrollTop = saved.scrollTop;
  });

  useEffect(() => {
    if (!loading || !restorePending.current) return undefined;
    const scroller = inventoryScroller();
    if (!scroller) return undefined;
    const mark = () => {
      userMoved.current = true;
    };
    const onKey = (event: KeyboardEvent) => {
      if (['ArrowDown', 'ArrowUp', 'PageDown', 'PageUp', 'Home', 'End', ' '].includes(event.key)) mark();
    };
    scroller.addEventListener('wheel', mark, { passive: true });
    scroller.addEventListener('touchmove', mark, { passive: true });
    window.addEventListener('keydown', onKey);
    return () => {
      scroller.removeEventListener('wheel', mark);
      scroller.removeEventListener('touchmove', mark);
      window.removeEventListener('keydown', onKey);
    };
  }, [loading]);

  useLayoutEffect(() => {
    if (loading || !restorePending.current) return;
    if (error !== '' || offlineLoad) return;
    const saved = pendingSnapshot.current;
    restorePending.current = false;
    if (!saved) return;
    const scroller = inventoryScroller();
    const target = restorationTarget(rows.map((row) => row.id), saved.lastItemId, saved.before, saved.after);
    if (scroller) {
      const row = target.id ? document.getElementById(`shelf-row-${target.id}`) : null;
      const targetTop = row
        ? row.getBoundingClientRect().top - scroller.getBoundingClientRect().top + scroller.scrollTop
        : null;
      scroller.scrollTop = scrollAfterLoad({
        userMoved: userMoved.current,
        currentScrollTop: scroller.scrollTop,
        targetTop,
        itemOffset: saved.itemOffset,
        savedScrollTop: saved.scrollTop,
        scrollHeight: scroller.scrollHeight,
        clientHeight: scroller.clientHeight,
      });
    }
    if (!userMoved.current && target.highlight && target.id) {
      setHighlightId(target.id);
      document.getElementById(`shelf-open-${target.id}`)?.focus();
    }
    pendingSnapshot.current = null;
    listView.consume();
  }, [loading, error, offlineLoad, rows, listView]);

  return (
    <Stack gap="sm" className="page-wide bin-page shelf-page">
      <Group className="page-cluster" justify="space-between" align="center" wrap="nowrap">
        <Title id="inventory-heading" order={1} size="h3" tabIndex={-1}>Inventory</Title>
        <Button size="xs" variant="default" onClick={() => setAddItemOpen(true)}>
          {ADD_ITEM_BUTTON}
        </Button>
      </Group>
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
      <div role="status" aria-live="polite" className="shelf-sr">{loading ? 'Loading inventory' : ''}</div>
      {!loading && error !== '' && <Alert color="red" py="xs">{error}</Alert>}
      {empty && <Text c="dimmed">Your inventory is empty.</Text>}
      {!loading && !offlineLoad && error === '' && !empty && rows.length === 0 && filter !== 'categories' && (
        <Text c="dimmed">No products or groups match.</Text>
      )}
      {!loading && filter === 'categories' && restock && (
        <Stack gap={0}>
          {restock.categories.map((category) => (
            <UnstyledButton key={category.id} className="restock-row" onClick={() => navigate(`/inventory/categories/${category.id}`)}>
              <span className="name"><b>{category.name}</b><span>{`${category.items} items`}</span></span>
              <span className="value">{category.valueLabel}<span>{category.sourceLabel}</span></span>
            </UnstyledButton>
          ))}
        </Stack>
      )}
      {(loading && error === '' && !offlineLoad) || (filter !== 'categories' && rows.length > 0) ? (
        <div className="shelf-list" aria-busy={loading} aria-label="Inventory">
          {loading && error === '' && !offlineLoad && (
            <div
              className="shelf-placeholder"
              aria-hidden="true"
              style={placeholderHeight > 0 ? { height: `${placeholderHeight}px` } : undefined}
            />
          )}
          {!loading && filter !== 'categories' && rows.map((row) => {
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
                  onOpenMember={(member) => {
                    if (member.itemId) openItem(member.itemId, 'instances');
                  }}
                  highlightedItemId={highlightId}
                  onChanged={() => { void loadInventory(true); }}
                />
              );
            }
            const inventoryItem = row.item;
            return (
              <ItemRow
                key={row.id}
                inventoryItem={inventoryItem}
                hand={row.hand}
                suggestion={row.suggestion}
                onOpen={(section) => openItem(inventoryItem.item.id, section)}
                onAdd={() => {
                  if (itemParam) openChild('group');
                  else setSheet({ productId: inventoryItem.item.productId, name: inventoryItem.item.product.name });
                }}
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
                highlighted={highlightId === inventoryItem.item.id}
              />
            );
          })}
        </div>
      ) : null}
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
      <AddItemSheet
        opened={addItemOpen}
        mode="inventory"
        onClose={() => setAddItemOpen(false)}
        onCreated={(result) => {
          showUndo({ detail: addedItemToast(result.name) });
          void loadInventory(true);
        }}
      />
      {(sheet || childOverlay === 'group') && (
        <AddToGroupSheet
          opened
          zIndex={childOverlay === 'group' ? 400 : undefined}
          productId={childOverlay === 'group' ? (detailItem?.item.productId ?? sheet?.productId ?? '') : (sheet?.productId ?? '')}
          productName={childOverlay === 'group' ? (detailItem?.item.product.name ?? sheet?.name ?? '') : (sheet?.name ?? '')}
          fromGroupId={childOverlay === 'group' ? detailItem?.group?.id : sheet?.fromGroupId}
          groups={groups}
          suggestions={suggestions}
          onClose={() => {
            if (childOverlay === 'group') navigate(-1);
            else setSheet(null);
          }}
          onChanged={() => {
            if (childOverlay === 'group') navigate(-1);
            setSheet(null);
            void loadInventory(true);
          }}
        />
      )}
      <TargetEditor
        opened={editorItem !== null || sheetEditorOpen}
        sessionId={sheetEditorOpen ? (detailItem?.item.id ?? '') : (editorItem?.itemId ?? '')}
        title={(sheetEditorOpen ? detailItem?.item.product.name : editorItem?.name)
          ? `Restock target for ${sheetEditorOpen ? detailItem?.item.product.name : editorItem?.name}`
          : 'Restock target'}
        initial={sheetEditorOpen ? detailView?.target : editorItem?.target}
        onClose={() => {
          if (sheetEditorOpen) navigate(-1);
          else setEditorItem(null);
        }}
        onSave={async (target) => {
          const id = sheetEditorOpen ? detailItem?.item.id : editorItem?.itemId;
          if (!id) return;
          await setItemTarget(id, target);
          await loadInventory(true);
        }}
      />
      <ItemDetailSheet
        itemId={itemParam}
        item={detailItem}
        hand={detailItem ? itemHand(detailItem) : ''}
        loading={loading}
        loadError={error}
        offlineWithData={loadedAt !== null && (offlineLoad || offlineNow || browserOffline)}
        knownAt={loadedAt === null ? '' : formatKnownAt(loadedAt)}
        groupName={detailItem?.group?.name ?? null}
        groupId={detailItem?.group?.id ?? null}
        restockItem={detailView}
        section={navState?.section ?? null}
        child={childOverlay}
        onClose={closeSheet}
        onEditTarget={() => openChild('target')}
        onUseBroader={detailView?.useAction ? () => { void clearItemTarget(detailView.itemId).then(() => loadInventory(true)); } : undefined}
        onOpenCategory={(id) => navigate(`/inventory/categories/${id}`)}
        onAddCategory={detailView ? () => {
          const next = restock?.categories.find((category) => !detailView.categories.some((member) => member.id === category.id));
          if (next) void addItemCategory(detailView.itemId, next.id).then(() => loadInventory(true));
        } : undefined}
        onRemoveCategory={(id) => {
          if (!detailView) return;
          void removeItemCategory(detailView.itemId, id).then(() => loadInventory(true));
        }}
        onAddToGroup={() => openChild('group')}
        onHandChange={(delta) => {
          if (detailItem) shiftOnHand(detailItem.item.id, delta);
        }}
        onQuietSaved={() => { void loadInventory(true); }}
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
