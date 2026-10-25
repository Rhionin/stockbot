import { useEffect, useMemo, useState } from 'react';
import { Badge, Button, Drawer, Group, Loader, Stack, Text, TextInput, UnstyledButton } from '@mantine/core';
import {
  addItemInstance,
  ApiError,
  ensureProductItem,
  removeItemInstance,
  searchProducts,
  stockOutItem,
  undoMove,
} from '../../api/client';
import {
  ADD_IT_AND_TAKE_IT_OUT,
  CODE_NONE_ON_HAND,
  inlineFailure,
} from '@stockbot/shared/contract';
import type { ProductSearchHit, ScanDirection } from '@stockbot/shared/types';
import { onHandLabel } from '../history/format';
import { AddItemSheet, type AddItemSheetMode } from '../inventory/AddItemSheet';
import {
  addNamedProductLabel,
  addedAndTookOutOneToast,
  addedOneToast,
  BACK_BUTTON,
  FIND_ITEM_INPUT_LABEL,
  FIND_ITEM_TITLE,
  findNoneOnHandBody,
  NONE_ON_HAND_TITLE,
  nothingCalledYet,
  QUERY_TOO_SHORT,
  SCANS_ADD_TO_STOCK,
  SCANS_REMOVE_FROM_STOCK,
  SEARCH_FAILURE,
  tookOutOneToast,
  TYPE_A_NAME_PLACEHOLDER,
} from '../inventory/noBarcodeCopy';
import { showUndo } from '../restock/undo';
import { ServerNoticeBanner } from '../shell/ServerNoticeBanner';

const SEARCH_DEBOUNCE_MS = 300;

export interface FindItemSheetProps {
  opened: boolean;
  direction: ScanDirection;
  onClose: () => void;
  onInventoryChanged: () => void;
}

type PendingZero = { hit: ProductSearchHit; itemId: string };

export function FindItemSheet({
  opened,
  direction,
  onClose,
  onInventoryChanged,
}: FindItemSheetProps) {
  const [query, setQuery] = useState('');
  const [debouncedQuery, setDebouncedQuery] = useState('');
  const [results, setResults] = useState<ProductSearchHit[]>([]);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState('');
  const [pendingZero, setPendingZero] = useState<PendingZero | null>(null);
  const [addSheet, setAddSheet] = useState<{ mode: AddItemSheetMode; name: string } | null>(null);
  const [acting, setActing] = useState(false);

  useEffect(() => {
    if (!opened) return undefined;
    const timer = window.setTimeout(() => setDebouncedQuery(query.trim()), SEARCH_DEBOUNCE_MS);
    return () => window.clearTimeout(timer);
  }, [opened, query]);

  useEffect(() => {
    if (!opened) return undefined;
    const q = debouncedQuery;
    if (q.length < 2) return undefined;
    let active = true;
    // eslint-disable-next-line react-hooks/set-state-in-effect -- debounced fetch lifecycle
    setLoading(true);
    setError('');
    void searchProducts(q)
      .then((hits) => {
        if (!active) return;
        setResults(hits);
      })
      .catch((requestError) => {
        if (!active) return;
        setResults([]);
        setError(inlineFailure(requestError, SEARCH_FAILURE) ?? '');
      })
      .finally(() => {
        if (active) setLoading(false);
      });
    return () => {
      active = false;
    };
  }, [opened, debouncedQuery]);

  const trimmedQuery = query.trim();
  const shortQuery = trimmedQuery.length > 0 && trimmedQuery.length < 2;
  const resultRows = useMemo(
    () => (debouncedQuery.length >= 2 ? results : []),
    [debouncedQuery, results],
  );
  const hasExactNameMatch = resultRows.some(
    (hit) => hit.name.toLowerCase() === trimmedQuery.toLowerCase(),
  );
  const showAddNamed = trimmedQuery.length >= 2 && !hasExactNameMatch;

  const addMode: AddItemSheetMode = direction === 'stock_out' ? 'stock_out' : 'stock_in';
  const modeSubtitle = direction === 'stock_in' ? SCANS_ADD_TO_STOCK : SCANS_REMOVE_FROM_STOCK;

  const resolveItemId = async (hit: ProductSearchHit): Promise<string> => {
    if (hit.itemId !== undefined && hit.itemId !== '') return hit.itemId;
    const ensured = await ensureProductItem(hit.productId);
    return ensured.itemId;
  };

  const stockInHit = async (hit: ProductSearchHit) => {
    setActing(true);
    setError('');
    try {
      const itemId = await resolveItemId(hit);
      const instance = await addItemInstance(itemId);
      onInventoryChanged();
      showUndo({
        detail: addedOneToast(hit.name),
        onUndo: () => {
          void removeItemInstance(instance.id)
            .then(() => onInventoryChanged())
            .catch((requestError: unknown) => {
              const message = inlineFailure(requestError, 'Unable to undo.');
              if (message !== null) setError(message);
            });
        },
      });
      onClose();
    } catch (requestError) {
      setError(inlineFailure(requestError, 'Unable to stock in.') ?? '');
    } finally {
      setActing(false);
    }
  };

  const stockOutHit = async (hit: ProductSearchHit, backfill = false) => {
    setActing(true);
    setError('');
    try {
      const itemId = await resolveItemId(hit);
      const result = await stockOutItem(itemId, backfill ? { backfill: true } : undefined);
      onInventoryChanged();
      if (backfill && result.backfill !== undefined) {
        const moveId = result.backfill.stockInMoveId;
        showUndo({
          detail: addedAndTookOutOneToast(hit.name),
          onUndo: moveId === '' ? undefined : () => {
            void undoMove(moveId)
              .then(() => onInventoryChanged())
              .catch((requestError: unknown) => {
                const message = inlineFailure(requestError, 'Unable to undo.');
                if (message !== null) setError(message);
              });
          },
        });
      } else {
        const moveId = result.stockOutMoveId ?? '';
        showUndo({
          detail: tookOutOneToast(hit.name),
          onUndo: moveId === '' ? undefined : () => {
            void undoMove(moveId)
              .then(() => onInventoryChanged())
              .catch((requestError: unknown) => {
                const message = inlineFailure(requestError, 'Unable to undo.');
                if (message !== null) setError(message);
              });
          },
        });
      }
      setPendingZero(null);
      onClose();
    } catch (requestError) {
      if (
        !backfill
        && requestError instanceof ApiError
        && requestError.code === CODE_NONE_ON_HAND
      ) {
        try {
          const itemId = await resolveItemId(hit);
          setPendingZero({ hit, itemId });
        } catch (resolveError) {
          setError(inlineFailure(resolveError, 'Unable to stock out.') ?? '');
        }
        return;
      }
      setError(inlineFailure(requestError, 'Unable to stock out.') ?? '');
    } finally {
      setActing(false);
    }
  };

  const beginZeroConfirm = async (hit: ProductSearchHit) => {
    setActing(true);
    setError('');
    try {
      const itemId = await resolveItemId(hit);
      setPendingZero({ hit, itemId });
    } catch (requestError) {
      setError(inlineFailure(requestError, 'Unable to stock out.') ?? '');
    } finally {
      setActing(false);
    }
  };

  const pickHit = (hit: ProductSearchHit) => {
    if (acting) return;
    setPendingZero(null);
    if (direction === 'stock_in') {
      void stockInHit(hit);
      return;
    }
    if (hit.onHand === 0) {
      void beginZeroConfirm(hit);
      return;
    }
    void stockOutHit(hit, false);
  };

  const close = () => {
    if (acting) return;
    onClose();
  };

  const handleCreated = async (
    result: { itemId?: string; id: string; name: string },
    mode: AddItemSheetMode,
  ) => {
    const itemId = result.itemId;
    if (itemId === undefined || itemId === '') {
      const ensured = await ensureProductItem(result.id);
      await finishCreated(ensured.itemId, result.name, mode);
      return;
    }
    await finishCreated(itemId, result.name, mode);
  };

  const finishCreated = async (itemId: string, name: string, mode: AddItemSheetMode) => {
    if (mode === 'stock_in') {
      const instance = await addItemInstance(itemId);
      onInventoryChanged();
      showUndo({
        detail: addedOneToast(name),
        onUndo: () => {
          void removeItemInstance(instance.id).then(() => onInventoryChanged());
        },
      });
      onClose();
      return;
    }
    if (mode === 'stock_out') {
      const out = await stockOutItem(itemId, { backfill: true });
      onInventoryChanged();
      const moveId = out.backfill?.stockInMoveId ?? '';
      showUndo({
        detail: addedAndTookOutOneToast(name),
        onUndo: moveId === '' ? undefined : () => {
          void undoMove(moveId).then(() => onInventoryChanged());
        },
      });
      onClose();
    }
  };

  return (
    <>
      <Drawer.Root opened={opened} onClose={close} position="bottom" transitionProps={{ duration: 0 }}>
        <Drawer.Overlay />
        <Drawer.Content className="bin-page shelf-sheet find-item-sheet">
          <Drawer.Header component="div">
            <Drawer.Title>{FIND_ITEM_TITLE}</Drawer.Title>
            <Drawer.CloseButton aria-label="Close" />
          </Drawer.Header>
          <Drawer.Body className="shelf-sheet-body">
            <Stack gap="sm">
              <Text size="sm" c="dimmed">{modeSubtitle}</Text>
              <ServerNoticeBanner inset />
              <TextInput
                label={FIND_ITEM_INPUT_LABEL}
                placeholder={TYPE_A_NAME_PLACEHOLDER}
                value={query}
                onChange={(event) => setQuery(event.currentTarget.value)}
                autoComplete="off"
                error={shortQuery ? QUERY_TOO_SHORT : undefined}
              />
              {loading && <Loader size="sm" aria-label="Searching products" />}
              {error !== '' && <Text size="sm" c="red">{error}</Text>}
              {pendingZero !== null && direction === 'stock_out' && (
                <Stack gap="xs" className="find-item-zero-confirm" role="region" aria-label={NONE_ON_HAND_TITLE}>
                  <Text size="sm" fw={600}>{NONE_ON_HAND_TITLE}</Text>
                  <Text size="sm">{findNoneOnHandBody(pendingZero.hit.name)}</Text>
                  <Button
                    loading={acting}
                    onClick={() => void stockOutHit(pendingZero.hit, true)}
                  >
                    {ADD_IT_AND_TAKE_IT_OUT}
                  </Button>
                  <Button variant="default" disabled={acting} onClick={() => setPendingZero(null)}>
                    {BACK_BUTTON}
                  </Button>
                </Stack>
              )}
              {pendingZero === null && resultRows.length > 0 && (
                <Stack gap={4} aria-label="Search results">
                  {resultRows.map((hit) => (
                    <UnstyledButton
                      key={hit.productId}
                      className="shelf-sheet-choice find-item-hit"
                      disabled={acting}
                      onClick={() => pickHit(hit)}
                    >
                      <Group justify="space-between" wrap="nowrap" w="100%">
                        <Stack gap={0} align="flex-start">
                          <span className="shelf-sheet-choice-name">{hit.name}</span>
                          {(hit.brand !== undefined && hit.brand !== '')
                            || (hit.size !== undefined && hit.size !== '') ? (
                              <Text size="xs" c="dimmed">
                                {[hit.brand, hit.size].filter((part) => part !== undefined && part !== '').join(' · ')}
                              </Text>
                            ) : null}
                        </Stack>
                        <Badge
                          size="sm"
                          variant="light"
                          color={hit.onHand === 0 ? 'yellow' : 'gray'}
                        >
                          {onHandLabel(hit.onHand)}
                        </Badge>
                      </Group>
                    </UnstyledButton>
                  ))}
                </Stack>
              )}
              {pendingZero === null && !loading && debouncedQuery.length >= 2 && resultRows.length === 0 && error === '' && (
                <Text size="sm" c="dimmed">{nothingCalledYet(debouncedQuery)}</Text>
              )}
              {pendingZero === null && showAddNamed && (
                <Button
                  variant="default"
                  className="shelf-select"
                  disabled={acting}
                  onClick={() => setAddSheet({ mode: addMode, name: trimmedQuery })}
                >
                  {addNamedProductLabel(trimmedQuery)}
                </Button>
              )}
            </Stack>
          </Drawer.Body>
        </Drawer.Content>
      </Drawer.Root>
      {addSheet !== null && (
        <AddItemSheet
          opened
          mode={addSheet.mode}
          initialName={addSheet.name}
          onClose={() => setAddSheet(null)}
          onCreated={handleCreated}
        />
      )}
    </>
  );
}
