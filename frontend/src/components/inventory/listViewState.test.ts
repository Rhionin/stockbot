import { describe, expect, it } from 'vitest';
import {
  LIST_VIEW_TTL_MS,
  neighborIds,
  rememberedInventoryPath,
  restorationTarget,
  restoredScrollTop,
  scrollAfterLoad,
  viewIsFresh,
  type ListViewSnapshot,
} from './listViewState';

const view = (patch: Partial<ListViewSnapshot> = {}): ListViewSnapshot => ({
  scrollTop: 400,
  listHeight: 2000,
  search: 'oats',
  filter: 'low',
  expandedGroupId: null,
  lastItemId: 'item-3',
  before: ['item-2', 'item-1'],
  after: ['item-4', 'item-5'],
  itemOffset: 40,
  savedAt: 1_000,
  ...patch,
});

describe('list view state', () => {
  it('spec 23: a view is fresh one millisecond inside 30 minutes and expired at exactly 30 minutes', () => {
    const savedAt = 10_000;
    expect(viewIsFresh(savedAt, savedAt + LIST_VIEW_TTL_MS - 1)).toBe(true);
    expect(viewIsFresh(savedAt, savedAt + LIST_VIEW_TTL_MS)).toBe(false);
    expect(rememberedInventoryPath(view({ savedAt, filter: 'low' }), savedAt + LIST_VIEW_TTL_MS)).toBeNull();
    expect(rememberedInventoryPath(view({ savedAt, filter: 'low' }), savedAt + LIST_VIEW_TTL_MS - 1)).toBe('/inventory?filter=low');
    expect(rememberedInventoryPath(view({ savedAt, filter: '' }), savedAt)).toBe('/inventory');
    expect(rememberedInventoryPath(view({ savedAt, filter: 'groups' }), savedAt)).toBe('/inventory?filter=groups');
    expect(rememberedInventoryPath(null, savedAt)).toBeNull();
  });

  it('spec 25: a missing last item anchors on the next neighbour before the previous one', () => {
    const ids = ['a', 'b', 'd', 'e'];
    expect(neighborIds(['a', 'b', 'c', 'd', 'e'], 'c')).toEqual({ before: ['b', 'a'], after: ['d', 'e'] });
    expect(restorationTarget(ids, 'c', ['b', 'a'], ['d', 'e'])).toEqual({ id: 'd', highlight: false });
    expect(restorationTarget(ids, 'c', ['b', 'a'], [])).toEqual({ id: 'b', highlight: false });
    expect(restorationTarget(['c', 'd'], 'c', [], ['d'])).toEqual({ id: 'c', highlight: true });
    expect(restorationTarget([], 'c', ['b'], ['d'])).toEqual({ id: null, highlight: false });
  });

  it('spec 37: a scroll the person started during the placeholder is kept', () => {
    const anchored = restoredScrollTop({
      targetTop: 500,
      itemOffset: 40,
      savedScrollTop: 400,
      scrollHeight: 3000,
      clientHeight: 800,
    });
    expect(anchored).toBe(460);
    expect(scrollAfterLoad({
      userMoved: true,
      currentScrollTop: 120,
      targetTop: 500,
      itemOffset: 40,
      savedScrollTop: 400,
      scrollHeight: 3000,
      clientHeight: 800,
    })).toBe(120);
    expect(scrollAfterLoad({
      userMoved: false,
      currentScrollTop: 120,
      targetTop: null,
      itemOffset: 40,
      savedScrollTop: 9000,
      scrollHeight: 1000,
      clientHeight: 800,
    })).toBe(200);
  });
});
