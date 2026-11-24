import { ADD_IT_AND_TAKE_IT_OUT } from './contract';
import { ADD_AND_STOCK_IN, ADD_ITEM_SUBMIT } from './noBarcodeCopy';
import type { ProductSearchHit } from './types';

export const FIND_ITEM_DEBOUNCE_MS = 300;
export const FIND_ITEM_MIN_QUERY = 2;

export const runeLength = (text: string): number => [...text].length;

export function onHandLabel(count: number): string {
  if (count === 0) return 'None on hand';
  if (count === 1) return '1 on hand';
  return `${count} on hand`;
}

export function hitSubtitle(hit: Pick<ProductSearchHit, 'brand' | 'size'>): string {
  return [hit.brand, hit.size].filter((part) => part !== undefined && part !== '').join(' · ');
}

export interface FindItemView {
  trimmed: string;
  // One letter typed: the field says so. Nothing typed is not an error.
  tooShort: boolean;
  searchable: boolean;
  rows: ProductSearchHit[];
  showAddNamed: boolean;
}

// The rows follow the debounced query, so the list stays put while the person
// types and clears the moment the debounced text is too short to search.
export function findItemView(query: string, debounced: string, results: ProductSearchHit[]): FindItemView {
  const trimmed = query.trim();
  const length = runeLength(trimmed);
  const rows = runeLength(debounced) >= FIND_ITEM_MIN_QUERY ? results : [];
  const exact = rows.some((hit) => hit.name.toLowerCase() === trimmed.toLowerCase());
  return {
    trimmed,
    tooShort: length > 0 && length < FIND_ITEM_MIN_QUERY,
    searchable: runeLength(debounced) >= FIND_ITEM_MIN_QUERY,
    rows,
    showAddNamed: length >= FIND_ITEM_MIN_QUERY && !exact,
  };
}

export type AddItemMode = 'inventory' | 'stock_in' | 'stock_out';

export const addItemSubmitLabel: Record<AddItemMode, string> = {
  inventory: ADD_ITEM_SUBMIT,
  stock_in: ADD_AND_STOCK_IN,
  stock_out: ADD_IT_AND_TAKE_IT_OUT,
};
