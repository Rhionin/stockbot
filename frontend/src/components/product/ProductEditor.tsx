import { useEffect, useRef, useState } from 'react';
import { Alert, Anchor, Button, Stack, Text, TextInput } from '@mantine/core';
import { getProduct, updateProduct } from '../../api/client';
import type { ProductDetail, ProductWriteInput } from '@stockbot/shared/types';
import { ContributeFields, type ContributeChoice } from './ContributeFields';
import { NetSizeFields } from './NetSizeFields';
import { emptyNetSizeDraft, sizeWrite, type NetSizeDraft } from './netSize';
import { showShareNotice } from './shareNotice';
import { SupplyOverride } from './SupplyOverride';
import { inlineFailure } from '@stockbot/shared/contract';

export interface ProductEditorProps {
  productId: string;
  onSaved: () => void;
  group?: { id: string; name: string } | null;
}

const emptyChoice: ContributeChoice = {
  contribute: false,
  contributeTo: 'openfoodfacts',
};

export const ProductEditor = ({ productId, onSaved, group = null }: ProductEditorProps) => {
  const [open, setOpen] = useState(false);
  const [product, setProduct] = useState<ProductDetail | null>(null);
  const [name, setName] = useState('');
  const [category, setCategory] = useState('');
  const [size, setSize] = useState<NetSizeDraft>(emptyNetSizeDraft);
  const [sizeTouched, setSizeTouched] = useState(false);
  const [packTouched, setPackTouched] = useState(false);
  const [barcode, setBarcode] = useState('');
  const [share, setShare] = useState<ContributeChoice>(emptyChoice);
  const [error, setError] = useState('');
  const [saving, setSaving] = useState(false);
  const [supplyOpen, setSupplyOpen] = useState(false);
  // A slow product read must not replace text the person has already typed.
  const draftDirty = useRef(false);

  useEffect(() => {
    if (!open) {
      draftDirty.current = false;
      return;
    }
    let active = true;
    void getProduct(productId)
      .then((loaded) => {
        if (!active || draftDirty.current) return;
        setProduct(loaded);
        setName(loaded.name);
        setCategory(loaded.category);
        setSize({
          amount: loaded.netAmount === undefined ? '' : String(loaded.netAmount),
          unit: loaded.netUnit ?? '',
          packageWord: loaded.unitOfMeasure,
          packCount: loaded.packCount === undefined ? '' : String(loaded.packCount),
        });
        setSizeTouched(false);
        setPackTouched(false);
        setBarcode(loaded.barcodes[0] ?? '');
      })
      .catch((requestError: unknown) => {
        if (active) {
          setError(inlineFailure(requestError, 'Unable to load product.') ?? '');
        }
      });
    return () => {
      active = false;
    };
  }, [open, productId]);

  if (!open) {
    return (
      <Button size="xs" variant="light" onClick={() => setOpen(true)}>
        Edit product
      </Button>
    );
  }

  const upstream = product?.source === 'external' || Boolean(product?.externalSource);

  const save = async () => {
    if (name.trim() === '') return;
    setSaving(true);
    setError('');
    const input: ProductWriteInput = {
      name: name.trim(),
      category,
      ...sizeWrite(size, { size: sizeTouched, pack: packTouched }),
    };
    if (share.contribute) {
      input.contribute = true;
      input.contributeTo = share.contributeTo;
      input.barcode = barcode.trim();
    }
    try {
      const saved = await updateProduct(productId, input);
      showShareNotice(saved);
      onSaved();
      setOpen(false);
    } catch (requestError) {
      setError(inlineFailure(requestError, 'Unable to save product.') ?? '');
    } finally {
      setSaving(false);
    }
  };

  return (
    <Stack gap="xs">
      <TextInput
        label="Product name"
        required
        value={name}
        onChange={(event) => {
          draftDirty.current = true;
          setName(event.currentTarget.value);
        }}
      />
      <TextInput
        label="Category"
        value={category}
        onChange={(event) => {
          draftDirty.current = true;
          setCategory(event.currentTarget.value);
        }}
      />
      <NetSizeFields
        draft={size}
        onChange={(next) => {
          draftDirty.current = true;
          if (next.amount !== size.amount || next.unit !== size.unit) setSizeTouched(true);
          if (next.packCount !== size.packCount) setPackTouched(true);
          setSize(next);
        }}
      />
      {group ? (
        <Text size="sm">
          This product is in <Anchor href={`/groups/${group.id}`}>{group.name}</Anchor>. Change what the group keeps on hand.
        </Text>
      ) : (
        <details
          onToggle={(event) => setSupplyOpen(event.currentTarget.open)}
        >
          <summary>Supply</summary>
          {supplyOpen && <SupplyOverride productId={productId} />}
        </details>
      )}
      <ContributeFields allowProductOptIn={!upstream} onChange={setShare} />
      {share.contribute && (
        <TextInput
          label="Barcode"
          description="Digits printed on the package. Required only when contributing this product."
          value={barcode}
          onChange={(event) => {
            draftDirty.current = true;
            setBarcode(event.currentTarget.value);
          }}
        />
      )}
      <Button size="xs" disabled={name.trim() === ''} loading={saving} onClick={() => void save()}>
        Save product
      </Button>
      {error !== '' && (
        <Alert color="red" py="xs">
          {error}
        </Alert>
      )}
    </Stack>
  );
};
