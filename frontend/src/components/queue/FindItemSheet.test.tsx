import { beforeEach, describe, expect, it, vi } from 'vitest';
import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { MantineProvider } from '@mantine/core';
import { Notifications } from '@mantine/notifications';
import { FindItemSheet } from './FindItemSheet';
import type { ProductSearchHit } from '@stockbot/shared/types';

const undoMove = vi.fn();
const stockOutItem = vi.fn();
const searchProducts = vi.fn();
const ensureProductItem = vi.fn();
const addItemInstance = vi.fn();
const removeItemInstance = vi.fn();

vi.mock('../inventory/AddItemSheet', () => ({
  AddItemSheet: ({
    onCreated,
    onClose,
  }: {
    onCreated: (result: { id: string; name: string; itemId: string }, mode: 'stock_in' | 'stock_out') => void | Promise<void>;
    onClose: () => void;
  }) => (
    <div role="dialog" aria-label="Add an item">
      <button
        type="button"
        onClick={() => {
          void onCreated({ id: 'prod-new', name: 'Berry jam', itemId: 'item-new' }, 'stock_out');
          onClose();
        }}
      >
        Mock create
      </button>
    </div>
  ),
}));

vi.mock('../../api/client', () => ({
  undoMove: (...args: unknown[]) => undoMove(...args),
  stockOutItem: (...args: unknown[]) => stockOutItem(...args),
  searchProducts: (...args: unknown[]) => searchProducts(...args),
  ensureProductItem: (...args: unknown[]) => ensureProductItem(...args),
  addItemInstance: (...args: unknown[]) => addItemInstance(...args),
  removeItemInstance: (...args: unknown[]) => removeItemInstance(...args),
  ApiError: class ApiError extends Error {
    code: string;
    constructor(code: string) {
      super(code);
      this.code = code;
    }
  },
}));

const hit: ProductSearchHit = {
  productId: 'prod-oats',
  itemId: 'item-oats',
  name: 'Quick oats',
  onHand: 0,
};

const renderSheet = (direction: 'stock_in' | 'stock_out' = 'stock_out') => {
  const onInventoryChanged = vi.fn();
  const onClose = vi.fn();
  render(
    <MantineProvider>
      <Notifications />
      <FindItemSheet
        opened
        direction={direction}
        onClose={onClose}
        onInventoryChanged={onInventoryChanged}
      />
    </MantineProvider>,
  );
  return { onInventoryChanged, onClose };
};

describe('FindItemSheet backfill undo', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    undoMove.mockResolvedValue({});
    searchProducts.mockResolvedValue([hit]);
    ensureProductItem.mockResolvedValue({ itemId: 'item-oats' });
    stockOutItem.mockResolvedValue({
      backfill: { stockInMoveId: 'move-in', stockOutMoveId: 'move-out' },
    });
  });

  it('undoes stock-out before stock-in from inline none-on-hand confirm', async () => {
    renderSheet();
    const dialog = screen.getByRole('dialog', { name: 'Find an item' });
    fireEvent.change(within(dialog).getByLabelText('Find an item'), { target: { value: 'quick' } });
    await waitFor(() => expect(searchProducts).toHaveBeenCalled());
    await waitFor(() => expect(within(dialog).getByText('Quick oats')).toBeVisible());
    fireEvent.click(within(dialog).getByText('Quick oats'));
    await waitFor(() => expect(within(dialog).getByRole('button', { name: 'Add it and take it out' })).toBeVisible());
    await act(async () => {
      fireEvent.click(within(dialog).getByRole('button', { name: 'Add it and take it out' }));
    });
    await waitFor(() => expect(stockOutItem).toHaveBeenCalledWith('item-oats', { backfill: true }));
    const undoButton = await screen.findByRole('button', { name: 'Undo' });
    await act(async () => {
      fireEvent.click(undoButton);
    });
    await waitFor(() => expect(undoMove).toHaveBeenCalledTimes(2));
    expect(undoMove.mock.calls[0]?.[0]).toBe('move-out');
    expect(undoMove.mock.calls[1]?.[0]).toBe('move-in');
  });

  it('undoes stock-out before stock-in after Add named stock-out flow', async () => {
    searchProducts.mockResolvedValue([]);
    stockOutItem.mockResolvedValue({
      backfill: { stockInMoveId: 'move-in-2', stockOutMoveId: 'move-out-2' },
    });
    renderSheet();
    const dialog = screen.getByRole('dialog', { name: 'Find an item' });
    fireEvent.change(within(dialog).getByLabelText('Find an item'), { target: { value: 'berry' } });
    await waitFor(() => expect(within(dialog).getByRole('button', { name: 'Add "berry"' })).toBeVisible());
    fireEvent.click(within(dialog).getByRole('button', { name: 'Add "berry"' }));
    const addDialog = await screen.findByRole('dialog', { name: 'Add an item' });
    fireEvent.click(within(addDialog).getByRole('button', { name: 'Mock create' }));
    await waitFor(() => expect(stockOutItem).toHaveBeenCalledWith('item-new', { backfill: true }));
    const undoButton = await screen.findByRole('button', { name: 'Undo' });
    await act(async () => {
      fireEvent.click(undoButton);
    });
    await waitFor(() => expect(undoMove).toHaveBeenCalledTimes(2));
    expect(undoMove.mock.calls[0]?.[0]).toBe('move-out-2');
    expect(undoMove.mock.calls[1]?.[0]).toBe('move-in-2');
  });
});
