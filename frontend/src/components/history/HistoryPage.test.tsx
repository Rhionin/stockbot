import { fireEvent, render, screen, within } from '@testing-library/react';
import { MantineProvider } from '@mantine/core';
import { MemoryRouter, Route, Routes } from 'react-router-dom';
import { beforeEach, describe, expect, it, vi } from 'vitest';
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

vi.mock('../../api/client', () => ({
  getItemHistory: (...args: unknown[]) => getItemHistory(...args),
  getGroupHistory: (...args: unknown[]) => getGroupHistory(...args),
  setMoveQuantity: (...args: unknown[]) => setMoveQuantity(...args),
  undoMove: (...args: unknown[]) => undoMove(...args),
}));

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
    renderHistory();
    expect(await screen.findByText(/Added when taking out/)).toBeInTheDocument();

    fireEvent.click(screen.getByRole('button', { name: /Actions for Stocked in 1/ }));
    expect(screen.queryByRole('menuitem', { name: 'Change quantity' })).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole('menuitem', { name: 'Undo this stock in' }));
    const added = screen.getByRole('dialog', { name: 'Undo this add and take out?' });
    expect(within(added).getByText('This removes the added unit and the unit taken out. The pace is recalculated from the remaining history.')).toBeInTheDocument();
    expect(within(added).queryByText(/leaves the shelf/)).not.toBeInTheDocument();
    fireEvent.click(within(added).getByRole('button', { name: 'Keep it' }));

    fireEvent.click(screen.getByRole('button', { name: /Actions for Used 1/ }));
    expect(screen.queryByRole('menuitem', { name: 'Change quantity' })).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole('menuitem', { name: 'Undo this use' }));
    const used = screen.getByRole('dialog', { name: 'Undo this add and take out?' });
    expect(within(used).queryByText(/goes back on the shelf/)).not.toBeInTheDocument();
    expect(within(used).getByRole('button', { name: 'Undo add and take out' })).toBeInTheDocument();
  });

  it('lists the added row before its use when the response lists the use first', async () => {
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
    expect(rows[0]).toHaveTextContent('Added when taking out');
    expect(rows[1]).toHaveTextContent('scan');
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
