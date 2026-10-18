import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { fireEvent, render, screen, waitFor } from '@testing-library/react';
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

type Kind = 'define' | 'add' | 'link' | 'backfill' | 'batch';

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
    toast: 'Nothing on hand to take out.',
    refreshed: true,
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
    body: { removed: 1, backfilled: 1, itemId: 'item-sour' },
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
  },
  {
    name: 'not_pending shows the server sentence and reloads',
    kind: 'backfill',
    status: 400,
    body: { error: 'not pending', code: 'not_pending' },
    visible: ['not pending'],
    absent: ['Add it and take it out'],
    toast: 'not pending',
    confirm: true,
    refreshed: true,
  },
  {
    name: 'direction_unset shows the server sentence',
    kind: 'backfill',
    status: 400,
    body: { error: 'scan direction must be set before committing', code: 'direction_unset' },
    visible: ['scan direction must be set before committing'],
    absent: ['Add it and take it out'],
    toast: 'scan direction must be set before committing',
    confirm: true,
  },
  {
    name: 'scan_not_found shows the server sentence and reloads',
    kind: 'backfill',
    status: 404,
    body: { error: 'scan entry "scan-1" not found', code: 'scan_not_found' },
    visible: ['scan entry "scan-1" not found'],
    absent: ['Add it and take it out'],
    toast: 'scan entry "scan-1" not found',
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
  },
  {
    name: 'a batch none_on_hand card does not offer backfill',
    kind: 'batch',
    visible: ['Nothing on hand to take out.'],
    absent: ['Add it and take it out', 'Add as a new item'],
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
        if (row.kind === 'backfill' && commits === 1) {
          return Promise.resolve(json({ error: 'Nothing on hand to take out.', code: 'none_on_hand' }, 409));
        }
        if ((row.kind === 'define' || row.kind === 'add' || row.kind === 'link') && commits === 1) {
          return Promise.resolve(json({
            error: 'barcode is not linked',
            code: 'no_item',
            suggestions: [{ itemId: 'item-linked', productId: 'prod-linked', name: 'Sour cream', onHand: 0 }],
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
        return Promise.resolve(json({
          ...entry(false),
          productId: 'prod-new',
          status: 'pending',
          onHand: row.onHand ?? 0,
          product: { id: 'prod-new', name: 'Sour cream', category: 'Dairy', unitOfMeasure: 'tub' },
        }));
      }
      if (url === '/api/items/item-sour/history') return Promise.resolve(json(history));
      if (url === '/api/moves/move-backfill' && method === 'DELETE') {
        return Promise.resolve(json({ ...history, moves: [] }));
      }
      if (url === '/api/inventory' || url.startsWith('/api/inventory?')) return Promise.resolve(json([]));
      return Promise.resolve(json([]));
    }));

    const named = row.kind === 'backfill' || row.kind === 'batch';
    render(
      <MantineProvider>
        <Notifications />
        <ScanEntryCard
          entry={entry(named)}
          selected={false}
          itemId={named ? 'item-sour' : undefined}
          commitNotice={row.kind === 'batch' ? 'Nothing on hand to take out.' : ''}
          commitCode={row.kind === 'batch' ? 'none_on_hand' : ''}
          onSelectedChange={vi.fn()}
          onChanged={onChanged}
        />
      </MantineProvider>,
    );

    if (row.kind === 'backfill') {
      fireEvent.click(screen.getByRole('button', { name: 'Confirm' }));
      await screen.findByRole('button', { name: 'Add it and take it out' });
      notifications.clean();
      fireEvent.click(screen.getByRole('button', { name: 'Add it and take it out' }));
    } else if (row.kind === 'define' || row.kind === 'add' || row.kind === 'link') {
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
      if (row.kind === 'add') {
        fireEvent.click(screen.getByRole('button', { name: 'Add as a new item' }));
      }
      if (row.kind === 'link') {
        fireEvent.click(screen.getByRole('button', { name: 'Link Sour cream' }));
      }
    }

    await waitFor(() => {
      for (const text of row.visible) expect(screen.getAllByText(text).length).toBeGreaterThan(0);
      for (const text of row.absent) expect(screen.queryByText(text, { exact: false })).not.toBeInTheDocument();
      if (row.toast !== undefined) {
        const detail = document.querySelector('.restock-toast-detail');
        expect(detail?.textContent).toBe(row.toast);
      }
      if (row.confirm) {
        expect(screen.getByRole('button', { name: 'Confirm' })).toBeInTheDocument();
      }
      if (row.refreshed) expect(onChanged).toHaveBeenCalled();
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
      if (row.kind === 'link') {
        const link = calls.filter((call) => call.url.endsWith('/commit') && call.method === 'POST').at(-1);
        expect(link?.body).toEqual({ linkProductId: 'prod-linked' });
      }
      if (row.kind === 'backfill') {
        const posted = calls.filter((call) => call.url.endsWith('/commit') && call.method === 'POST');
        expect(posted.at(-1)?.body).toEqual({ backfill: true });
      }
      if (row.kind === 'batch') {
        expect(calls.some((call) => call.url.endsWith('/commit'))).toBe(false);
      }
    });

    expect(screen.getByRole('article', { name: `Scan ${barcode}` })).toBeInTheDocument();

    if (row.undoId !== undefined) {
      fireEvent.click(screen.getByRole('button', { name: 'Undo' }));
      await waitFor(() => {
        expect(calls.some((call) => call.method === 'DELETE' && call.url === `/api/moves/${row.undoId}`)).toBe(true);
      });
    }
  });
});
