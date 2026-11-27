// In-memory inventory list view. Ids, search text, and numbers only — never list rows.

export const LIST_VIEW_TTL_MS = 30 * 60 * 1000;

export interface ListViewSnapshot {
  scrollTop: number;
  listHeight: number;
  search: string;
  filter: string;
  expandedGroupId: string | null;
  lastItemId: string | null;
  /** Up to two row ids before lastItemId, nearest first. */
  before: string[];
  /** Up to two row ids after lastItemId, nearest first. */
  after: string[];
  /** Distance from the top of the scrollport to the last item. */
  itemOffset: number;
  savedAt: number;
}

export function viewIsFresh(savedAt: number, now: number): boolean {
  return now - savedAt < LIST_VIEW_TTL_MS;
}

export function rememberedInventoryPath(view: ListViewSnapshot | null, now: number): string | null {
  if (!view || !viewIsFresh(view.savedAt, now)) return null;
  if (view.filter === '' || view.filter === 'all') return '/inventory';
  return `/inventory?filter=${encodeURIComponent(view.filter)}`;
}

export function neighborIds(rowIds: readonly string[], lastItemId: string | null): { before: string[]; after: string[] } {
  const index = lastItemId === null ? -1 : rowIds.indexOf(lastItemId);
  if (index < 0) return { before: [], after: [] };
  const before = rowIds.slice(Math.max(0, index - 2), index).reverse();
  const after = rowIds.slice(index + 1, index + 3);
  return { before, after };
}

/** Last-opened row when it is still rendered; otherwise the next two, then the previous two. */
export function restorationTarget(
  rowIds: readonly string[],
  lastItemId: string | null,
  before: readonly string[],
  after: readonly string[],
): { id: string | null; highlight: boolean } {
  if (lastItemId !== null && rowIds.includes(lastItemId)) return { id: lastItemId, highlight: true };
  const order = [...after.slice(0, 2), ...before.slice(0, 2)];
  for (const id of order) {
    if (rowIds.includes(id)) return { id, highlight: false };
  }
  return { id: null, highlight: false };
}

export function restoredScrollTop(input: {
  targetTop: number | null;
  itemOffset: number;
  savedScrollTop: number;
  scrollHeight: number;
  clientHeight: number;
}): number {
  const max = Math.max(0, input.scrollHeight - input.clientHeight);
  const raw = input.targetTop === null ? input.savedScrollTop : input.targetTop - input.itemOffset;
  return Math.min(max, Math.max(0, raw));
}

/** A scroll the person started during the placeholder wins. Otherwise anchor the saved row. */
export function scrollAfterLoad(input: {
  userMoved: boolean;
  currentScrollTop: number;
  targetTop: number | null;
  itemOffset: number;
  savedScrollTop: number;
  scrollHeight: number;
  clientHeight: number;
}): number {
  if (input.userMoved) return input.currentScrollTop;
  return restoredScrollTop(input);
}

let slot: { scope: string; view: ListViewSnapshot | null } = { scope: '', view: null };

/** Clears a saved view when the signed-in user or household changes, before children render. */
export function adoptListViewScope(scope: string) {
  if (slot.scope !== scope) slot = { scope, view: null };
}

export function readListView(now = Date.now()): ListViewSnapshot | null {
  const view = slot.view;
  if (!view || !viewIsFresh(view.savedAt, now)) return null;
  return view;
}

export function writeListView(view: ListViewSnapshot | null) {
  slot = { scope: slot.scope, view };
}

export function inventoryScroller(): HTMLElement | null {
  if (typeof document === 'undefined') return null;
  return document.querySelector('.app-shell > .mantine-AppShell-main');
}
