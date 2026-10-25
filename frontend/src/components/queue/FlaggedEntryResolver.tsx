import { useEffect, useMemo, useState } from 'react';
import {
  Alert,
  Button,
  Combobox,
  Fieldset,
  Stack,
  TextInput,
  useCombobox,
} from '@mantine/core';
import {
  ApiError,
  createProduct,
  createProductOverride,
  listProducts,
  updateScanEntry,
} from '../../api/client';
import { CODE_LINK_TARGET_GONE } from '@stockbot/shared/contract';
import type { Product, ProductWriteInput, ScanEntry } from '@stockbot/shared/types';
import { ContributeFields, type ContributeChoice } from '../product/ContributeFields';
import { NetSizeFields } from '../product/NetSizeFields';
import { emptyNetSizeDraft, sizeWrite, type NetSizeDraft } from '../product/netSize';
import { showShareNotice } from '../product/shareNotice';
import { inlineFailure } from '@stockbot/shared/contract';

export interface FlaggedEntryResolverProps {
  entry: ScanEntry;
  onResolved: (entry: ScanEntry) => void;
}

type ProductDraft = Pick<Product, 'name' | 'category'> & { unitOfMeasure: string };

const productLabel = (product: Product): string => `${product.name} — ${product.category}`;

const findCreatedProduct = (products: Product[], draft: ProductDraft): Product | undefined =>
  products
    .filter(
      (product) =>
        product.name === draft.name &&
        product.category === draft.category &&
        product.unitOfMeasure === draft.unitOfMeasure,
    )
    .sort((left, right) => right.createdAt.localeCompare(left.createdAt))[0];

export const FlaggedEntryResolver = ({ entry, onResolved }: FlaggedEntryResolverProps) => {
  const combobox = useCombobox({
    onDropdownClose: () => combobox.resetSelectedOption(),
  });
  const [products, setProducts] = useState<Product[]>([]);
  const [productsLoading, setProductsLoading] = useState(true);
  const [query, setQuery] = useState('');
  const [selectedProductId, setSelectedProductId] = useState('');
  const [draft, setDraft] = useState<ProductDraft>({ name: '', category: '', unitOfMeasure: '' });
  const [size, setSize] = useState<NetSizeDraft>(emptyNetSizeDraft());
  const [sizeTouched, setSizeTouched] = useState(false);
  const [packTouched, setPackTouched] = useState(false);
  const [share, setShare] = useState<ContributeChoice>({
    contribute: false,
    contributeTo: 'openfoodfacts',
  });
  const [error, setError] = useState('');
  const [submitting, setSubmitting] = useState(false);

  useEffect(() => {
    let active = true;
    void listProducts()
      .then((loadedProducts) => {
        if (active) setProducts(loadedProducts);
      })
      .catch((requestError: unknown) => {
        if (active) {
          setError(inlineFailure(requestError, 'Unable to load products.') ?? '');
        }
      })
      .finally(() => {
        if (active) setProductsLoading(false);
      });
    return () => {
      active = false;
    };
  }, []);

  const matchingProducts = useMemo(() => {
    const normalizedQuery = query.trim().toLocaleLowerCase();
    if (normalizedQuery === '') return products;
    return products.filter((product) =>
      `${product.name} ${product.category}`.toLocaleLowerCase().includes(normalizedQuery),
    );
  }, [products, query]);

  const selectProduct = (productId: string) => {
    const selectedProduct = products.find((product) => product.id === productId);
    if (selectedProduct === undefined) return;
    setSelectedProductId(productId);
    setQuery(productLabel(selectedProduct));
    combobox.closeDropdown();
  };

  const updateQuery = (nextQuery: string) => {
    setQuery(nextQuery);
    const selectedProduct = products.find((product) => product.id === selectedProductId);
    if (selectedProduct !== undefined && nextQuery !== productLabel(selectedProduct)) {
      setSelectedProductId('');
    }
    combobox.openDropdown();
    combobox.updateSelectedOptionIndex();
  };

  const resolveWithProduct = async (productId: string) => {
    try {
      const resolvedEntry = await updateScanEntry(entry.id, { productId, status: 'pending' });
      await createProductOverride({ barcode: entry.barcode, productId });
      onResolved(resolvedEntry);
    } catch (requestError) {
      if (requestError instanceof ApiError && requestError.code === CODE_LINK_TARGET_GONE) {
        setProducts((current) => current.filter((product) => product.id !== productId));
        setSelectedProductId('');
        setQuery('');
      }
      throw requestError;
    }
  };

  const handleResolve = async () => {
    if (selectedProductId === '') return;
    setSubmitting(true);
    setError('');
    try {
      await resolveWithProduct(selectedProductId);
    } catch (requestError) {
      setError(inlineFailure(requestError, 'Unable to resolve scan.') ?? '');
    } finally {
      setSubmitting(false);
    }
  };

  const handleCreate = async () => {
    if (draft.name.trim() === '') return;
    setSubmitting(true);
    setError('');
    try {
      const written = sizeWrite(
        { ...size, packageWord: size.packageWord || draft.unitOfMeasure },
        { size: sizeTouched, pack: packTouched },
      );
      const input: ProductWriteInput = {
        name: draft.name,
        category: draft.category,
        unitOfMeasure: written.unitOfMeasure,
        netAmount: written.netAmount,
        netUnit: written.netUnit,
        packCount: written.packCount,
        barcode: entry.barcode,
      };
      if (share.contribute) {
        input.contribute = true;
        input.contributeTo = share.contributeTo;
      }
      const created = await createProduct(input);
      showShareNotice(created);
      const product = created.id === '' ? findCreatedProduct(await listProducts(), draft) : created;
      if (product === undefined) throw new Error('Created product could not be loaded.');
      await resolveWithProduct(product.id);
    } catch (requestError) {
      setError(inlineFailure(requestError, 'Unable to create product.') ?? '');
    } finally {
      setSubmitting(false);
    }
  };

  const productOptions = matchingProducts.map((product) => (
    <Combobox.Option
      key={product.id}
      value={product.id}
      active={product.id === selectedProductId}
    >
      {productLabel(product)}
    </Combobox.Option>
  ));

  return (
    <Stack gap="xs">
      <Combobox store={combobox} onOptionSubmit={selectProduct} withinPortal={false}>
        <Combobox.Target withExpandedAttribute>
          <TextInput
            size="xs"
            label="Search products"
            placeholder="Type a product name or category"
            value={query}
            autoComplete="off"
            role="combobox"
            aria-autocomplete="list"
            aria-expanded={combobox.dropdownOpened}
            aria-controls={combobox.listId ?? undefined}
            rightSection={<Combobox.Chevron />}
            rightSectionPointerEvents="none"
            onChange={(event) => updateQuery(event.currentTarget.value)}
            onClick={() => combobox.openDropdown()}
            onFocus={() => combobox.openDropdown()}
            onBlur={() => combobox.closeDropdown()}
          />
        </Combobox.Target>
        <Combobox.Dropdown>
          <Combobox.Options>
            {productsLoading && <Combobox.Empty>Loading products…</Combobox.Empty>}
            {!productsLoading && productOptions.length === 0 && (
              <Combobox.Empty>No matching products.</Combobox.Empty>
            )}
            {!productsLoading && productOptions}
          </Combobox.Options>
        </Combobox.Dropdown>
      </Combobox>
      <Button
        size="xs"
        variant="light"
        disabled={selectedProductId === ''}
        loading={submitting}
        onClick={() => void handleResolve()}
      >
        Use selected product
      </Button>
      <Fieldset legend="Create new product" p="xs">
        <Stack gap="xs">
          <TextInput
            size="xs"
            label="Product name"
            required
            value={draft.name}
            onChange={(event) => setDraft({ ...draft, name: event.currentTarget.value })}
          />
          <TextInput
            size="xs"
            label="Category"
            value={draft.category}
            onChange={(event) => setDraft({ ...draft, category: event.currentTarget.value })}
          />
          <NetSizeFields
            draft={{ ...size, packageWord: size.packageWord }}
            onChange={(next) => {
              if (next.amount !== size.amount || next.unit !== size.unit) setSizeTouched(true);
              if (next.packCount !== size.packCount) setPackTouched(true);
              setSize(next);
              setDraft({ ...draft, unitOfMeasure: next.packageWord });
            }}
          />
          <ContributeFields onChange={setShare} />
          <Button
            size="xs"
            disabled={draft.name.trim() === ''}
            loading={submitting}
            onClick={() => void handleCreate()}
          >
            Create and use product
          </Button>
        </Stack>
      </Fieldset>
      {error !== '' && (
        <Alert color="red" py="xs">
          {error}
        </Alert>
      )}
    </Stack>
  );
};
