import { useCallback, useEffect, useRef, useState } from 'react';
import {
  ActivityIndicator,
  Platform,
  Pressable,
  StyleSheet,
  Text,
  View,
  useWindowDimensions,
} from 'react-native';
import { useSafeAreaInsets } from 'react-native-safe-area-context';
import { inlineFailure, LINK_EXISTING_SCAN_FAILURE, CODE_LINK_TARGET_GONE } from '@stockbot/shared/contract';
import {
  emptyProductDraft,
  hasDraftErrors,
  productDraftInput,
  RESOLVER_CREATE_FAILURE,
  RESOLVER_CREATE_LABEL,
  RESOLVER_CREATE_LEGEND,
  RESOLVER_LINK_LEGEND,
  RESOLVER_LOAD_FAILURE,
  RESOLVER_NO_MATCH,
  RESOLVER_SEARCH_HINT,
  RESOLVER_SEARCH_LABEL,
  RESOLVER_SEARCH_PLACEHOLDER,
  RESOLVER_SEARCHING,
  RESOLVER_TITLE,
  RESOLVER_USE_LABEL,
  shareNoticeFor,
  validateProductDraft,
  type ProductDraft,
} from '@stockbot/shared/productForm';
import type { ProductSearchHit, ScanEntry } from '@stockbot/shared/types';
import { ProductFormFields, ProvenanceBadge, SheetFrame, TextField, type TextFieldHandle } from '@stockbot/ui';
import { ApiError, type StockbotClient } from '../api/client';
import { colors, fonts } from '../ui/theme';
import { DESKTOP_MIN_WIDTH } from '../ui/layout';
import { useContribution } from './useContribution';

const SEARCH_DELAY_MS = 250;
const SEARCH_LIMIT = 20;
const MIN_QUERY = 2;

export interface ResolveSheetProps {
  client: StockbotClient;
  entry: ScanEntry;
  onClose: () => void;
  // The scan is pending now. notice is the sharing receipt sentence, when the server sent one.
  onResolved: (resolved: ScanEntry, notice: string) => void;
}

function hitLabel(hit: ProductSearchHit): string {
  const brand = hit.brand ? ` (${hit.brand})` : '';
  return `${hit.name}${brand}`;
}

function hitDetail(hit: ProductSearchHit): string {
  const parts = [hit.size, hit.category].filter((part): part is string => part !== undefined && part !== '');
  parts.push(hit.itemId === null ? 'Not on your shelf yet' : `${hit.onHand} on hand`);
  return parts.join('  ');
}

export function ResolveSheet({ client, entry, onClose, onResolved }: ResolveSheetProps) {
  const insets = useSafeAreaInsets();
  const { width } = useWindowDimensions();
  const dialog = Platform.OS === 'web' && width >= DESKTOP_MIN_WIDTH;
  const contribution = useContribution(client);
  const nameRef = useRef<TextFieldHandle>(null);
  const nameEdited = useRef(false);
  const created = useRef<{ key: string; productId: string } | null>(null);
  const alive = useRef(true);

  const [draft, setDraft] = useState<ProductDraft>(() => emptyProductDraft());
  const [attempted, setAttempted] = useState(false);
  const [lookedUp, setLookedUp] = useState<ScanEntry['product'] | null>(null);
  const [query, setQuery] = useState('');
  const [hits, setHits] = useState<ProductSearchHit[]>([]);
  const [searching, setSearching] = useState(false);
  const [searched, setSearched] = useState('');
  const [searchError, setSearchError] = useState('');
  const [selectedId, setSelectedId] = useState('');
  const [submitting, setSubmitting] = useState<'link' | 'create' | ''>('');
  const [error, setError] = useState('');

  useEffect(() => {
    alive.current = true;
    return () => {
      alive.current = false;
    };
  }, []);

  useEffect(() => {
    let active = true;
    client.lookupProduct(entry.barcode).then((result) => {
      if (!active || result.product === null) return;
      setLookedUp(result.product);
      const found = result.product.name.trim();
      if (found !== '' && !nameEdited.current) setDraft((current) => ({ ...current, name: found }));
    }).catch(() => undefined);
    return () => {
      active = false;
    };
  }, [client, entry.barcode]);

  useEffect(() => {
    const text = query.trim();
    if (text.length < MIN_QUERY) return;
    let active = true;
    const timer = setTimeout(() => {
      setSearching(true);
      client.searchProducts(text, SEARCH_LIMIT).then((found) => {
        if (!active) return;
        setHits(found);
        setSearchError('');
      }).catch((requestError: unknown) => {
        if (!active) return;
        setHits([]);
        setSearchError(inlineFailure(requestError, RESOLVER_LOAD_FAILURE) ?? '');
      }).finally(() => {
        if (!active) return;
        setSearching(false);
        setSearched(text);
      });
    }, SEARCH_DELAY_MS);
    return () => {
      active = false;
      clearTimeout(timer);
    };
  }, [client, query]);

  const changeDraft = useCallback((next: ProductDraft) => {
    if (next.name !== draft.name) nameEdited.current = true;
    setDraft(next);
  }, [draft.name]);

  // The scan is patched with the product and its status together, then the
  // barcode override is written. A failure on the PATCH leaves the scan flagged.
  async function resolveWith(productId: string): Promise<ScanEntry> {
    const resolved = await client.updateScan(entry.id, { productId, status: 'pending' });
    try {
      await client.createProductOverride(entry.barcode, productId);
    } catch {
      // The PATCH already linked the barcode in the same transaction, so the
      // scan is resolved. A failed second write is not worth undoing it.
    }
    return resolved;
  }

  async function linkSelected() {
    if (selectedId === '' || submitting !== '') return;
    setSubmitting('link');
    setError('');
    try {
      const resolved = await resolveWith(selectedId);
      if (alive.current) onResolved(resolved, '');
    } catch (requestError) {
      if (!alive.current) return;
      if (requestError instanceof ApiError && requestError.code === CODE_LINK_TARGET_GONE) {
        setHits((current) => current.filter((hit) => hit.productId !== selectedId));
        setSelectedId('');
      }
      setError(requestError instanceof ApiError
        ? (requestError.code === CODE_LINK_TARGET_GONE || requestError.code === 'conflicting_fields'
          ? inlineFailure(requestError, LINK_EXISTING_SCAN_FAILURE) ?? ''
          : LINK_EXISTING_SCAN_FAILURE)
        : inlineFailure(requestError, LINK_EXISTING_SCAN_FAILURE) ?? '');
    } finally {
      if (alive.current) setSubmitting('');
    }
  }

  async function createAndUse() {
    if (submitting !== '') return;
    const problems = validateProductDraft(draft);
    setAttempted(true);
    if (hasDraftErrors(problems)) {
      setError('');
      if (problems.name !== undefined) setTimeout(() => nameRef.current?.focus(), 0);
      return;
    }
    setSubmitting('create');
    setError('');
    try {
      const input = productDraftInput(draft, entry.barcode);
      const key = JSON.stringify(input);
      let productId = created.current?.key === key ? created.current.productId : '';
      let notice = '';
      if (productId === '') {
        const product = await client.createProduct(input);
        if (product.id === '') throw new Error('Created product could not be loaded.');
        productId = product.id;
        created.current = { key, productId };
        notice = shareNoticeFor(product)?.message ?? '';
      }
      const resolved = await resolveWith(productId);
      if (alive.current) onResolved(resolved, notice);
    } catch (requestError) {
      if (alive.current) setError(inlineFailure(requestError, RESOLVER_CREATE_FAILURE) ?? '');
    } finally {
      if (alive.current) setSubmitting('');
    }
  }

  const errors = attempted ? validateProductDraft(draft) : {};
  const searchable = query.trim().length >= MIN_QUERY;
  const shownHits = searchable ? hits : [];
  const busy = submitting !== '';
  const upstream = lookedUp?.externalSource;

  return (
    <SheetFrame
      title={RESOLVER_TITLE}
      dialog={dialog}
      topInset={insets.top}
      bottomInset={insets.bottom}
      fontBold={fonts.bold}
      contentGap={28}
      onClose={onClose}
    >
        <View style={styles.barcodeRow}>
        <Text style={styles.barcode} selectable>{`Barcode ${entry.barcode}`}</Text>
        <ProvenanceBadge externalSource={upstream} fontRegular={fonts.regular} fontBold={fonts.bold} />
      </View>

      <View role="group" aria-label={RESOLVER_LINK_LEGEND} style={styles.section}>
        <TextField
          fieldId="resolve-search"
          label={RESOLVER_SEARCH_LABEL}
          placeholder={RESOLVER_SEARCH_PLACEHOLDER}
          value={query}
          disabled={busy}
          fontRegular={fonts.regular}
          fontBold={fonts.bold}
          onChangeText={(next) => {
            setQuery(next);
            setSelectedId('');
          }}
        />
        {searchError !== '' ? <Text role="alert" accessibilityRole="alert" style={styles.error}>{searchError}</Text> : null}
        {!searchable ? (
          <Text style={styles.muted}>{RESOLVER_SEARCH_HINT}</Text>
        ) : searching ? (
          <View style={styles.searching}>
            <ActivityIndicator accessible={false} color={colors.shelfDeep} size="small" />
            <Text style={styles.muted}>{RESOLVER_SEARCHING}</Text>
          </View>
        ) : shownHits.length === 0 && searchError === '' && searched === query.trim() ? (
          <Text style={styles.muted}>{RESOLVER_NO_MATCH}</Text>
        ) : null}
        <View role="radiogroup" aria-label="Matching products" accessibilityLabel="Matching products">
          {shownHits.map((hit) => {
            const checked = hit.productId === selectedId;
            return (
              <Pressable
                key={hit.productId}
                testID={`resolve-hit-${hit.productId}`}
                role="radio"
                aria-checked={checked}
                accessibilityRole="radio"
                accessibilityState={{ checked, disabled: busy }}
                accessibilityLabel={hitLabel(hit)}
                disabled={busy}
                onPress={() => setSelectedId(hit.productId)}
                style={[styles.hit, checked && styles.hitChecked]}
              >
                <View style={styles.hitCopy}>
                  <Text style={styles.hitName}>{hitLabel(hit)}</Text>
                  <Text style={styles.muted}>{hitDetail(hit)}</Text>
                </View>
              </Pressable>
            );
          })}
        </View>
        <Pressable
          accessibilityRole="button"
          accessibilityLabel={RESOLVER_USE_LABEL}
          accessibilityState={{ disabled: selectedId === '' || busy }}
          disabled={selectedId === '' || busy}
          onPress={() => { void linkSelected(); }}
          style={[styles.secondary, (selectedId === '' || busy) && styles.dim]}
        >
          <Text style={styles.secondaryText}>{submitting === 'link' ? 'Using' : RESOLVER_USE_LABEL}</Text>
        </Pressable>
      </View>

      <View role="group" aria-label={RESOLVER_CREATE_LEGEND} style={styles.section}>
        <Text accessibilityRole="header" aria-level={3} style={styles.legend}>{RESOLVER_CREATE_LEGEND}</Text>
        <ProductFormFields
          ref={nameRef}
          idPrefix="resolve"
          draft={draft}
          errors={errors}
          disabled={busy}
          fontRegular={fonts.regular}
          fontBold={fonts.bold}
          onChange={changeDraft}
          contribute={{
            enabled: contribution.enabled,
            configured: contribution.configured,
            error: contribution.error,
            allowProductOptIn: upstream === undefined,
            onToggleEnabled: (next) => {
              void contribution.toggle(next).then((saved) => {
                if (!saved) setDraft((current) => ({ ...current, share: { ...current.share, contribute: false } }));
              });
            },
          }}
        />
        <Pressable
          accessibilityRole="button"
          accessibilityLabel={RESOLVER_CREATE_LABEL}
          accessibilityState={{ disabled: busy }}
          disabled={busy}
          onPress={() => { void createAndUse(); }}
          style={[styles.primary, busy && styles.dim]}
        >
          <Text style={styles.primaryText}>{submitting === 'create' ? 'Creating' : RESOLVER_CREATE_LABEL}</Text>
        </Pressable>
      </View>
      {error !== '' ? <Text role="alert" accessibilityRole="alert" style={styles.error}>{error}</Text> : null}
    </SheetFrame>
  );
}

const styles = StyleSheet.create({
  barcodeRow: { gap: 6 },
  barcode: { fontFamily: fonts.regular, fontSize: 16, lineHeight: 22, color: colors.inkMuted },
  section: { gap: 12 },
  legend: { fontFamily: fonts.bold, fontSize: 20, lineHeight: 26, color: colors.ink },
  muted: { fontFamily: fonts.regular, fontSize: 15, lineHeight: 21, color: colors.inkMuted },
  searching: { flexDirection: 'row', alignItems: 'center', gap: 8 },
  hit: {
    minHeight: 56,
    justifyContent: 'center',
    paddingVertical: 8,
    paddingHorizontal: 10,
    borderLeftWidth: 4,
    borderLeftColor: colors.rule,
    borderBottomWidth: 1,
    borderBottomColor: colors.rule,
    backgroundColor: colors.white,
  },
  hitChecked: { borderLeftColor: colors.shelfDeep, backgroundColor: colors.successWash },
  hitCopy: { gap: 2 },
  hitName: { fontFamily: fonts.bold, fontSize: 17, lineHeight: 23, color: colors.ink },
  secondary: {
    minHeight: 48,
    alignItems: 'center',
    justifyContent: 'center',
    borderWidth: 2,
    borderColor: colors.shelfDeep,
    paddingHorizontal: 16,
  },
  secondaryText: { fontFamily: fonts.bold, fontSize: 17, color: colors.shelfDeep },
  primary: {
    minHeight: 52,
    alignItems: 'center',
    justifyContent: 'center',
    backgroundColor: colors.shelfDeep,
    paddingHorizontal: 16,
  },
  primaryText: { fontFamily: fonts.bold, fontSize: 18, color: colors.white },
  dim: { opacity: 0.5 },
  error: { fontFamily: fonts.bold, fontSize: 16, lineHeight: 22, color: colors.danger },
});
