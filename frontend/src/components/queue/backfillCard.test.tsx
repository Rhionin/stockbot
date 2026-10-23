import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { MantineProvider } from '@mantine/core';
import { notifications, Notifications } from '@mantine/notifications';
import { SERVER_ERROR_FALLBACK } from '../../api/contract';
import { ScanEntryCard } from './ScanEntryCard';
import type { ScanEntry } from '../../types';

const barcode = '049000000294';

const entry = (named: boolean): ScanEntry => ({
  id: 'scan-1',
  userId: 'user-1',
  barcode,
  scannedAt: '2026-10-18T04:00:00.000Z',
  direction: 'stock_out',
  unitCount: 1,
  expiresAt: null,
  status: 'pending',
  productId: named ? 'prod-sour' : null,
  product: named
    ? { id: 'prod-sour', name: 'Sour cream', category: 'Dairy', unitOfMeasure: 'tub' }
    : null,
  committedAt: null,
  createdAt: '2026-10-18T04:00:00.000Z',
});

interface Call {
  method: string;
  url: string;
  body?: unknown;
}

type Kind = 'define' | 'add' | 'link' | 'backfill' | 'none' | 'batch' | 'suggest' | 'patch';

interface Outcome {
  name: string;
  kind: Kind;
  status?: number;
  body?: unknown;
  lookupName?: string;
  typedName?: string;
  onHand?: number;
  visible: string[];
  absent: string[];
  toast?: string;
  undoId?: string;
  confirm?: boolean;
  refreshed?: boolean;
  nameDisabled?: boolean;
  suggestions?: { itemId: string; productId: string; name: string; onHand: number }[];
  readsSuggestions?: boolean;
  openingSuggestions?: { itemId: string; productId: string; name: string; onHand: number }[];
  suggestionsFail?: boolean;
  createStatus?: number;
  createBody?: unknown;
  undoStatus?: number;
  undoError?: string;
  undoReject?: boolean;
}

const noneOnHand = {
  error: 'Nothing on hand to take out.',
  code: 'none_on_hand',
  itemId: 'item-sour',
  onHand: 0,
};

const history = {
  kind: 'product',
  id: 'item-sour',
  name: 'Sour cream',
  onHand: 0,
  pace: { known: false, daysLeftKnown: false, sparkline: [] },
  moves: [
    {
      id: 'move-backfill',
      direction: 'in',
      quantity: 1,
      at: '2026-10-18T04:40:00.000Z',
      source: 'backfill',
      productId: 'prod-sour',
      productName: 'Sour cream',
    },
    {
      id: 'move-out',
      direction: 'out',
      quantity: 1,
      at: '2026-10-18T04:40:00.000Z',
      source: 'scan',
      productId: 'prod-sour',
      productName: 'Sour cream',
    },
  ],
};

const outcomes: Outcome[] = [
  {
    name: 'an unknown barcode asks which item and can be added, with no backfill',
    kind: 'define',
    visible: ['Which item is this?', 'Add as a new item', 'Product name'],
    absent: ['Add it and take it out', 'Not in Stockbot yet? Add it and take it out.'],
    nameDisabled: true,
  },
  {
    name: 'a lookup name is prefilled',
    kind: 'define',
    lookupName: 'Sour cream',
    visible: ['Which item is this?', 'Add as a new item'],
    absent: ['Add it and take it out'],
  },
  {
    name: 'add as a new item leaves the scan pending with nothing on hand',
    kind: 'add',
    typedName: 'Sour cream',
    onHand: 0,
    visible: ['Nothing on hand to take out.', 'Add it and take it out', 'Sour cream'],
    absent: ['Which item is this?', 'Add as a new item'],
    refreshed: true,
  },
  {
    name: 'a prefilled name is the product that gets created',
    kind: 'add',
    lookupName: 'Sour cream',
    onHand: 0,
    visible: ['Add it and take it out', 'Nothing on hand to take out.'],
    absent: ['Which item is this?'],
    refreshed: true,
  },
  {
    name: 'a new item that already has units keeps the normal stock-out',
    kind: 'add',
    typedName: 'Sour cream',
    onHand: 2,
    visible: ['Sour cream'],
    absent: ['Add it and take it out', 'Nothing on hand to take out.'],
    confirm: true,
    refreshed: true,
  },
  {
    name: 'linking an empty shelf keeps the link and offers backfill',
    kind: 'link',
    status: 409,
    body: noneOnHand,
    visible: ['Nothing on hand to take out.', 'Add it and take it out'],
    absent: ['Which item is this?', 'Add as a new item'],
    refreshed: false,
  },
  {
    name: 'linking an item that has units takes it out',
    kind: 'link',
    status: 200,
    body: { ...entry(true), status: 'committed', onHand: 2 },
    visible: [],
    absent: ['Add it and take it out', 'Which item is this?'],
    confirm: true,
    refreshed: true,
  },
  {
    name: 'backfill success toasts and undo deletes the backfill move',
    kind: 'backfill',
    status: 200,
    body: {
      removed: 1,
      backfilled: 1,
      itemId: 'item-sour',
      backfill: { stockInMoveId: 'move-backfill', stockOutMoveId: 'move-out' },
    },
    visible: ['Added and taken out.'],
    absent: [],
    toast: 'Added and taken out.',
    undoId: 'move-backfill',
    refreshed: true,
    confirm: true,
  },
  {
    name: 'product_unresolved sends the person back to define the barcode',
    kind: 'backfill',
    status: 400,
    body: { error: 'Choose which item this is first.', code: 'product_unresolved' },
    visible: ['Choose which item this is first.', 'Which item is this?', 'Add as a new item'],
    absent: ['Add it and take it out'],
    toast: 'Choose which item this is first.',
    confirm: true,
    refreshed: false,
  },
  {
    name: 'units_on_hand refreshes the card and offers the normal stock-out',
    kind: 'backfill',
    status: 400,
    body: {
      error: "You already have some on hand, so there's nothing to add.",
      code: 'units_on_hand',
      itemId: 'item-sour',
      onHand: 3,
    },
    visible: ["You already have some on hand, so there's nothing to add."],
    absent: ['Add it and take it out'],
    toast: "You already have some on hand, so there's nothing to add.",
    confirm: true,
    refreshed: true,
  },
  {
    name: 'not_stock_out shows the server sentence',
    kind: 'backfill',
    status: 400,
    body: { error: 'This only works when taking something out.', code: 'not_stock_out' },
    visible: ['This only works when taking something out.'],
    absent: ['Add it and take it out'],
    toast: 'This only works when taking something out.',
    confirm: true,
    refreshed: false,
  },
  {
    name: 'not_pending shows the server sentence and reloads',
    kind: 'backfill',
    status: 400,
    body: { error: 'This scan was already handled.', code: 'not_pending' },
    visible: ['This scan was already handled.'],
    absent: ['Add it and take it out'],
    toast: 'This scan was already handled.',
    confirm: true,
    refreshed: true,
  },
  {
    name: 'direction_unset shows the server sentence',
    kind: 'backfill',
    status: 400,
    body: { error: 'Choose stock in or stock out first.', code: 'direction_unset' },
    visible: ['Choose stock in or stock out first.'],
    absent: ['Add it and take it out'],
    toast: 'Choose stock in or stock out first.',
    confirm: true,
    refreshed: false,
  },
  {
    name: 'scan_not_found shows the server sentence and reloads',
    kind: 'backfill',
    status: 404,
    body: { error: 'This scan is no longer in the queue.', code: 'scan_not_found' },
    visible: ['This scan is no longer in the queue.'],
    absent: ['Add it and take it out'],
    toast: 'This scan is no longer in the queue.',
    confirm: true,
    refreshed: true,
  },
  {
    name: 'none_on_hand on backfill is a wiped item and drops the offer',
    kind: 'backfill',
    status: 409,
    body: { error: 'Nothing on hand to take out.', code: 'none_on_hand' },
    visible: ['Nothing on hand to take out.'],
    absent: ['Add it and take it out'],
    toast: 'Nothing on hand to take out.',
    confirm: true,
    refreshed: false,
  },
  {
    name: 'internal shows the server sentence',
    kind: 'backfill',
    status: 500,
    body: { error: 'Something went wrong. Try again.', code: 'internal' },
    visible: ['Something went wrong. Try again.'],
    absent: ['Add it and take it out'],
    toast: 'Something went wrong. Try again.',
    confirm: true,
    refreshed: false,
  },
  {
    name: 'a 500 with no sentence uses the shared fallback',
    kind: 'backfill',
    status: 500,
    body: {},
    visible: [SERVER_ERROR_FALLBACK],
    absent: ['Add it and take it out'],
    toast: 'Something went wrong. Try again.',
    confirm: true,
    refreshed: false,
  },
  {
    name: 'a batch never offers or claims a backfill',
    kind: 'batch',
    visible: [],
    absent: ['Add it and take it out', 'Added and taken out.', 'Nothing on hand to take out.'],
  },
  {
    name: 'none_on_hand maps to the card with the backfill offer',
    kind: 'none',
    visible: [
      'Nothing on hand to take out.',
      'Add it and take it out',
      "Stockbot thinks you have none, so this can't be confirmed yet. Stock it in first, or remove this scan.",
    ],
    absent: ['Added and taken out.', 'No inventory item is available for this product.', 'Not in your pantry'],
  },
  {
    name: 'a shelved item is on hand 0 and offers backfill',
    kind: 'add',
    typedName: 'Sour cream',
    onHand: 0,
    visible: ['Add it and take it out', 'Nothing on hand to take out.'],
    absent: ['Which item is this?'],
    refreshed: true,
  },
  {
    name: 'patch link_target_gone drops the item and reloads suggestions',
    kind: 'patch',
    status: 409,
    body: { error: "That item isn't available anymore. Pick another.", code: 'link_target_gone' },
    suggestions: [{ itemId: 'item-kept', productId: 'prod-kept', name: 'Kept Oats', onHand: 2 }],
    typedName: 'Sour cream',
    visible: ["That item isn't available anymore. Pick another.", 'Kept Oats', 'Which item is this?'],
    absent: ['Add it and take it out'],
    toast: "That item isn't available anymore. Pick another.",
    readsSuggestions: true,
  },
  {
    name: 'patch conflicting_fields shows the server sentence',
    kind: 'patch',
    status: 400,
    body: { error: 'Choose one change at a time.', code: 'conflicting_fields' },
    typedName: 'Sour cream',
    visible: ['Choose one change at a time.', 'Which item is this?', 'Add as a new item', 'Sour cream'],
    absent: ['Add it and take it out'],
    toast: 'Choose one change at a time.',
  },
  {
    name: 'link suggestions leave out the scan\'s own product',
    kind: 'suggest',
    status: 200,
    body: {
      suggestions: [
        { itemId: 'item-own', productId: 'prod-sour', name: 'Own cream', onHand: 1 },
        { itemId: 'item-kept', productId: 'prod-kept', name: 'Kept Oats', onHand: 2 },
      ],
    },
    visible: ['Kept Oats', "That item isn't available anymore. Pick another."],
    absent: ['Own cream', 'Add it and take it out'],
    toast: "That item isn't available anymore. Pick another.",
    readsSuggestions: true,
  },
  {
    name: 'link suggestions 404 scan_not_found shows the server sentence',
    kind: 'suggest',
    status: 404,
    body: { error: 'This scan is no longer in the queue.', code: 'scan_not_found' },
    visible: ['This scan is no longer in the queue.'],
    absent: ['Add it and take it out'],
    toast: 'This scan is no longer in the queue.',
    refreshed: true,
    readsSuggestions: true,
  },
  {
    name: 'link suggestions not_pending shows the server sentence',
    kind: 'suggest',
    status: 400,
    body: { error: 'This scan was already handled.', code: 'not_pending' },
    visible: ['This scan was already handled.'],
    absent: ['Add it and take it out'],
    toast: 'This scan was already handled.',
    refreshed: true,
    readsSuggestions: true,
  },
  {
    name: 'link suggestions not_stock_out shows the server sentence',
    kind: 'suggest',
    status: 400,
    body: { error: 'This only works when taking something out.', code: 'not_stock_out' },
    visible: ['This only works when taking something out.'],
    absent: ['Add it and take it out'],
    toast: 'This only works when taking something out.',
    readsSuggestions: true,
  },
  {
    name: 'link suggestions direction_unset shows the server sentence',
    kind: 'suggest',
    status: 400,
    body: { error: 'Choose stock in or stock out first.', code: 'direction_unset' },
    visible: ['Choose stock in or stock out first.'],
    absent: ['Add it and take it out'],
    toast: 'Choose stock in or stock out first.',
    readsSuggestions: true,
  },
  {
    name: 'patch link_target_gone drops the created product when suggestions cannot reload',
    kind: 'patch',
    status: 409,
    body: { error: "That item isn't available anymore. Pick another.", code: 'link_target_gone' },
    openingSuggestions: [
      { itemId: 'item-new', productId: 'prod-new', name: 'Fresh Cream', onHand: 0 },
      { itemId: 'item-kept', productId: 'prod-kept', name: 'Kept Oats', onHand: 2 },
    ],
    suggestionsFail: true,
    typedName: 'Sour cream',
    visible: ["That item isn't available anymore. Pick another.", 'Kept Oats', 'Which item is this?'],
    absent: ['Fresh Cream', 'Add it and take it out'],
    toast: "That item isn't available anymore. Pick another.",
    readsSuggestions: true,
  },
  {
    name: 'a create rejected as link_target_gone does not drop a suggestion that has no product',
    kind: 'patch',
    createStatus: 409,
    createBody: { error: "That item isn't available anymore. Pick another.", code: 'link_target_gone' },
    openingSuggestions: [
      { itemId: 'item-blank', productId: '', name: 'Unnamed', onHand: 0 },
      { itemId: 'item-kept', productId: 'prod-kept', name: 'Kept Oats', onHand: 2 },
    ],
    suggestionsFail: true,
    typedName: 'Sour cream',
    visible: ["That item isn't available anymore. Pick another.", 'Unnamed', 'Kept Oats'],
    absent: ['Add it and take it out'],
    toast: "That item isn't available anymore. Pick another.",
    readsSuggestions: true,
  },
  {
    name: 'a failed undo shows the server sentence',
    kind: 'backfill',
    status: 200,
    body: {
      removed: 1,
      backfilled: 1,
      itemId: 'item-sour',
      backfill: { stockInMoveId: 'move-backfill', stockOutMoveId: 'move-out' },
    },
    visible: ['Added and taken out.'],
    absent: [],
    toast: 'Added and taken out.',
    undoId: 'move-backfill',
    undoStatus: 400,
    undoError: 'This scan was already handled.',
    refreshed: true,
    confirm: true,
  },
  {
    name: 'a failed undo with no sentence uses the shared fallback',
    kind: 'backfill',
    status: 200,
    body: {
      removed: 1,
      backfilled: 1,
      itemId: 'item-sour',
      backfill: { stockInMoveId: 'move-backfill', stockOutMoveId: 'move-out' },
    },
    visible: ['Added and taken out.'],
    absent: [],
    toast: 'Added and taken out.',
    undoId: 'move-backfill',
    undoStatus: 500,
    undoError: SERVER_ERROR_FALLBACK,
    refreshed: true,
    confirm: true,
  },
  {
    name: 'a failed undo that never reaches the server uses the shared fallback',
    kind: 'backfill',
    status: 200,
    body: {
      removed: 1,
      backfilled: 1,
      itemId: 'item-sour',
      backfill: { stockInMoveId: 'move-backfill', stockOutMoveId: 'move-out' },
    },
    visible: ['Added and taken out.'],
    absent: [],
    toast: 'Added and taken out.',
    undoId: 'move-backfill',
    undoReject: true,
    undoError: SERVER_ERROR_FALLBACK,
    refreshed: true,
    confirm: true,
  },
  {
    name: 'undo deletes the stock-in id from the commit response',
    kind: 'backfill',
    status: 200,
    body: {
      removed: 2,
      backfilled: 2,
      itemId: 'item-sour-cream',
      backfill: { stockInMoveId: 'move-cream-in', stockOutMoveId: 'move-cream-out' },
    },
    visible: ['Added and taken out.'],
    absent: [],
    toast: 'Added and taken out.',
    undoId: 'move-cream-in',
    refreshed: true,
    confirm: true,
  },
];

const json = (body: unknown, status = 200) =>
  new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } });

describe('backfill card', () => {
  beforeEach(() => {
    notifications.clean();
  });

  afterEach(() => {
    notifications.clean();
    vi.unstubAllGlobals();
  });

  it.each(outcomes)('$name', async (row) => {
    const calls: Call[] = [];
    let commits = 0;
    const onChanged = vi.fn();
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      const method = init?.method ?? 'GET';
      const body = init?.body === undefined ? undefined : JSON.parse(String(init.body)) as unknown;
      calls.push({ method, url, body });
      if (url.endsWith('/commit') && method === 'POST') {
        commits += 1;
        if ((row.kind === 'backfill' || row.kind === 'none') && commits === 1) {
          return Promise.resolve(json({ error: 'Nothing on hand to take out.', code: 'none_on_hand', itemId: 'item-sour', onHand: 0 }, 409));
        }
        if ((row.kind === 'define' || row.kind === 'add' || row.kind === 'link' || row.kind === 'patch' || row.kind === 'suggest') && commits === 1) {
          return Promise.resolve(json({
            error: 'barcode is not linked',
            code: 'no_item',
            suggestions: row.openingSuggestions ?? [{ itemId: 'item-linked', productId: 'prod-linked', name: 'Sour cream', onHand: 0 }],
          }, 409));
        }
        if (row.kind === 'suggest') {
          return Promise.resolve(json({
            error: "That item isn't available anymore. Pick another.",
            code: 'link_target_gone',
          }, 409));
        }
        return Promise.resolve(json(row.body ?? {}, row.status ?? 200));
      }
      if (url.startsWith('/api/products/lookup')) {
        const product = row.lookupName === undefined
          ? null
          : { id: 'prod-lookup', name: row.lookupName, category: 'Dairy', unitOfMeasure: 'tub' };
        return Promise.resolve(json({ product, source: product === null ? '' : 'global' }));
      }
      if (url === '/api/products' && method === 'POST') {
        if (row.createStatus !== undefined && row.createStatus >= 400) {
          return Promise.resolve(json(row.createBody ?? {}, row.createStatus));
        }
        const written = body as { name: string; category: string; unitOfMeasure: string };
        return Promise.resolve(json({
          id: 'prod-new',
          name: written.name,
          category: written.category,
          unitOfMeasure: written.unitOfMeasure,
          createdAt: '2026-10-18T04:40:00.000Z',
        }, 201));
      }
      if (url === '/api/products/overrides' && method === 'POST') {
        return Promise.resolve(json({ ...(body as object), source: 'user' }));
      }
      if (url === '/api/scans/scan-1' && method === 'PATCH') {
        if (row.kind === 'patch') return Promise.resolve(json(row.body ?? {}, row.status ?? 400));
        const written = body as { productId?: string; status?: string };
        if (written.productId !== undefined && written.status === 'cancelled') {
          return Promise.resolve(json({ error: 'Choose one change at a time.', code: 'conflicting_fields' }, 400));
        }
        return Promise.resolve(json({
          ...entry(false),
          productId: 'prod-new',
          status: 'pending',
          onHand: row.onHand ?? 0,
          product: { id: 'prod-new', name: 'Sour cream', category: 'Dairy', unitOfMeasure: 'tub' },
        }));
      }
      if (url === '/api/scans/scan-1/link-suggestions') {
        if (row.suggestionsFail) return Promise.reject(new TypeError('Failed to fetch'));
        if (row.kind === 'suggest' && row.status !== undefined && row.status !== 200) {
          return Promise.resolve(json(row.body ?? {}, row.status));
        }
        const listed = row.suggestions
          ?? (row.body as { suggestions?: unknown[] } | undefined)?.suggestions
          ?? [];
        return Promise.resolve(json({ suggestions: listed }));
      }
      if (url === '/api/items/item-sour/history' || url === '/api/items/item-sour-cream/history') {
        return Promise.resolve(json(history));
      }
      if (method === 'DELETE' && url.startsWith('/api/moves/')) {
        if (row.undoReject) return Promise.reject(new TypeError('Failed to fetch'));
        if (row.undoStatus !== undefined && row.undoStatus >= 400) {
          const failure = row.undoStatus >= 500 ? {} : { error: row.undoError, code: 'not_pending' };
          return Promise.resolve(json(failure, row.undoStatus));
        }
        return Promise.resolve(json({ ...history, moves: [] }));
      }
      if (url === '/api/inventory' || url.startsWith('/api/inventory?')) return Promise.resolve(json([]));
      return Promise.resolve(json([]));
    }));

    const named = row.kind === 'backfill' || row.kind === 'none' || row.kind === 'batch' || row.name.includes('own product');
    render(
      <MantineProvider>
        <Notifications />
        <ScanEntryCard
          entry={entry(named)}
          selected={false}
          itemId={named ? 'item-sour' : undefined}
          commitCode={row.kind === 'batch' ? 'none_on_hand' : ''}
          onSelectedChange={vi.fn()}
          onChanged={onChanged}
        />
      </MantineProvider>,
    );

    let refreshBefore = 0;
    if (row.kind === 'backfill' || row.kind === 'none') {
      fireEvent.click(screen.getByRole('button', { name: 'Confirm' }));
      await screen.findByRole('button', { name: 'Add it and take it out' });
      if (row.kind === 'backfill') {
        notifications.clean();
        refreshBefore = onChanged.mock.calls.length;
        fireEvent.click(screen.getByRole('button', { name: 'Add it and take it out' }));
      }
    } else if (row.kind === 'define' || row.kind === 'add' || row.kind === 'link' || row.kind === 'patch' || row.kind === 'suggest') {
      fireEvent.click(screen.getByRole('button', { name: 'Confirm' }));
      const offer = await screen.findByRole('region', { name: 'Which item is this?' });
      expect(offer).toBeInTheDocument();
      if (row.lookupName !== undefined) {
        await waitFor(() => expect(screen.getByRole('textbox', { name: 'Product name' })).toHaveValue(row.lookupName));
      }
      if (row.nameDisabled) {
        await waitFor(() => expect(screen.getByRole('button', { name: 'Add as a new item' })).toBeDisabled());
      }
      if (row.typedName !== undefined) {
        fireEvent.change(screen.getByRole('textbox', { name: 'Product name' }), {
          target: { value: row.typedName },
        });
      }
      if (row.kind === 'add' || row.kind === 'patch') {
        fireEvent.click(screen.getByRole('button', { name: 'Add as a new item' }));
      }
      if (row.kind === 'link' || row.kind === 'suggest') {
        fireEvent.click(screen.getByRole('button', { name: 'Link Sour cream' }));
      }
    }

    await waitFor(() => {
      for (const text of row.absent) expect(screen.queryByText(text, { exact: false })).not.toBeInTheDocument();
    });

    await waitFor(() => {
      for (const text of row.visible) expect(screen.getAllByText(text).length).toBeGreaterThan(0);
      if (row.toast !== undefined) {
        const detail = document.querySelector('.restock-toast-detail');
        expect(detail?.textContent).toBe(row.toast);
      }
      if (row.confirm) {
        expect(screen.getByRole('button', { name: 'Confirm' })).toBeInTheDocument();
      }
      if (row.kind === 'backfill' && row.refreshed === true) {
        expect(onChanged.mock.calls.length).toBeGreaterThan(refreshBefore);
      }
      if (row.kind === 'backfill' && row.refreshed === false) {
        expect(onChanged.mock.calls.length).toBe(refreshBefore);
      }
      if (row.kind !== 'backfill' && row.refreshed) expect(onChanged).toHaveBeenCalled();
      if (row.kind === 'link' && row.refreshed === false) expect(onChanged).not.toHaveBeenCalled();
      if (row.kind === 'add') {
        const created = row.typedName ?? row.lookupName;
        const product = calls.find((call) => call.url === '/api/products' && call.method === 'POST');
        const override = calls.find((call) => call.url === '/api/products/overrides' && call.method === 'POST');
        const patch = calls.find((call) => call.url === '/api/scans/scan-1' && call.method === 'PATCH');
        expect(product?.body).toMatchObject({ name: created });
        expect(override?.body).toEqual({ barcode, productId: 'prod-new' });
        expect(patch?.body).toEqual({ productId: 'prod-new', status: 'pending' });
        expect(calls.indexOf(product!)).toBeLessThan(calls.indexOf(override!));
        expect(calls.indexOf(override!)).toBeLessThan(calls.indexOf(patch!));
        expect(calls.some((call) => call.method === 'POST' && call.url === '/api/scans')).toBe(false);
      }
      if (row.kind === 'link' || row.kind === 'suggest') {
        const link = calls.filter((call) => call.url.endsWith('/commit') && call.method === 'POST').at(-1);
        expect(link?.body).toEqual({ linkProductId: 'prod-linked' });
      }
      if (row.kind === 'patch' || row.kind === 'add') {
        const patches = calls.filter((call) => call.url === '/api/scans/scan-1' && call.method === 'PATCH');
        for (const patch of patches) {
          const written = patch.body as { productId?: string; status?: string };
          expect(written.productId !== undefined && written.status === 'cancelled').toBe(false);
        }
      }
      if (row.readsSuggestions) {
        expect(calls.some((call) => call.url === '/api/scans/scan-1/link-suggestions' && call.method === 'GET')).toBe(true);
        expect(calls.filter((call) => call.url.endsWith('/commit') && call.method === 'POST')).toHaveLength(row.kind === 'suggest' ? 2 : 1);
      }
      if (row.kind === 'backfill') {
        const posted = calls.filter((call) => call.url.endsWith('/commit') && call.method === 'POST');
        expect(posted.at(-1)?.body).toEqual({ backfill: true });
        expect(calls.some((call) => call.url.includes('/history'))).toBe(false);
      }
      if (row.kind === 'batch') {
        expect(calls.some((call) => call.url.endsWith('/commit') || call.url.includes('batch-commit'))).toBe(false);
        expect(calls.some((call) => {
          const written = call.body as { backfill?: boolean } | undefined;
          return written?.backfill === true;
        })).toBe(false);
        expect(screen.queryByRole('button', { name: 'Add it and take it out' })).not.toBeInTheDocument();
        expect(screen.queryByText('Added and taken out.')).not.toBeInTheDocument();
        expect(screen.getByRole('button', { name: 'Confirm' })).toBeInTheDocument();
      }
      if (row.kind === 'none') {
        const posted = calls.filter((call) => call.url.endsWith('/commit') && call.method === 'POST');
        expect(posted).toHaveLength(1);
        expect(posted[0]?.body).toEqual({});
        expect(document.querySelector('.restock-toast')).toBeNull();
        expect(screen.getByRole('article', { name: `Scan ${barcode}` })).toHaveClass('scan-entry-card--nothing');
        expect(screen.queryByRole('button', { name: 'Confirm' })).not.toBeInTheDocument();
      }
    });

    const articleName = row.kind === 'batch' ? new RegExp(`^Scan ${barcode}`) : `Scan ${barcode}`;
    expect(screen.getByRole('article', { name: articleName })).toBeInTheDocument();

    if (row.undoId !== undefined) {
      const inventoryReads = () => calls.filter((call) => call.method === 'GET' && (call.url === '/api/inventory' || call.url.startsWith('/api/inventory?'))).length;
      const beforeInventory = inventoryReads();
      const beforeRefresh = onChanged.mock.calls.length;
      fireEvent.click(screen.getByRole('button', { name: 'Undo' }));
      await waitFor(() => {
        expect(calls.some((call) => call.method === 'DELETE' && call.url === `/api/moves/${row.undoId}`)).toBe(true);
        expect(inventoryReads()).toBe(beforeInventory);
        if (row.undoError !== undefined) {
          const details = [...document.querySelectorAll('.restock-toast-detail')];
          expect(details.some((node) => node.textContent === row.undoError)).toBe(true);
          expect(onChanged.mock.calls.length).toBe(beforeRefresh);
        } else {
          expect(onChanged.mock.calls.length).toBeGreaterThan(beforeRefresh);
        }
      });
    }
  });

  it('undo of the later commit removes that pair when history lists the newer scan first', async () => {
    const calls: Call[] = [];
    const commits = new Map<string, number>();
    const newer = { ...entry(true), id: 'scan-newer', barcode: '111', scannedAt: '2026-10-18T18:00:00.000Z' };
    const older = { ...entry(true), id: 'scan-older', barcode: '222', scannedAt: '2026-10-18T04:00:00.000Z' };
    const committed: Record<string, unknown> = {
      'scan-newer': {
        removed: 1,
        backfilled: 1,
        itemId: 'item-sour-cream',
        backfill: { stockInMoveId: 'move-newer-in', stockOutMoveId: 'move-newer-out' },
      },
      'scan-older': {
        removed: 1,
        backfilled: 1,
        itemId: 'item-sour-cream',
        backfill: { stockInMoveId: 'move-older-in', stockOutMoveId: 'move-older-out' },
      },
    };
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      const method = init?.method ?? 'GET';
      calls.push({ method, url });
      if (url.endsWith('/commit') && method === 'POST') {
        const id = url.split('/').at(-2) ?? '';
        const count = (commits.get(id) ?? 0) + 1;
        commits.set(id, count);
        if (count === 1) {
          return Promise.resolve(json({ error: 'Nothing on hand to take out.', code: 'none_on_hand' }, 409));
        }
        return Promise.resolve(json(committed[id] ?? {}));
      }
      if (url.includes('/history')) {
        return Promise.resolve(json({
          moves: [
            { id: 'move-newer-in', source: 'backfill', direction: 'in', at: newer.scannedAt, productId: 'prod-sour' },
            { id: 'move-older-in', source: 'backfill', direction: 'in', at: older.scannedAt, productId: 'prod-sour' },
          ],
        }));
      }
      if (method === 'DELETE' && url.startsWith('/api/moves/')) return Promise.resolve(json({ moves: [] }));
      if (url === '/api/inventory' || url.startsWith('/api/inventory?')) {
        return Promise.resolve(json([{
          item: {
            id: 'item-sour-cream',
            productId: 'prod-sour',
            onHand: 0,
            product: { id: 'prod-sour', name: 'Sour cream', category: 'Dairy', unitOfMeasure: 'tub' },
          },
        }]));
      }
      return Promise.resolve(json([]));
    }));

    const newerChanged = vi.fn();
    const olderChanged = vi.fn();
    render(
      <MantineProvider>
        <Notifications />
        <ScanEntryCard entry={newer} selected={false} itemId="item-sour" onSelectedChange={vi.fn()} onChanged={newerChanged} />
        <ScanEntryCard entry={older} selected={false} itemId="item-sour" onSelectedChange={vi.fn()} onChanged={olderChanged} />
      </MantineProvider>,
    );

    const newerCard = screen.getByRole('article', { name: 'Scan 111' });
    const olderCard = screen.getByRole('article', { name: 'Scan 222' });
    fireEvent.click(within(newerCard).getByRole('button', { name: 'Confirm' }));
    fireEvent.click(await within(newerCard).findByRole('button', { name: 'Add it and take it out' }));
    await screen.findByText('Added and taken out.');

    fireEvent.click(within(olderCard).getByRole('button', { name: 'Confirm' }));
    fireEvent.click(await within(olderCard).findByRole('button', { name: 'Add it and take it out' }));
    await waitFor(() => expect(screen.getAllByText('Added and taken out.')).toHaveLength(2));

    const inventoryReads = () => calls.filter((call) => call.method === 'GET' && call.url.startsWith('/api/inventory')).length;
    const beforeInventory = inventoryReads();
    const beforeOlder = olderChanged.mock.calls.length;
    const latest = screen.getAllByText('Added and taken out.').at(-1)?.closest('.restock-toast');
    if (!(latest instanceof HTMLElement)) throw new Error('missing undo toast');
    fireEvent.click(within(latest).getByRole('button', { name: 'Undo' }));
    await waitFor(() => {
      expect(calls.some((call) => call.method === 'DELETE' && call.url === '/api/moves/move-older-in')).toBe(true);
      expect(olderChanged.mock.calls.length).toBeGreaterThan(beforeOlder);
      expect(inventoryReads()).toBe(beforeInventory);
    });
    expect(calls.some((call) => call.method === 'DELETE' && call.url === '/api/moves/move-newer-in')).toBe(false);
    expect(calls.some((call) => call.url.includes('/history'))).toBe(false);
  });

  it('clears the nothing-on-hand toast when the backfill succeeds', async () => {
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      const method = init?.method ?? 'GET';
      if (url.endsWith('/commit') && method === 'POST') {
        const body = init?.body === undefined ? undefined : JSON.parse(String(init.body)) as { backfill?: boolean };
        if (body?.backfill === true) {
          return Promise.resolve(json({
            removed: 1,
            backfilled: 1,
            itemId: 'item-sour',
            backfill: { stockInMoveId: 'move-backfill', stockOutMoveId: 'move-out' },
          }));
        }
        return Promise.resolve(json({ error: 'barcode is not linked', code: 'no_item', suggestions: [] }, 409));
      }
      if (url.startsWith('/api/products/lookup')) return Promise.resolve(json({ product: null, source: '' }));
      if (url === '/api/products' && method === 'POST') {
        const written = JSON.parse(String(init?.body)) as { name: string };
        return Promise.resolve(json({
          id: 'prod-new',
          name: written.name,
          category: '',
          unitOfMeasure: '',
          createdAt: '2026-10-18T04:40:00.000Z',
        }, 201));
      }
      if (url === '/api/products/overrides' && method === 'POST') return Promise.resolve(json({ source: 'user' }));
      if (url === '/api/scans/scan-1' && method === 'PATCH') {
        return Promise.resolve(json({
          ...entry(false),
          productId: 'prod-new',
          status: 'pending',
          onHand: 0,
          product: { id: 'prod-new', name: 'Sour cream', category: '', unitOfMeasure: '' },
        }));
      }
      return Promise.resolve(json([]));
    }));

    render(
      <MantineProvider>
        <Notifications />
        <ScanEntryCard entry={entry(false)} selected={false} onSelectedChange={vi.fn()} onChanged={vi.fn()} />
      </MantineProvider>,
    );

    fireEvent.click(screen.getByRole('button', { name: 'Confirm' }));
    fireEvent.change(await screen.findByRole('textbox', { name: 'Product name' }), {
      target: { value: 'Sour cream' },
    });
    fireEvent.click(screen.getByRole('button', { name: 'Add as a new item' }));
    expect(await screen.findByRole('button', { name: 'Add it and take it out' })).toBeInTheDocument();
    expect(document.querySelector('.restock-toast')).toBeNull();
    fireEvent.click(screen.getByRole('button', { name: 'Add it and take it out' }));
    await waitFor(() => {
      const details = [...document.querySelectorAll('.restock-toast-detail')].map((node) => node.textContent);
      expect(details).toEqual(['Added and taken out.']);
    });
  });
});
