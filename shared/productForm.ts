import { emptyNetSizeDraft, sizeWrite, sizeUnits, type NetSizeDraft } from './netSize';
import type { ExternalSource, ProductWriteInput, ProductWriteResult } from './types';

export interface ContributeChoice {
  contribute: boolean;
  contributeTo: ExternalSource;
}

export const emptyContributeChoice = (): ContributeChoice => ({
  contribute: false,
  contributeTo: 'openfoodfacts',
});

export const PRODUCT_NAME_LABEL = 'Product name';
export const PRODUCT_BRAND_LABEL = 'Brand';
export const NET_SIZE_AMOUNT_LABEL = 'Size';
export const NET_SIZE_UNIT_LABEL = 'Unit';
export const NET_SIZE_PACKAGE_LABEL = 'Package';
export const NET_SIZE_PACKAGE_HINT = 'The word on the shelf, such as can or jar.';
export const NET_SIZE_PACK_COUNT_LABEL = 'Pack count';
export const NET_SIZE_PACK_COUNT_HINT =
  "How many individual units one scan of this barcode adds. Leave blank if you don't know yet.";

export const CONTRIBUTE_SWITCH_LABEL = 'Let me contribute products I type in';
export const CONTRIBUTE_OFF_NOTE =
  'Off by default. Turning this on does not send anything until you also check Contribute on a product.';
export const CONTRIBUTE_CONFIGURED_NOTE = 'A product is sent only when you check Contribute on that product.';
export const CONTRIBUTE_UNCONFIGURED_NOTE =
  'This Stockbot is not signed in to the open databases. A product you choose to contribute is saved here and is not sent.';
export const CONTRIBUTE_CHECKBOX_LABEL = 'Contribute this product';
export const CONTRIBUTE_DATABASE_LABEL = 'Open database';
export const CONTRIBUTE_UPSTREAM_NOTE =
  'This product already comes from an open database, so edits stay in your pantry.';
export const CONTRIBUTE_SAVE_FAILURE = 'Unable to save sharing setting.';

export const RESOLVER_TITLE = 'What is it?';
export const RESOLVER_SEARCH_LABEL = 'Search products';
export const RESOLVER_SEARCH_PLACEHOLDER = 'Type a product name or category';
export const RESOLVER_USE_LABEL = 'Use selected product';
export const RESOLVER_CREATE_LEGEND = 'Create new product';
export const RESOLVER_CREATE_LABEL = 'Create and use product';
export const RESOLVER_LOAD_FAILURE = 'Unable to load products.';
export const RESOLVER_CREATE_FAILURE = 'Unable to create product.';
export const RESOLVER_NO_MATCH = 'No matching products.';
export const RESOLVER_SEARCHING = 'Searching…';
export const RESOLVER_SEARCH_HINT = 'Type at least 2 letters.';

export const PRODUCT_NAME_REQUIRED = 'Enter a name for this product.';
export const NET_SIZE_PAIR_REQUIRED = 'Enter a size and a unit together.';
export const NET_SIZE_POSITIVE = 'Size has to be greater than zero.';
export const PACK_COUNT_MINIMUM = 'Pack count has to be at least 1.';
export const PACK_COUNT_WHOLE = 'Pack count has to be a whole number.';

export interface ProductDraft {
  name: string;
  brand: string;
  size: NetSizeDraft;
  sizeTouched: boolean;
  packTouched: boolean;
  share: ContributeChoice;
}

export const emptyProductDraft = (name = ''): ProductDraft => ({
  name,
  brand: '',
  size: emptyNetSizeDraft(),
  sizeTouched: false,
  packTouched: false,
  share: emptyContributeChoice(),
});

// A size or pack edit marks the part the person changed, so an untouched part
// is never written back (the same rule the web form follows).
export const withSize = (draft: ProductDraft, next: NetSizeDraft): ProductDraft => ({
  ...draft,
  size: next,
  sizeTouched: draft.sizeTouched || next.amount !== draft.size.amount || next.unit !== draft.size.unit,
  packTouched: draft.packTouched || next.packCount !== draft.size.packCount,
});

export interface ProductDraftErrors {
  name?: string;
  size?: string;
  packCount?: string;
}

const DECIMAL = /^(\d+(\.\d*)?|\.\d+)$/;
const WHOLE = /^\d+$/;

export function validateProductDraft(draft: ProductDraft): ProductDraftErrors {
  const errors: ProductDraftErrors = {};
  if (draft.name.trim() === '') errors.name = PRODUCT_NAME_REQUIRED;
  if (draft.sizeTouched) {
    const amount = draft.size.amount.trim();
    const unit = draft.size.unit;
    if ((amount === '') !== (unit === '')) errors.size = NET_SIZE_PAIR_REQUIRED;
    else if (amount !== '' && (!DECIMAL.test(amount) || !(Number(amount) > 0))) errors.size = NET_SIZE_POSITIVE;
    else if (unit !== '' && !sizeUnits.some((known) => known.value === unit)) errors.size = NET_SIZE_PAIR_REQUIRED;
  }
  if (draft.packTouched) {
    const count = draft.size.packCount.trim();
    if (count !== '') {
      if (!WHOLE.test(count)) errors.packCount = PACK_COUNT_WHOLE;
      else if (Number(count) < 1) errors.packCount = PACK_COUNT_MINIMUM;
    }
  }
  return errors;
}

export const hasDraftErrors = (errors: ProductDraftErrors): boolean =>
  errors.name !== undefined || errors.size !== undefined || errors.packCount !== undefined;

// The body POST /api/products takes for a flagged scan. Same keys the web
// resolver sends; brand is left out when blank.
export function productDraftInput(draft: ProductDraft, barcode: string): ProductWriteInput {
  const written = sizeWrite(draft.size, { size: draft.sizeTouched, pack: draft.packTouched });
  const input: ProductWriteInput = {
    name: draft.name.trim(),
    category: '',
    unitOfMeasure: written.unitOfMeasure,
    netAmount: written.netAmount,
    netUnit: written.netUnit,
    packCount: written.packCount,
    barcode,
  };
  const brand = draft.brand.trim();
  if (brand !== '') input.brand = brand;
  if (draft.share.contribute) {
    input.contribute = true;
    input.contributeTo = draft.share.contributeTo;
  }
  return input;
}

export interface ShareNotice {
  message: string;
  tone: 'submitted' | 'other';
}

export function shareNoticeFor(result: ProductWriteResult): ShareNotice | null {
  const detail = result.contribution?.detail;
  if (!detail) return null;
  return { message: detail, tone: result.contribution?.status === 'submitted' ? 'submitted' : 'other' };
}
