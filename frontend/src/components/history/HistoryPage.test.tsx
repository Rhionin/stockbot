import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { MantineProvider } from '@mantine/core';
import { MemoryRouter, Route, Routes } from 'react-router-dom';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { ApiError } from '../../api/client';
import type { HistoryView } from '../../types';
import { HistoryPage } from './HistoryPage';

const view: HistoryView = {
  kind: 'product',
  id: 'item-1',
  name: 'Black Beans',
  detail: '15 oz cans',
  onHand: 4,
  pace: {
    known: true,
    daysBetweenUses: 2.7,
    daysLeft: 11,
    daysLeftKnown: true,
    trend: 'steady',
    sparkline: [0, 1, 0, 2, 1, 0, 1],
  },
  moves: [
    {
      id: 'move-out',
      direction: 'out',
      quantity: 1,
      at: '2026-10-10T12:14:00Z',
      source: 'scan',
      productId: 'prod-1',
      productName: 'Black Beans',
    },
    {
      id: 'move-in',
      direction: 'in',
      quantity: 6,
      at: '2026-10-08T15:05:00Z',
      source: 'manual',
      productId: 'prod-1',
      productName: 'Black Beans',
    },
  ],
};

const getItemHistory = vi.fn();
const getGroupHistory = vi.fn();
const setMoveQuantity = vi.fn();
const undoMove = vi.fn();
const previewUndo = vi.fn();

vi.mock('../../api/client', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../../api/client')>();
  return {
    ...actual,
    getItemHistory: (...args: unknown[]) => getItemHistory(...args),
    getGroupHistory: (...args: unknown[]) => getGroupHistory(...args),
    setMoveQuantity: (...args: unknown[]) => setMoveQuantity(...args),
    undoMove: (...args: unknown[]) => undoMove(...args),
    previewUndo: (...args: unknown[]) => previewUndo(...args),
  };
});

const renderHistory = (path = '/inventory/item-1/history') => render(
  <MantineProvider env="test">
    <MemoryRouter initialEntries={[path]}>
      <Routes>
        <Route path="/inventory/:itemId/history" element={<HistoryPage />} />
        <Route path="/groups/:groupId/history" element={<HistoryPage />} />
      </Routes>
    </MemoryRouter>
  </MantineProvider>,
);

describe('HistoryPage', () => {
  beforeEach(() => {
    getItemHistory.mockReset();
    getGroupHistory.mockReset();
    setMoveQuantity.mockReset();
    undoMove.mockReset();
    previewUndo.mockReset();
    previewUndo.mockResolvedValue({ effects: [] });
    getItemHistory.mockResolvedValue(view);
    getGroupHistory.mockResolvedValue({ ...view, kind: 'group', name: 'Beans' });
  });

  it('leads with pace and then the recent moves', async () => {
    renderHistory();

    expect(await screen.findByRole('heading', { name: 'Black Beans' })).toBeInTheDocument();
    expect(screen.getByText('15 oz cans')).toBeInTheDocument();
    expect(screen.getByText('2.7')).toBeInTheDocument();
    expect(screen.getByText('days between uses')).toBeInTheDocument();
    expect(screen.getByText('4 on hand')).toBeInTheDocument();
    expect(screen.getByText('about 11 days left')).toBeInTheDocument();
    expect(screen.getByText('Steady compared with the previous 30 days')).toBeInTheDocument();
    expect(screen.getByText('Used 1')).toBeInTheDocument();
    expect(screen.getByText('Stocked in 6')).toBeInTheDocument();
    expect(screen.getByLabelText('1 used')).toBeInTheDocument();
    expect(screen.getByLabelText('6 stocked in')).toBeInTheDocument();
  });

  it('filters the trail to stocked-in moves', async () => {
    renderHistory();
    await screen.findByText('Used 1');

    fireEvent.click(screen.getByRole('button', { name: 'Stocked in' }));

    expect(screen.queryByText('Used 1')).not.toBeInTheDocument();
    expect(screen.getByText('Stocked in 6')).toBeInTheDocument();
  });

  it('saves a corrected quantity and reloads the pace', async () => {
    setMoveQuantity.mockResolvedValue(view);
    renderHistory();
    await screen.findByText('Used 1');

    fireEvent.click(screen.getByRole('button', { name: /Actions for Used 1/ }));
    fireEvent.click(screen.getByRole('menuitem', { name: 'Change quantity' }));
    const field = screen.getByLabelText('How many units were actually used?');
    fireEvent.change(field, { target: { value: '2' } });
    fireEvent.click(screen.getByRole('button', { name: 'Save quantity' }));

    await vi.waitFor(() => expect(setMoveQuantity).toHaveBeenCalledWith('move-out', 2));
    expect(getItemHistory).toHaveBeenCalledTimes(2);
  });

  it('asks before undoing a move', async () => {
    undoMove.mockResolvedValue({ ...view, moves: [view.moves[1]], onHand: 10, pace: { ...view.pace, daysBetweenUses: 4 } });
    renderHistory();
    await screen.findByText('Used 1');

    fireEvent.click(screen.getByRole('button', { name: /Actions for Used 1/ }));
    fireEvent.click(screen.getByRole('menuitem', { name: 'Undo this use' }));
    const dialog = screen.getByRole('dialog', { name: 'Undo this use?' });
    expect(within(dialog).getByText(/goes back on the shelf/)).toBeInTheDocument();

    fireEvent.click(within(dialog).getByRole('button', { name: 'Keep it' }));
    expect(undoMove).not.toHaveBeenCalled();

    fireEvent.click(screen.getByRole('button', { name: /Actions for Used 1/ }));
    fireEvent.click(screen.getByRole('menuitem', { name: 'Undo this use' }));
    fireEvent.click(screen.getByRole('button', { name: 'Undo this use' }));

    await vi.waitFor(() => expect(undoMove).toHaveBeenCalledWith('move-out'));
  });

  it('keeps undo on a backfill pair and does not offer a new count', async () => {
    const at = '2026-10-18T16:40:00.000Z';
    getItemHistory.mockResolvedValue({
      ...view,
      onHand: 0,
      moves: [
        { id: 'move-backfill', direction: 'in', quantity: 1, at, source: 'backfill', productId: 'prod-1', productName: 'Black Beans' },
        { id: 'move-out-pair', direction: 'out', quantity: 1, at, source: 'scan', productId: 'prod-1', productName: 'Black Beans' },
      ],
    });
    previewUndo.mockResolvedValue({
      effects: [{
        itemId: 'item-1',
        productId: 'prod-1',
        name: 'Black Beans',
        stockInMoveId: 'move-backfill',
        stockOutMoveId: 'move-out-pair',
        onHand: 0,
        reshelvesOnUndo: true,
      }],
    });
    renderHistory();
    expect(await screen.findByText(/Added when taking out/)).toBeInTheDocument();

    fireEvent.click(screen.getByRole('button', { name: /Actions for Stocked in 1/ }));
    expect(screen.queryByRole('menuitem', { name: 'Change quantity' })).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole('menuitem', { name: 'Undo this stock in' }));
    const added = screen.getByRole('dialog', { name: 'Undo "Add it and take it out"?' });
    expect(within(added).getByText('This removes both the add and the take-out for Black Beans, as if neither happened.')).toBeInTheDocument();
    await waitFor(() => expect(within(added).getByText('Black Beans will be hidden from your inventory again, since none are on hand.')).toBeVisible());
    expect(within(added).queryByText(/pace|history|leaves the shelf/)).not.toBeInTheDocument();
    expect(within(added).getByRole('button', { name: 'Cancel' })).toBeInTheDocument();
    fireEvent.click(within(added).getByRole('button', { name: 'Cancel' }));

    fireEvent.click(screen.getByRole('button', { name: /Actions for Used 1/ }));
    expect(screen.queryByRole('menuitem', { name: 'Change quantity' })).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole('menuitem', { name: 'Undo this use' }));
    const used = screen.getByRole('dialog', { name: 'Undo "Add it and take it out"?' });
    expect(within(used).queryByText(/goes back on the shelf/)).not.toBeInTheDocument();
    await waitFor(() => expect(within(used).getByText('Black Beans will be hidden from your inventory again, since none are on hand.')).toBeVisible());
    expect(within(used).getByRole('button', { name: 'Undo' })).toBeEnabled();
    expect(previewUndo).toHaveBeenCalledWith('move-backfill');
  });

  const hideLine = 'Black Beans will be hidden from your inventory again, since none are on hand.';
  const pairBody = 'This removes both the add and the take-out for Black Beans, as if neither happened.';
  const previewEffect = (reshelvesOnUndo: boolean, stockInMoveId = 'move-backfill') => ({
    effects: [{
      itemId: 'item-1',
      productId: 'prod-1',
      name: 'Black Beans',
      stockInMoveId,
      stockOutMoveId: 'move-out-pair',
      onHand: 0,
      reshelvesOnUndo,
    }],
  });

  it.each([
    { name: 'true', settle: 'true', hide: true },
    { name: 'false', settle: 'false', hide: false },
    { name: 'loading', settle: 'loading', hide: false },
    { name: '404', settle: '404', hide: false },
    { name: '409', settle: '409', hide: false },
    { name: 'network error', settle: 'network', hide: false },
    { name: 'no matching effect', settle: 'other', hide: false },
  ])('shows the hide line only when the preview says the undo re-shelves ($name)', async ({ settle, hide }) => {
    const at = '2026-10-18T16:40:00.000Z';
    if (settle === 'loading') previewUndo.mockReturnValue(new Promise(() => {}));
    if (settle === '404') previewUndo.mockRejectedValue(new ApiError(404, 'That move is no longer in the history.'));
    if (settle === '409') previewUndo.mockRejectedValue(new ApiError(409, '1 of these was already used. Undo that use first.'));
    if (settle === 'network') previewUndo.mockRejectedValue(new TypeError('Failed to fetch'));
    if (settle === 'other') previewUndo.mockResolvedValue(previewEffect(true, 'move-other'));
    if (settle === 'true') previewUndo.mockResolvedValue(previewEffect(true));
    if (settle === 'false') previewUndo.mockResolvedValue(previewEffect(false));
    getItemHistory.mockResolvedValue({
      ...view,
      onHand: 0,
      moves: [
        { id: 'move-backfill', direction: 'in', quantity: 1, at, source: 'backfill', productId: 'prod-1', productName: 'Black Beans' },
        { id: 'move-out-pair', direction: 'out', quantity: 1, at, source: 'scan', productId: 'prod-1', productName: 'Black Beans' },
      ],
    });
    renderHistory();
    fireEvent.click(await screen.findByRole('button', { name: /Actions for Stocked in 1/ }));
    fireEvent.click(screen.getByRole('menuitem', { name: 'Undo this stock in' }));
    const dialog = screen.getByRole('dialog', { name: 'Undo "Add it and take it out"?' });
    expect(within(dialog).getByText(pairBody)).toBeInTheDocument();
    await vi.waitFor(() => expect(previewUndo).toHaveBeenCalledWith('move-backfill'));
    if (settle !== 'loading') {
      const pending = previewUndo.mock.results.at(-1)?.value as Promise<unknown> | undefined;
      await act(async () => {
        await pending?.catch(() => undefined);
      });
    }
    if (hide) expect(within(dialog).getByText(hideLine)).toBeVisible();
    else expect(within(dialog).getByText(hideLine)).not.toBeVisible();
    expect(within(dialog).getByRole('button', { name: 'Undo' })).toBeEnabled();
    if (settle === '404') expect(within(dialog).queryByText('That move is no longer in the history.')).not.toBeInTheDocument();
    if (settle === '409') expect(within(dialog).queryByText('1 of these was already used. Undo that use first.')).not.toBeInTheDocument();
  });

  it('shows the history load error and does not preview an undo', async () => {
    getItemHistory.mockRejectedValue(new Error('Unable to load history.'));
    renderHistory();
    expect(await screen.findByRole('alert')).toHaveTextContent('Unable to load history.');
    expect(screen.queryByRole('heading', { name: 'Black Beans' })).not.toBeInTheDocument();
    expect(screen.queryByLabelText('Loading history')).not.toBeInTheDocument();
    expect(previewUndo).not.toHaveBeenCalled();
  });

  it('does not preview while the dialog is closed or for a normal move', async () => {
    renderHistory();
    await screen.findByText('Used 1');
    expect(previewUndo).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole('button', { name: /Actions for Used 1/ }));
    fireEvent.click(screen.getByRole('menuitem', { name: 'Undo this use' }));
    expect(screen.getByRole('dialog', { name: 'Undo this use?' })).toBeInTheDocument();
    expect(previewUndo).not.toHaveBeenCalled();
  });

  const gate = () => {
    let resolve: (value: unknown) => void = () => {};
    let reject: (reason: unknown) => void = () => {};
    const promise = new Promise((done, fail) => {
      resolve = done;
      reject = fail;
    });
    return { promise, resolve, reject };
  };

  const pairView = () => {
    const at = '2026-10-18T16:40:00.000Z';
    return {
      ...view,
      onHand: 0,
      moves: [
        { id: 'move-backfill', direction: 'in' as const, quantity: 1, at, source: 'backfill' as const, productId: 'prod-1', productName: 'Black Beans' },
        { id: 'move-out-pair', direction: 'out' as const, quantity: 1, at, source: 'scan' as const, productId: 'prod-1', productName: 'Black Beans' },
      ],
    };
  };

  const openPairUndo = async () => {
    fireEvent.click(await screen.findByRole('button', { name: /Actions for Stocked in 1/ }));
    fireEvent.click(screen.getByRole('menuitem', { name: 'Undo this stock in' }));
    return screen.getByRole('dialog', { name: 'Undo "Add it and take it out"?' });
  };

  it.each([
    { name: 'reopening drops the previous true answer', order: 'settled-then-false' },
    { name: 'a slow earlier true loses to the later false', order: 'false-then-late-true' },
    { name: 'a 404 on reopen leaves the line out', order: 'settled-then-404' },
    { name: 'a 409 on reopen leaves the line out', order: 'settled-then-409' },
  ])('$name', async ({ order }) => {
    const first = gate();
    const second = gate();
    let calls = 0;
    previewUndo.mockImplementation(() => {
      calls += 1;
      return calls === 1 ? first.promise : second.promise;
    });
    getItemHistory.mockResolvedValue(pairView());
    renderHistory();
    const opened = await openPairUndo();
    expect(within(opened).getByText(hideLine)).not.toBeVisible();
    if (order !== 'false-then-late-true') {
      await act(async () => {
        first.resolve(previewEffect(true));
      });
      expect(within(opened).getByText(hideLine)).toBeVisible();
    }
    fireEvent.click(within(opened).getByRole('button', { name: 'Cancel' }));
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
    expect(previewUndo).toHaveBeenCalledTimes(1);

    const again = await openPairUndo();
    expect(within(again).getByText(hideLine)).not.toBeVisible();
    expect(within(again).getByRole('button', { name: 'Undo' })).toBeEnabled();
    if (order === 'settled-then-false' || order === 'false-then-late-true') {
      await act(async () => {
        second.resolve(previewEffect(false));
      });
    }
    if (order === 'settled-then-404') {
      await act(async () => {
        second.reject(new ApiError(404, 'That move is no longer in the history.'));
      });
    }
    if (order === 'settled-then-409') {
      await act(async () => {
        second.reject(new ApiError(409, '1 of these was already used. Undo that use first.'));
      });
    }
    expect(within(again).getByText(hideLine)).not.toBeVisible();
    expect(within(again).queryByText('That move is no longer in the history.')).not.toBeInTheDocument();
    expect(within(again).queryByText('1 of these was already used. Undo that use first.')).not.toBeInTheDocument();
    if (order === 'false-then-late-true') {
      await act(async () => {
        first.resolve(previewEffect(true));
      });
      expect(within(again).getByText(hideLine)).not.toBeVisible();
    }
    expect(previewUndo).toHaveBeenCalledTimes(2);
    expect(previewUndo.mock.calls).toEqual([['move-backfill'], ['move-backfill']]);
  });

  it('undoes the pair while the preview is still pending', async () => {
    const pending = gate();
    previewUndo.mockReturnValue(pending.promise);
    undoMove.mockResolvedValue(pairView());
    getItemHistory.mockResolvedValue(pairView());
    renderHistory();
    const dialog = await openPairUndo();
    expect(within(dialog).getByText('This removes both the add and the take-out for Black Beans, as if neither happened.')).toBeInTheDocument();
    expect(within(dialog).getByText('Black Beans will be hidden from your inventory again, since none are on hand.')).not.toBeVisible();
    expect(within(dialog).getByRole('button', { name: 'Undo' })).toBeEnabled();
    fireEvent.click(within(dialog).getByRole('button', { name: 'Undo' }));
    await vi.waitFor(() => expect(undoMove).toHaveBeenCalledWith('move-backfill'));
    await vi.waitFor(() => expect(screen.queryByRole('dialog', { name: 'Undo "Add it and take it out"?' })).not.toBeInTheDocument());
    await act(async () => {
      pending.resolve(previewEffect(true));
    });
  });

  it('keeps the server order when the response lists the use first', async () => {
    const at = '2026-10-18T16:40:00.000Z';
    getItemHistory.mockResolvedValue({
      ...view,
      moves: [
        { id: 'move-out-pair', direction: 'out', quantity: 1, at, source: 'scan', productId: 'prod-1', productName: 'Black Beans' },
        { id: 'move-backfill', direction: 'in', quantity: 1, at, source: 'backfill', productId: 'prod-1', productName: 'Black Beans' },
      ],
    });
    renderHistory();
    const rows = await screen.findAllByRole('listitem');
    expect(rows[0]).toHaveTextContent('scan');
    expect(rows[1]).toHaveTextContent('Added when taking out');
  });

  it('shows the locked-count sentence when a save is refused', async () => {
    const sentence = "This was recorded with Add it and take it out, so its count can't be changed. Undo it instead.";
    setMoveQuantity.mockRejectedValue(Object.assign(new Error(sentence), { code: 'backfill_locked', status: 409 }));
    renderHistory();
    await screen.findByText('Used 1');
    fireEvent.click(screen.getByRole('button', { name: /Actions for Used 1/ }));
    fireEvent.click(screen.getByRole('menuitem', { name: 'Change quantity' }));
    fireEvent.click(screen.getByRole('button', { name: 'Save quantity' }));
    expect(await screen.findByText(sentence)).toBeInTheDocument();
  });

  it('says when the pace is not known yet', async () => {
    getItemHistory.mockResolvedValue({
      ...view,
      onHand: 0,
      pace: { known: false, daysLeftKnown: false, sparkline: [] },
      moves: [],
    });
    renderHistory();

    expect(await screen.findByText('Pace not known yet')).toBeInTheDocument();
    expect(screen.getByText('None on hand')).toBeInTheDocument();
    expect(screen.getByText(/No stock moves yet/)).toBeInTheDocument();
  });

  it('loads a group history', async () => {
    renderHistory('/groups/group-1/history');

    expect(await screen.findByRole('heading', { name: 'Beans' })).toBeInTheDocument();
    expect(getGroupHistory).toHaveBeenCalledWith('group-1');
    expect(screen.getByRole('link', { name: 'Group' })).toHaveAttribute('href', '/groups/group-1');
  });
});
