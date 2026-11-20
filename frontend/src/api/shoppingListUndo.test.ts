import { afterEach, describe, expect, it, vi } from 'vitest';
import type { ShoppingListEntry } from '@stockbot/shared/types';
import { undoRemoveShoppingListItem } from './client';

const entry = (): ShoppingListEntry => ({
  id: 'manual-tea',
  itemId: 'tea',
  quantity: 1,
  source: 'manual',
  purchasedAt: null,
});

describe('undoRemoveShoppingListItem', () => {
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it('POSTs to undo-remove with the pantry item id and returns the entry', async () => {
    const restored = entry();
    const fetchMock = vi.fn(() => Promise.resolve(new Response(JSON.stringify(restored), {
      status: 200,
      headers: { 'Content-Type': 'application/json' },
    })));
    vi.stubGlobal('fetch', fetchMock);

    await expect(undoRemoveShoppingListItem('tea')).resolves.toEqual(restored);
    expect(fetchMock).toHaveBeenCalledWith(
      expect.stringContaining('/api/shopping-list/items/tea/undo-remove'),
      expect.objectContaining({ method: 'POST' }),
    );
  });

  it('surfaces 404 trip remove skip not found', async () => {
    vi.stubGlobal('fetch', vi.fn(() => Promise.resolve(new Response(
      JSON.stringify({ error: 'trip remove skip not found', code: 'not_found' }),
      { status: 404, headers: { 'Content-Type': 'application/json' } },
    ))));

    await expect(undoRemoveShoppingListItem('tea')).rejects.toMatchObject({
      status: 404,
      message: 'trip remove skip not found',
    });
  });
});
