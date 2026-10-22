import { fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { MantineProvider } from '@mantine/core';
import { MemoryRouter } from 'react-router-dom';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import type { HistoryLedger, LedgerMove } from '../../types';
import { LedgerPage } from './LedgerPage';

const todayAt = new Date(2026, 9, 14, 15, 0, 0);
const earlierAt = new Date(2026, 9, 14, 8, 14, 0);
const yesterdayAt = new Date(2026, 9, 13, 18, 42, 0);

const moves: LedgerMove[] = [
  {
    id: 'move-milk',
    direction: 'in',
    quantity: 2,
    at: todayAt.toISOString(),
    source: 'scan',
    productId: 'prod-milk',
    productName: 'Milk',
    imageUrl: 'https://images.example.com/milk.jpg',
  },
  {
    id: 'move-oats',
    direction: 'in',
    quantity: 4,
    at: earlierAt.toISOString(),
    source: 'manual',
    productId: 'prod-oats',
    productName: 'Oats',
  },
  {
    id: 'move-peas',
    direction: 'out',
    quantity: 1,
    at: yesterdayAt.toISOString(),
    source: 'scan',
    productId: 'prod-peas',
    productName: 'Chickpeas',
    groupId: 'group-legumes',
    groupName: 'Legumes',
  },
];

const getHouseholdHistory = vi.fn();
const setMoveQuantity = vi.fn();
const undoMove = vi.fn();
const previewUndo = vi.fn();

vi.mock('../../api/client', () => ({
  getHouseholdHistory: (...args: unknown[]) => getHouseholdHistory(...args),
  setMoveQuantity: (...args: unknown[]) => setMoveQuantity(...args),
  undoMove: (...args: unknown[]) => undoMove(...args),
  previewUndo: (...args: unknown[]) => previewUndo(...args),
}));

const filtered = (filter?: { direction?: string; q?: string }): HistoryLedger => {
  let rows = moves;
  if (filter?.direction === 'in' || filter?.direction === 'out') {
    rows = rows.filter((move) => move.direction === filter.direction);
  }
  const query = filter?.q?.trim().toLowerCase() ?? '';
  if (query !== '') {
    rows = rows.filter((move) =>
      move.productName.toLowerCase().includes(query)
      || (move.groupName ?? '').toLowerCase().includes(query));
  }
  return { moves: rows };
};

const renderLedger = () => render(
  <MantineProvider env="test">
    <MemoryRouter>
      <LedgerPage />
    </MemoryRouter>
  </MantineProvider>,
);

describe('LedgerPage', () => {
  beforeEach(() => {
    vi.useFakeTimers({ toFake: ['Date'] });
    vi.setSystemTime(new Date(2026, 9, 14, 16, 0, 0));
    getHouseholdHistory.mockReset();
    setMoveQuantity.mockReset();
    undoMove.mockReset();
    previewUndo.mockReset();
    previewUndo.mockResolvedValue({ effects: [] });
    getHouseholdHistory.mockImplementation(async (filter?: { direction?: string; q?: string }) => filtered(filter));
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  it('lists every move newest first, grouped by day, with photo, direction, and amount', async () => {
    renderLedger();

    expect(await screen.findByRole('heading', { name: 'History' })).toBeInTheDocument();
    const today = screen.getByRole('region', { name: 'Today' });
    const yesterday = screen.getByRole('region', { name: 'Yesterday' });
    expect(today.compareDocumentPosition(yesterday) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();

    const todayRows = within(today).getAllByRole('listitem');
    expect(todayRows[0]).toHaveTextContent('Milk');
    expect(todayRows[0]).toHaveTextContent('Stocked in');
    expect(todayRows[0]).toHaveTextContent('+2');
    expect(todayRows[0]).toHaveTextContent('scan');
    expect(todayRows[0].querySelector('img')).toHaveAttribute('src', 'https://images.example.com/milk.jpg');
    expect(todayRows[1]).toHaveTextContent('Oats');
    expect(todayRows[1]).toHaveTextContent('by hand');

    const yesterdayRow = within(yesterday).getByRole('listitem');
    expect(yesterdayRow).toHaveTextContent('Chickpeas');
    expect(yesterdayRow).toHaveTextContent('Legumes');
    expect(yesterdayRow).toHaveTextContent('Used');
    expect(yesterdayRow).toHaveTextContent('−1');
    expect(getHouseholdHistory).toHaveBeenCalledWith({ direction: undefined, q: undefined });
  });

  it('filters by direction and by a product or group name', async () => {
    renderLedger();
    await screen.findByText('Chickpeas');

    fireEvent.click(screen.getByRole('button', { name: 'Used' }));
    await waitFor(() => expect(screen.queryByText('Milk')).not.toBeInTheDocument());
    expect(screen.getByText('Chickpeas')).toBeInTheDocument();
    expect(getHouseholdHistory).toHaveBeenCalledWith({ direction: 'out', q: undefined });

    fireEvent.click(screen.getByRole('button', { name: 'All' }));
    fireEvent.change(screen.getByRole('textbox', { name: 'Search products or groups' }), {
      target: { value: 'legume' },
    });
    await waitFor(() => expect(screen.queryByText('Oats')).not.toBeInTheDocument());
    expect(screen.getByText('Chickpeas')).toBeInTheDocument();
    expect(getHouseholdHistory).toHaveBeenCalledWith({ direction: undefined, q: 'legume' });

    fireEvent.change(screen.getByRole('textbox', { name: 'Search products or groups' }), {
      target: { value: 'oat' },
    });
    await waitFor(() => expect(screen.queryByText('Chickpeas')).not.toBeInTheDocument());
    expect(screen.getByText('Oats')).toBeInTheDocument();
  });

  it('changes a quantity and undoes a move with the same actions as one product', async () => {
    setMoveQuantity.mockResolvedValue({});
    undoMove.mockResolvedValue({});
    renderLedger();
    await screen.findByText('Chickpeas');

    fireEvent.click(screen.getByRole('button', { name: /Actions for Chickpeas, 1 used/ }));
    fireEvent.click(screen.getByRole('menuitem', { name: 'Change quantity' }));
    const field = screen.getByLabelText('How many units were actually used?');
    fireEvent.change(field, { target: { value: '2' } });
    fireEvent.click(screen.getByRole('button', { name: 'Save quantity' }));
    await vi.waitFor(() => expect(setMoveQuantity).toHaveBeenCalledWith('move-peas', 2));
    await waitFor(() => expect(screen.queryByRole('dialog', { name: 'Change quantity' })).not.toBeInTheDocument());

    expect(previewUndo).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole('button', { name: /Actions for Milk, 2 stocked in/ }));
    fireEvent.click(screen.getByRole('menuitem', { name: 'Undo this stock in' }));
    const dialog = screen.getByRole('dialog', { name: 'Undo this stock in?' });
    expect(previewUndo).not.toHaveBeenCalled();
    expect(within(dialog).getByText(/leave the shelf/)).toBeInTheDocument();
    fireEvent.click(within(dialog).getByRole('button', { name: 'Keep it' }));
    expect(undoMove).not.toHaveBeenCalled();

    fireEvent.click(screen.getByRole('button', { name: /Actions for Milk, 2 stocked in/ }));
    fireEvent.click(screen.getByRole('menuitem', { name: 'Undo this stock in' }));
    const confirm = await screen.findByRole('dialog', { name: 'Undo this stock in?' });
    fireEvent.click(within(confirm).getByRole('button', { name: 'Undo stock in' }));
    await vi.waitFor(() => expect(undoMove).toHaveBeenCalledWith('move-milk'));
  });

  it('charts the filtered moves, and leaves older moves in the list', async () => {
    const flourAt = new Date(2026, 8, 1, 11, 0, 0);
    getHouseholdHistory.mockImplementation(async (filter?: { direction?: string; q?: string }) => {
      const base = filtered(filter);
      if (filter?.direction === 'out' || (filter?.q && filter.q !== '')) return base;
      return {
        moves: [
          {
            id: 'move-flour',
            direction: 'in',
            quantity: 9,
            at: flourAt.toISOString(),
            source: 'manual',
            productId: 'prod-flour',
            productName: 'Flour',
          },
          ...base.moves,
        ],
      };
    });
    renderLedger();

    const chart = await screen.findByRole('img', { name: 'Last 30 days. 6 units stocked in. 1 unit used.' });
    expect(chart.querySelectorAll('.pulse-hump')).toHaveLength(2);
    expect(screen.getByRole('button', { name: '30 days' })).toHaveAttribute('aria-pressed', 'true');
    expect(screen.getByText(/Overlapping moves add together/)).toBeInTheDocument();
    expect(screen.getByText(/Height is the number of units/)).toBeInTheDocument();
    expect(screen.getByText('Flour')).toBeInTheDocument();

    fireEvent.click(screen.getByRole('button', { name: '90 days' }));
    expect(screen.getByRole('img', { name: 'Last 90 days. 15 units stocked in. 1 unit used.' })).toBeInTheDocument();

    fireEvent.click(screen.getByRole('button', { name: '30 days' }));
    fireEvent.click(screen.getByRole('button', { name: 'Used' }));
    expect(await screen.findByRole('img', { name: 'Last 30 days. No units stocked in. 1 unit used.' })).toBeInTheDocument();
    expect(screen.queryByText('Flour')).not.toBeInTheDocument();
  });

  it('starts the chart at the opening stock in when that day is inside the range', async () => {
    const setupAt = new Date(2026, 9, 1, 12, 0, 0).toISOString();
    const since = new Intl.DateTimeFormat(undefined, { month: 'short', day: 'numeric' }).format(new Date(2026, 9, 1, 12, 0, 0));
    getHouseholdHistory.mockImplementation(async (filter?: { direction?: string; q?: string }) => {
      const base = filtered(filter);
      if (filter?.direction === 'out') return { ...base, setupAt };
      return {
        setupAt,
        moves: [
          {
            id: 'move-rice',
            direction: 'in' as const,
            quantity: 80,
            at: new Date(2026, 8, 20, 9, 0, 0).toISOString(),
            source: 'scan' as const,
            productId: 'prod-rice',
            productName: 'Rice',
          },
          ...base.moves,
        ],
      };
    });
    renderLedger();

    expect(await screen.findByRole('img', { name: `Since ${since}. 6 units stocked in. 1 unit used.` })).toBeInTheDocument();
    expect(screen.getByText('The opening stock in is where this chart starts.')).toBeInTheDocument();
    expect(screen.getByText('6 stocked in')).toBeInTheDocument();
    expect(screen.getByText('1 used')).toBeInTheDocument();
    expect(screen.queryByText('80')).not.toBeInTheDocument();
    expect(screen.getByText('Rice')).toBeInTheDocument();

    fireEvent.click(screen.getByRole('button', { name: '7 days' }));
    expect(screen.getByRole('img', { name: 'Last 7 days. 6 units stocked in. 1 unit used.' })).toBeInTheDocument();
    expect(screen.queryByText('The opening stock in is where this chart starts.')).not.toBeInTheDocument();
  });

  it('reads the day under a tap without scrolling or moving focus', async () => {
    const scroll = vi.fn();
    const originalScroll = Element.prototype.scrollIntoView;
    Element.prototype.scrollIntoView = scroll;
    renderLedger();

    await screen.findByRole('img', { name: /Last 30 days/ });
    const hit = document.querySelector('.pulse-hit');
    if (!(hit instanceof HTMLElement)) throw new Error('chart hit target missing');
    expect(hit).not.toHaveAttribute('tabindex');
    hit.getBoundingClientRect = () => ({
      x: 0, y: 0, left: 0, top: 0, right: 1000, bottom: 140, width: 1000, height: 140, toJSON() { return {}; },
    });

    fireEvent.pointerMove(hit, { clientX: 1000, pointerType: 'mouse' });
    const tip = await screen.findByRole('tooltip');
    expect(tip).toHaveTextContent('6 stocked in');
    expect(tip).toHaveTextContent('0 used');
    expect(screen.getByRole('region', { name: 'Today' })).toHaveClass('is-inspected');
    expect(scroll).not.toHaveBeenCalled();

    fireEvent.pointerLeave(hit, { pointerType: 'mouse' });
    expect(screen.queryByRole('tooltip')).not.toBeInTheDocument();

    fireEvent.pointerDown(hit, { clientX: 1000, clientY: 40, pointerType: 'touch', pointerId: 1 });
    fireEvent.pointerUp(hit, { clientX: 1000, clientY: 40, pointerType: 'touch', pointerId: 1 });
    fireEvent.pointerDown(hit, { clientX: 1000, clientY: 40, pointerType: 'touch', pointerId: 1 });
    fireEvent.pointerUp(hit, { clientX: 1000, clientY: 40, pointerType: 'touch', pointerId: 1 });
    expect(await screen.findByRole('tooltip')).toHaveTextContent('6 stocked in');
    expect(screen.getByRole('region', { name: 'Today' })).toHaveClass('is-inspected');
    expect(scroll).not.toHaveBeenCalled();
    expect(document.activeElement).not.toBe(hit);
    Element.prototype.scrollIntoView = originalScroll;
  });

  it('says what to do when nothing has moved, and when a filter matches nothing', async () => {
    getHouseholdHistory.mockResolvedValue({ moves: [] });
    renderLedger();
    expect(await screen.findByText(/No stock moves yet/)).toBeInTheDocument();
    expect(screen.queryByRole('img', { name: /Last 30 days/ })).not.toBeInTheDocument();

    fireEvent.click(screen.getByRole('button', { name: 'Stocked in' }));
    expect(await screen.findByText('No stocked-in moves in this list.')).toBeInTheDocument();
  });

  it('does not offer a new count for a backfill use in the Used filter', async () => {
    const at = yesterdayAt.toISOString();
    const backfill = {
      id: 'move-bf',
      direction: 'in' as const,
      quantity: 1,
      at,
      source: 'backfill' as const,
      productId: 'prod-peas',
      productName: 'Chickpeas',
      groupName: 'Legumes',
    };
    const pairedUse = {
      id: 'move-use',
      direction: 'out' as const,
      quantity: 1,
      at,
      source: 'scan' as const,
      productId: 'prod-peas',
      productName: 'Chickpeas',
      groupName: 'Legumes',
    };
    const otherUse = {
      id: 'move-milk-out',
      direction: 'out' as const,
      quantity: 1,
      at,
      source: 'scan' as const,
      productId: 'prod-milk',
      productName: 'Milk',
    };
    getHouseholdHistory.mockImplementation(async (filter?: { direction?: string; q?: string }) => {
      const outs = [pairedUse, otherUse];
      if (filter?.direction === 'out') return { moves: outs };
      return { moves: [backfill, ...outs] };
    });
    previewUndo.mockResolvedValue({
      effects: [{
        itemId: 'item-peas',
        productId: 'prod-peas',
        name: 'Chickpeas',
        stockInMoveId: 'move-bf',
        stockOutMoveId: 'move-use',
        onHand: 0,
        reshelvesOnUndo: true,
      }],
    });
    renderLedger();
    await screen.findAllByText('Chickpeas');
    fireEvent.click(screen.getByRole('button', { name: 'Used' }));
    await waitFor(() => expect(screen.getAllByText('Chickpeas')).toHaveLength(1));

    fireEvent.click(screen.getByRole('button', { name: /Actions for Chickpeas, 1 used/ }));
    expect(screen.queryByRole('menuitem', { name: 'Change quantity' })).not.toBeInTheDocument();
    expect(screen.getByRole('menuitem', { name: 'Undo this use' })).toBeInTheDocument();
    fireEvent.click(screen.getByRole('menuitem', { name: 'Undo this use' }));
    const dialog = screen.getByRole('dialog', { name: 'Undo "Add it and take it out"?' });
    expect(within(dialog).getByText('This removes both the add and the take-out for Chickpeas, as if neither happened.')).toBeInTheDocument();
    expect(await within(dialog).findByText('Chickpeas will be hidden from your inventory again, since none are on hand.')).toBeInTheDocument();
    expect(within(dialog).queryByText(/pace|history|goes back on the shelf/)).not.toBeInTheDocument();
    expect(within(dialog).getByRole('button', { name: 'Undo' })).toBeEnabled();
    expect(previewUndo).toHaveBeenCalledWith('move-bf');

    fireEvent.click(screen.getByRole('button', { name: 'Cancel' }));
    fireEvent.click(screen.getByRole('button', { name: /Actions for Milk, 1 used/ }));
    expect(screen.getByRole('menuitem', { name: 'Change quantity' })).toBeInTheDocument();
  });

  it('leaves the hide line out when the preview says the undo does not re-shelve', async () => {
    const at = yesterdayAt.toISOString();
    previewUndo.mockResolvedValue({
      effects: [{
        itemId: 'item-peas',
        productId: 'prod-peas',
        name: 'Chickpeas',
        stockInMoveId: 'move-bf',
        stockOutMoveId: 'move-use',
        onHand: 0,
        reshelvesOnUndo: false,
      }],
    });
    getHouseholdHistory.mockResolvedValue({
      moves: [
        { id: 'move-bf', direction: 'in', quantity: 1, at, source: 'backfill', productId: 'prod-peas', productName: 'Chickpeas' },
        { id: 'move-use', direction: 'out', quantity: 1, at, source: 'scan', productId: 'prod-peas', productName: 'Chickpeas' },
      ],
    });
    renderLedger();
    fireEvent.click(await screen.findByRole('button', { name: /Actions for Chickpeas, 1 stocked in/ }));
    fireEvent.click(screen.getByRole('menuitem', { name: 'Undo this stock in' }));
    const dialog = screen.getByRole('dialog', { name: 'Undo "Add it and take it out"?' });
    expect(within(dialog).getByText('This removes both the add and the take-out for Chickpeas, as if neither happened.')).toBeInTheDocument();
    await waitFor(() => expect(previewUndo).toHaveBeenCalledWith('move-bf'));
    expect(within(dialog).queryByText('Chickpeas will be hidden from your inventory again, since none are on hand.')).not.toBeInTheDocument();
    expect(within(dialog).getByRole('button', { name: 'Undo' })).toBeEnabled();
  });

  it('lists the added row before its use', async () => {
    const at = todayAt.toISOString();
    getHouseholdHistory.mockResolvedValue({
      moves: [
        { id: 'out', direction: 'out', quantity: 1, at, source: 'scan', productId: 'p', productName: 'Cream' },
        { id: 'in', direction: 'in', quantity: 1, at, source: 'backfill', productId: 'p', productName: 'Cream' },
      ],
    });
    renderLedger();
    const rows = await screen.findAllByRole('listitem');
    expect(rows[0]).toHaveTextContent('Added when taking out');
    expect(rows[1]).toHaveTextContent('scan');
  });

  it('shows the history load error instead of an empty ledger', async () => {
    getHouseholdHistory.mockRejectedValue(new Error('Unable to load history.'));
    renderLedger();
    expect(await screen.findByRole('alert')).toHaveTextContent('Unable to load history.');
    expect(screen.queryByRole('listitem')).not.toBeInTheDocument();
    expect(screen.queryByText(/No stock moves yet/)).not.toBeInTheDocument();
    expect(screen.queryByLabelText('Loading history')).not.toBeInTheDocument();
    expect(previewUndo).not.toHaveBeenCalled();
  });
});
