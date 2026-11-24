import { useEffect, useRef, useState } from 'react';
import { ActivityIndicator, Text, View, useWindowDimensions } from 'react-native';
import { useSafeAreaInsets } from 'react-native-safe-area-context';
import { ADD_IT_AND_TAKE_IT_OUT, CODE_NONE_ON_HAND, inlineFailure } from '@stockbot/shared/contract';
import {
  FIND_ITEM_DEBOUNCE_MS,
  FIND_ITEM_MIN_QUERY,
  findItemView,
  hitSubtitle,
  onHandLabel,
  runeLength,
} from '@stockbot/shared/findItem';
import {
  ADD_ITEM_TITLE,
  addedAndTookOutOneToast,
  addedOneToast,
  addNamedProductLabel,
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
} from '@stockbot/shared/noBarcodeCopy';
import type { ProductSearchHit, ProductWriteResult, ScanDirection } from '@stockbot/shared/types';
import {
  ActionButton,
  focusOnWeb,
  NoticeBanner,
  SearchResults,
  SheetFrame,
  TextField,
  type SearchResultRow,
  type TextFieldHandle,
} from '@stockbot/ui';
import { ApiError, type StockbotClient } from '../api/client';
import { colors, fonts } from '../ui/theme';
import { isDesktopLayout } from '../ui/layout';
import { AddItemForm, type AddItemSheetMode } from './AddItemForm';

export interface RecordedStock {
  text: string;
  // Present when the stock can be taken back. Rejects when it cannot.
  undo?: () => Promise<void>;
}

export interface FindItemSheetProps {
  client: StockbotClient;
  direction: ScanDirection;
  onClose: () => void;
  onInventoryChanged: () => void;
  onRecorded: (recorded: RecordedStock) => void;
}

type PendingZero = { hit: ProductSearchHit; itemId: string };

export function FindItemSheet({ client, direction, onClose, onInventoryChanged, onRecorded }: FindItemSheetProps) {
  const insets = useSafeAreaInsets();
  const { width } = useWindowDimensions();
  const searchRef = useRef<TextFieldHandle>(null);
  const zeroTitleRef = useRef<unknown>(null);
  const alive = useRef(true);
  const [query, setQuery] = useState('');
  const [debouncedQuery, setDebouncedQuery] = useState('');
  const [results, setResults] = useState<ProductSearchHit[]>([]);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState('');
  const [pendingZero, setPendingZero] = useState<PendingZero | null>(null);
  const [addSheet, setAddSheet] = useState<{ mode: AddItemSheetMode; name: string } | null>(null);
  const [acting, setActing] = useState(false);
  const [adding, setAdding] = useState(false);

  useEffect(() => {
    alive.current = true;
    return () => {
      alive.current = false;
    };
  }, []);

  useEffect(() => {
    const timer = setTimeout(() => setDebouncedQuery(query.trim()), FIND_ITEM_DEBOUNCE_MS);
    return () => clearTimeout(timer);
  }, [query]);

  useEffect(() => {
    if (runeLength(debouncedQuery) < FIND_ITEM_MIN_QUERY) return undefined;
    let active = true;
    // eslint-disable-next-line react-hooks/set-state-in-effect -- debounced fetch lifecycle
    setLoading(true);
    setError('');
    client.searchProducts(debouncedQuery).then((hits) => {
      if (active) setResults(hits);
    }).catch((requestError: unknown) => {
      if (!active) return;
      setResults([]);
      setError(inlineFailure(requestError, SEARCH_FAILURE) ?? '');
    }).finally(() => {
      if (active) setLoading(false);
    });
    return () => {
      active = false;
    };
  }, [client, debouncedQuery]);

  useEffect(() => {
    if (pendingZero === null) return undefined;
    const timer = setTimeout(() => focusOnWeb(zeroTitleRef.current), 0);
    return () => clearTimeout(timer);
  }, [pendingZero]);

  const { trimmed, tooShort, searchable, rows, showAddNamed } = findItemView(query, debouncedQuery, results);
  const addMode: AddItemSheetMode = direction === 'stock_out' ? 'stock_out' : 'stock_in';
  const modeSubtitle = direction === 'stock_in' ? SCANS_ADD_TO_STOCK : SCANS_REMOVE_FROM_STOCK;

  async function resolveItemId(hit: ProductSearchHit): Promise<string> {
    if (hit.itemId != null && hit.itemId !== '') return hit.itemId;
    return (await client.ensureProductItem(hit.productId)).itemId;
  }

  // A backfill writes a stock in and a stock out together, and removing either removes both.
  function backfillUndo(backfill: { stockInMoveId: string; stockOutMoveId: string } | undefined) {
    if (backfill === undefined || backfill.stockInMoveId === '' || backfill.stockOutMoveId === '') return undefined;
    return () => client.undoMove(backfill.stockInMoveId);
  }

  async function stockIn(itemId: string, name: string) {
    const instance = await client.addItemInstance(itemId);
    onInventoryChanged();
    onRecorded({ text: addedOneToast(name), undo: () => client.removeItemInstance(instance.id) });
    onClose();
  }

  async function stockInHit(hit: ProductSearchHit) {
    setActing(true);
    setError('');
    try {
      await stockIn(await resolveItemId(hit), hit.name);
    } catch (requestError) {
      if (alive.current) setError(inlineFailure(requestError, 'Unable to stock in.') ?? '');
    } finally {
      if (alive.current) setActing(false);
    }
  }

  async function stockOutHit(hit: ProductSearchHit, backfill = false) {
    setActing(true);
    setError('');
    try {
      const itemId = await resolveItemId(hit);
      const result = await client.stockOutItem(itemId, backfill ? { backfill: true } : undefined);
      onInventoryChanged();
      if (backfill && result.backfill !== undefined) {
        onRecorded({ text: addedAndTookOutOneToast(hit.name), undo: backfillUndo(result.backfill) });
      } else {
        const moveId = result.stockOutMoveId ?? '';
        onRecorded({
          text: tookOutOneToast(hit.name),
          undo: moveId === '' ? undefined : () => client.undoMove(moveId),
        });
      }
      setPendingZero(null);
      onClose();
    } catch (requestError) {
      if (!backfill && requestError instanceof ApiError && requestError.code === CODE_NONE_ON_HAND) {
        try {
          const itemId = await resolveItemId(hit);
          if (alive.current) setPendingZero({ hit, itemId });
        } catch (resolveError) {
          if (alive.current) setError(inlineFailure(resolveError, 'Unable to stock out.') ?? '');
        }
        return;
      }
      if (alive.current) setError(inlineFailure(requestError, 'Unable to stock out.') ?? '');
    } finally {
      if (alive.current) setActing(false);
    }
  }

  async function beginZeroConfirm(hit: ProductSearchHit) {
    setActing(true);
    setError('');
    try {
      const itemId = await resolveItemId(hit);
      if (alive.current) setPendingZero({ hit, itemId });
    } catch (requestError) {
      if (alive.current) setError(inlineFailure(requestError, 'Unable to stock out.') ?? '');
    } finally {
      if (alive.current) setActing(false);
    }
  }

  function pickHit(productId: string) {
    const hit = rows.find((row) => row.productId === productId);
    if (hit === undefined || acting) return;
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
  }

  function close() {
    if (acting || adding) return;
    if (addSheet !== null) setAddSheet(null);
    else onClose();
  }

  async function handleCreated(result: ProductWriteResult, mode: AddItemSheetMode) {
    try {
      const itemId = result.itemId !== undefined && result.itemId !== ''
        ? result.itemId
        : (await client.ensureProductItem(result.id)).itemId;
      if (mode === 'stock_in') {
        await stockIn(itemId, result.name);
      } else if (mode === 'stock_out') {
        const out = await client.stockOutItem(itemId, { backfill: true });
        onInventoryChanged();
        onRecorded({ text: addedAndTookOutOneToast(result.name), undo: backfillUndo(out.backfill) });
        onClose();
      }
    } catch (requestError) {
      if (alive.current) setError(inlineFailure(requestError, 'Unable to add item.') ?? '');
    }
  }

  const resultRows: SearchResultRow[] = rows.map((hit) => ({
    id: hit.productId,
    title: hit.name,
    detail: hitSubtitle(hit) || undefined,
    badge: onHandLabel(hit.onHand),
    badgeWarn: hit.onHand === 0,
  }));
  const spinning = loading && searchable;
  const noMatches = pendingZero === null && !spinning && searchable && rows.length === 0 && error === '';

  const findBody = (
    <>
        <Text style={{ fontFamily: fonts.regular, fontSize: 16, lineHeight: 22, color: colors.inkMuted }}>{modeSubtitle}</Text>
        <TextField
          ref={searchRef}
          fieldId="find-item-search"
          label={FIND_ITEM_INPUT_LABEL}
          placeholder={TYPE_A_NAME_PLACEHOLDER}
          value={query}
          error={tooShort ? QUERY_TOO_SHORT : undefined}
          autoComplete="off"
          returnKeyType="search"
          fontRegular={fonts.regular}
          fontBold={fonts.bold}
          onChangeText={setQuery}
        />
        {spinning ? (
          <ActivityIndicator accessibilityLabel="Searching products" color={colors.shelfDeep} size="small" style={{ alignSelf: 'flex-start' }} />
        ) : null}
        {error !== '' ? (
          <Text role="alert" accessibilityRole="alert" style={{ fontFamily: fonts.bold, fontSize: 16, lineHeight: 22, color: colors.danger }}>
            {error}
          </Text>
        ) : null}
        {pendingZero !== null && direction === 'stock_out' ? (
          <View role="region" aria-label={NONE_ON_HAND_TITLE} accessibilityLabel={NONE_ON_HAND_TITLE} style={{ gap: 12 }}>
            <Text
              ref={zeroTitleRef as never}
              {...{ tabIndex: -1 }}
              accessibilityRole="header"
              aria-level={3}
              style={{ fontFamily: fonts.bold, fontSize: 20, lineHeight: 26, color: colors.ink }}
            >
              {NONE_ON_HAND_TITLE}
            </Text>
            <Text style={{ fontFamily: fonts.regular, fontSize: 16, lineHeight: 22, color: colors.ink }}>
              {findNoneOnHandBody(pendingZero.hit.name)}
            </Text>
            <ActionButton
              label={ADD_IT_AND_TAKE_IT_OUT}
              busy={acting}
              fontBold={fonts.bold}
              onPress={() => { void stockOutHit(pendingZero.hit, true); }}
            />
            <ActionButton
              label={BACK_BUTTON}
              variant="secondary"
              disabled={acting}
              fontBold={fonts.bold}
              onPress={() => {
                setPendingZero(null);
                setTimeout(() => focusOnWeb(searchRef.current), 0);
              }}
            />
          </View>
        ) : null}
        {pendingZero === null && resultRows.length > 0 ? (
          <SearchResults
            label="Search results"
            rows={resultRows}
            disabled={acting}
            fontRegular={fonts.regular}
            fontBold={fonts.bold}
            onPick={pickHit}
          />
        ) : null}
        {noMatches ? (
          <Text style={{ fontFamily: fonts.regular, fontSize: 16, lineHeight: 22, color: colors.inkMuted }}>
            {nothingCalledYet(debouncedQuery)}
          </Text>
        ) : null}
        {pendingZero === null && showAddNamed ? (
          <ActionButton
            label={addNamedProductLabel(trimmed)}
            variant="secondary"
            disabled={acting}
            fontBold={fonts.bold}
            onPress={() => setAddSheet({ mode: addMode, name: trimmed })}
          />
        ) : null}
    </>
  );

  return (
    <SheetFrame
      title={addSheet === null ? FIND_ITEM_TITLE : ADD_ITEM_TITLE}
      dialog={isDesktopLayout(width)}
      topInset={insets.top}
      bottomInset={insets.bottom}
      fontBold={fonts.bold}
      closeDisabled={acting || adding}
      banner={<NoticeBanner fontRegular={fonts.regular} fontBold={fonts.bold} />}
      onClose={close}
    >
      {addSheet === null ? findBody : (
        <AddItemForm
          client={client}
          mode={addSheet.mode}
          initialName={addSheet.name}
          onClose={() => setAddSheet(null)}
          onBusyChange={setAdding}
          onCreated={handleCreated}
        />
      )}
    </SheetFrame>
  );
}
