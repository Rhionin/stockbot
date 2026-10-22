export interface RestockTarget {
  kind: 'months' | 'amount';
  value: number;
}

export interface ChainCell {
  key: string;
  label: string;
  value: string;
  win: boolean;
  struck: boolean;
}

export interface RestockMembership {
  id: string;
  name: string;
}

export interface RestockItem {
  itemId: string;
  productId: string;
  name: string;
  unit: string;
  groupId?: string;
  groupName?: string;
  target: RestockTarget;
  level: string;
  levelName?: string;
  sourceLabel: string;
  valueLabel: string;
  quietLine?: string;
  rowSource: string;
  chain: ChainCell[];
  useAction?: string;
  useHint?: string;
  categories: RestockMembership[];
  setLabel: string;
}

export interface CategorySummary {
  id: string;
  name: string;
  items: number;
  ownTargets: number;
  target?: RestockTarget;
  valueLabel: string;
  sourceLabel: string;
}

export interface RestockSnapshot {
  household: RestockTarget;
  householdLabel: string;
  appliesTo: number;
  itemCount: number;
  items: RestockItem[];
  categories: CategorySummary[];
  notRestocked: number;
  suggestionCount: number;
}

export interface PreviewLine {
  itemId: string;
  name: string;
  changes: boolean;
  before?: string;
  after: string;
  reason?: string;
}

export interface RestockPreview {
  title: string;
  body: string;
  stop: PreviewLine[];
  keep: PreviewLine[];
  confirm: string;
}

export interface SuggestionItem {
  itemId: string;
  name: string;
  reason: string;
  checked: boolean;
  source: string;
}

export interface SuggestionCard {
  category: string;
  categoryId?: string;
  new: boolean;
  freshOnly: boolean;
  note?: string;
  items: SuggestionItem[];
  notSuggested?: string[];
}

export interface DontRestockResult {
  previous: RestockTarget | null;
  title: string;
  detail: string;
}

export function asRestock(value: unknown): RestockSnapshot | null {
  if (!value || typeof value !== 'object' || Array.isArray(value)) return null;
  const row = value as Partial<RestockSnapshot>;
  if (!row.household || typeof row.household !== 'object' || !Array.isArray(row.items) || !Array.isArray(row.categories)) {
    return null;
  }
  return {
    household: row.household,
    householdLabel: row.householdLabel ?? '',
    appliesTo: row.appliesTo ?? 0,
    itemCount: row.itemCount ?? row.items.length,
    items: row.items,
    categories: row.categories,
    notRestocked: row.notRestocked ?? 0,
    suggestionCount: row.suggestionCount ?? 0,
  };
}

export function asSuggestions(value: unknown): SuggestionCard[] {
  if (!Array.isArray(value)) return [];
  return value.filter((row): row is SuggestionCard => (
    !!row && typeof row === 'object' && typeof (row as SuggestionCard).category === 'string' && Array.isArray((row as SuggestionCard).items)
  ));
}
