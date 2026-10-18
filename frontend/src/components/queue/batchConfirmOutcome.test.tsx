import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { MantineProvider } from '@mantine/core';
import { notifications, Notifications } from '@mantine/notifications';
import { SERVER_ERROR_FALLBACK } from '../../api/contract';
import { showUndo } from '../restock/undo';
import { ScanQueuePage } from './ScanQueuePage';
import { formatExpiryDate } from './queueUtils';
import type { ItemInstanceWithStatus, ScanEntry } from '../../types';

class FakeEventSource {
  static instances: FakeEventSource[] = [];
  readonly url: string;
  private readonly listeners = new Map<string, Array<(event: MessageEvent) => void>>();

  constructor(url: string) {
    this.url = url;
    FakeEventSource.instances.push(this);
  }

  addEventListener(type: string, listener: (event: MessageEvent) => void) {
    const current = this.listeners.get(type) ?? [];
    current.push(listener);
    this.listeners.set(type, current);
  }

  close() {}
}

const product = (id: string, name: string) => ({
  id,
  name,
  category: 'Grocery',
  unitOfMeasure: 'carton',
});

const scanEntry = (overrides: Partial<ScanEntry>): ScanEntry => ({
  id: 'scan-milk',
  userId: 'user-1',
  barcode: '111',
  scannedAt: '2026-03-20T16:02:00Z',
  direction: 'stock_out',
  unitCount: 4,
  expiresAt: null,
  status: 'pending',
  productId: 'prod-milk',
  product: product('prod-milk', 'Milk'),
  committedAt: null,
  createdAt: '2026-03-20T16:02:00Z',
  ...overrides,
});

const milk = scanEntry({});
const oats = scanEntry({
  id: 'scan-oats',
  barcode: '222',
  scannedAt: '2026-03-20T16:03:00Z',
  productId: 'prod-oats',
  product: product('prod-oats', 'Oats'),
  createdAt: '2026-03-20T16:03:00Z',
});

const goneInstance: ItemInstanceWithStatus = {
  id: 'gone',
  itemId: 'item-milk',
  stockInAt: '2026-03-01T00:00:00Z',
  expiresAt: '2026-04-01T00:00:00Z',
  removedAt: null,
  removalReason: null,
  createdAt: '2026-03-01T00:00:00Z',
  expiryStatus: 'ok',
};
const keptInstance: ItemInstanceWithStatus = {
  ...goneInstance,
  id: 'kept',
  expiresAt: '2026-06-01T00:00:00Z',
  stockInAt: '2026-03-02T00:00:00Z',
  createdAt: '2026-03-02T00:00:00Z',
};

const goneLabel = `Expires ${formatExpiryDate(goneInstance.expiresAt!)}`;
const keptLabel = `Expires ${formatExpiryDate(keptInstance.expiresAt!)}`;

const jsonResponse = (body: unknown, status = 200) =>
  new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } });

const householdItem = (id: string, name: string, instanceCount: number) => ({
  item: {
    id: `item-${id}`,
    userId: 'user-1',
    productId: `prod-${id}`,
    product: product(`prod-${id}`, name),
    targetQuantity: null,
    createdAt: '2026-03-20T10:00:00Z',
  },
  instanceCount,
  nearExpiryCount: 0,
  expiredCount: 0,
  needsAttention: false,
});

const searchHousehold = [
  householdItem('empty', 'Empty Oats', 0),
  householdItem('rice', 'Plain Rice', 2),
];
const searchCatalog = [product('prod-catalog', 'Catalog Oats')];

const inventory = [
  {
    item: {
      id: 'item-milk',
      userId: 'user-1',
      productId: 'prod-milk',
      product: product('prod-milk', 'Milk'),
      targetQuantity: null,
      createdAt: '2026-03-20T10:00:00Z',
    },
    instanceCount: 2,
    nearExpiryCount: 0,
    expiredCount: 0,
    needsAttention: false,
  },
  {
    item: {
      id: 'item-oats',
      userId: 'user-1',
      productId: 'prod-oats',
      product: product('prod-oats', 'Oats'),
      targetQuantity: null,
      createdAt: '2026-03-20T10:00:00Z',
    },
    instanceCount: 1,
    nearExpiryCount: 0,
    expiredCount: 0,
    needsAttention: false,
  },
];

type PendingSet = 'both' | 'oats' | 'milk' | 'none' | 'crowd';

const crowd = Array.from({ length: 10 }, (_, index) => {
  const at = new Date(Date.parse('2026-03-20T16:00:00Z') + index * 20_000).toISOString();
  return scanEntry({
    id: `scan-crowd-${index}`,
    barcode: String(300 + index),
    scannedAt: at,
    createdAt: at,
  });
});

const pendingEntries = (which: PendingSet): ScanEntry[] => {
  if (which === 'both') return [milk, oats];
  if (which === 'oats') return [oats];
  if (which === 'milk') return [milk];
  return [];
};

interface OutcomeCase {
  name: string;
  action: 'batch' | 'confirm-milk' | 'confirm-milk-instance' | 'more-milk' | 'batch-then-milk' | 'confirm-milk-more';
  pendingAfter: PendingSet;
  commit?: { status: number; body: unknown; empty?: boolean };
  batch?: unknown;
  instancesAfter?: ItemInstanceWithStatus[];
  visible: string[];
  absent: string[];
  once?: string[];
  cardOnce?: { barcode: string; text: string };
  pendingBarcodes: string[];
  goneBarcodes: string[];
  cardReason?: { barcode: string; text: string };
  cardWithoutAlert?: string;
  instanceFetches?: number;
  dismiss?: boolean;
  quiet?: boolean;
  unlinked?: 'suggestions' | 'empty' | 'search';
  cards?: ScanEntry[];
  searchOn?: string[];
  noSearchOn?: string[];
  relink?: { name: string; remaining: string | null; status: number; body: unknown };
  searchPick?: { query: string; option: string; productId: string; absent: string[] };
  searchLook?: {
    query: string;
    present: string[];
    absent: string[];
    unmatched?: boolean;
    toggle?: boolean;
  };
  hideMore?: string;
  openMore?: boolean;
  household?: unknown;
  catalog?: unknown;
  stillSelected?: boolean;
  fixed?: boolean;
  toast?: string;
  keepsUndo?: boolean;
}

const committedMilk = { ...milk, status: 'committed' };
const fallback = 'Some scans could not be confirmed.';

const cases: OutcomeCase[] = [
  {
    name: 'batch failure reason stays on the pending card',
    action: 'batch',
    pendingAfter: 'oats',
    batch: {
      updatedCount: 2,
      committedIds: ['scan-milk'],
      failedIds: ['scan-oats'],
      failedReasons: ['Nothing on hand to take out.'],
    },
    visible: ['Nothing on hand to take out.', fallback],
    absent: [],
    once: [fallback],
    pendingBarcodes: ['222'],
    goneBarcodes: ['111'],
    cardReason: { barcode: '222', text: 'Nothing on hand to take out.' },
  },
  {
    name: 'a blank batch reason uses the fallback once',
    action: 'batch',
    pendingAfter: 'oats',
    batch: {
      updatedCount: 2,
      committedIds: ['scan-milk'],
      failedIds: ['scan-oats'],
      failedReasons: [''],
    },
    visible: [fallback],
    absent: [],
    once: [fallback],
    pendingBarcodes: ['222'],
    goneBarcodes: ['111'],
    cardWithoutAlert: '222',
  },
  {
    name: 'missing failedReasons still leaves the failed card pending',
    action: 'batch',
    pendingAfter: 'oats',
    batch: {
      updatedCount: 2,
      committedIds: ['scan-milk'],
      failedIds: ['scan-oats'],
    },
    visible: [fallback],
    absent: [],
    once: [fallback],
    pendingBarcodes: ['222'],
    goneBarcodes: ['111'],
    cardWithoutAlert: '222',
  },
  {
    name: 'a short failedReasons list does not invent the missing reason',
    action: 'batch',
    pendingAfter: 'both',
    batch: {
      updatedCount: 2,
      failedIds: ['scan-milk', 'scan-oats'],
      failedReasons: ['Nothing on hand to take out.'],
    },
    visible: ['Nothing on hand to take out.', fallback],
    absent: [],
    once: [fallback],
    pendingBarcodes: ['111', '222'],
    goneBarcodes: [],
    cardReason: { barcode: '111', text: 'Nothing on hand to take out.' },
    cardWithoutAlert: '222',
  },
  {
    name: 'instance_gone shows the server message once in the toast and drops the removed unit',
    action: 'confirm-milk-instance',
    pendingAfter: 'both',
    commit: {
      status: 409,
      body: { error: 'That one was already taken out. Pick another.', code: 'instance_gone' },
    },
    instancesAfter: [keptInstance],
    visible: ['That one was already taken out. Pick another.', keptLabel],
    absent: [goneLabel, 'No inventory item is available for this product.'],
    pendingBarcodes: ['111', '222'],
    goneBarcodes: [],
    cardWithoutAlert: '111',
    instanceFetches: 2,
    toast: 'That one was already taken out. Pick another.',
    noSearchOn: ['111'],
  },
  {
    name: 'none_on_hand shows the message and does not reload instances',
    action: 'confirm-milk-instance',
    pendingAfter: 'both',
    commit: {
      status: 409,
      body: { error: 'Nothing on hand to take out.', code: 'none_on_hand' },
    },
    visible: ['Nothing on hand to take out.'],
    absent: [],
    pendingBarcodes: ['111', '222'],
    goneBarcodes: [],
    cardWithoutAlert: '111',
    instanceFetches: 1,
    toast: 'Nothing on hand to take out.',
    noSearchOn: ['111'],
  },
  {
    name: 'a failed single commit shows the server sentence in the toast',
    action: 'confirm-milk',
    pendingAfter: 'both',
    commit: { status: 409, body: { error: 'Nothing on hand to take out.', code: 'none_on_hand' } },
    visible: ['Nothing on hand to take out.'],
    absent: [],
    pendingBarcodes: ['111', '222'],
    goneBarcodes: [],
    cardWithoutAlert: '111',
    toast: 'Nothing on hand to take out.',
    noSearchOn: ['111'],
  },
  {
    name: 'a 400 without a code still shows the server message',
    action: 'confirm-milk',
    pendingAfter: 'both',
    commit: { status: 400, body: { error: 'Choose a different product.' } },
    visible: ['Choose a different product.'],
    absent: ['No close match found.'],
    pendingBarcodes: ['111', '222'],
    goneBarcodes: [],
    cardWithoutAlert: '111',
    toast: 'Choose a different product.',
    noSearchOn: ['111'],
    instanceFetches: 0,
  },
  {
    name: 'a 500 with the picker open shows the message and does not reload',
    action: 'confirm-milk-instance',
    pendingAfter: 'both',
    commit: { status: 500, body: {} },
    visible: [SERVER_ERROR_FALLBACK, goneLabel],
    absent: [],
    pendingBarcodes: ['111', '222'],
    goneBarcodes: [],
    cardWithoutAlert: '111',
    instanceFetches: 1,
    toast: SERVER_ERROR_FALLBACK,
    noSearchOn: ['111'],
  },
  {
    name: 'unlinked stock-out with no suggestions keeps search',
    action: 'confirm-milk',
    pendingAfter: 'both',
    commit: { status: 409, body: { error: 'barcode is not linked', code: 'no_item', suggestions: [] } },
    visible: ['Which item is this?', 'No close match found.'],
    absent: ['No close match. Search or add it as new.', 'No stocked item matches this barcode.', 'barcode is not linked'],
    pendingBarcodes: ['111', '222'],
    goneBarcodes: [],
    cardWithoutAlert: '111',
    dismiss: true,
    unlinked: 'empty',
  },
  {
    name: 'unlinked stock-out with suggestions offers Link and search',
    action: 'confirm-milk',
    pendingAfter: 'both',
    commit: {
      status: 409,
      body: {
        error: 'barcode is not linked',
        code: 'no_item',
        suggestions: [{ itemId: 'item-nearby', productId: 'prod-nearby', name: 'Rolled Oats Nearby', onHand: 1 }],
      },
    },
    visible: ['Which item is this?', 'Rolled Oats Nearby'],
    absent: ['barcode is not linked', 'No close match found.'],
    pendingBarcodes: ['111', '222'],
    goneBarcodes: [],
    cardWithoutAlert: '111',
    unlinked: 'suggestions',
  },
  {
    name: 'a short shelf shows the partial note',
    action: 'confirm-milk',
    pendingAfter: 'oats',
    // testdata/contract/scan-commit-partial.json body from 3afbe09.
    commit: {
      status: 200,
      body: {
        id: 'scan-partial',
        userId: 'user-1',
        barcode: 'scan-partial',
        direction: 'stock_out',
        unitCount: 3,
        status: 'committed',
        productId: 'prod-partial',
        removedCount: 1,
        requestedCount: 3,
      },
    },
    visible: ['Only 1 was on hand, so 1 was taken out.'],
    absent: ['Only 1 were on hand'],
    pendingBarcodes: ['222'],
    goneBarcodes: ['111'],
    toast: 'Only 1 was on hand, so 1 was taken out.',
    keepsUndo: true,
  },
  {
    name: 'several units short uses the plural sentence',
    action: 'confirm-milk',
    pendingAfter: 'oats',
    commit: {
      status: 200,
      body: {
        id: 'scan-partial',
        userId: 'user-1',
        barcode: 'scan-partial',
        direction: 'stock_out',
        unitCount: 3,
        status: 'committed',
        productId: 'prod-partial',
        removedCount: 2,
        requestedCount: 3,
      },
    },
    visible: ['Only 2 were on hand, so 2 were taken out.'],
    absent: ['Only 2 was on hand'],
    pendingBarcodes: ['222'],
    goneBarcodes: ['111'],
    toast: 'Only 2 were on hand, so 2 were taken out.',
  },
  {
    name: 'equal removed and requested counts show no note',
    action: 'confirm-milk',
    pendingAfter: 'oats',
    commit: {
      status: 200,
      body: {
        id: 'scan-partial',
        userId: 'user-1',
        barcode: 'scan-partial',
        direction: 'stock_out',
        unitCount: 3,
        status: 'committed',
        productId: 'prod-partial',
        removedCount: 3,
        requestedCount: 3,
      },
    },
    visible: [],
    absent: ['was taken out', 'were taken out'],
    pendingBarcodes: ['222'],
    goneBarcodes: ['111'],
    quiet: true,
  },
  {
    name: 'a stock-in commit without the count fields shows no note',
    action: 'confirm-milk',
    pendingAfter: 'oats',
    commit: {
      status: 200,
      body: {
        id: 'scan-partial',
        userId: 'user-1',
        barcode: 'scan-partial',
        direction: 'stock_in',
        unitCount: 3,
        status: 'committed',
        productId: 'prod-partial',
      },
    },
    visible: [],
    absent: ['was taken out', 'were taken out'],
    pendingBarcodes: ['222'],
    goneBarcodes: ['111'],
    quiet: true,
  },
  {
    name: 'a removedCount of zero adds no notice',
    action: 'confirm-milk',
    pendingAfter: 'oats',
    commit: { status: 200, body: { ...committedMilk, removedCount: 0 } },
    visible: [],
    absent: ['was taken out', 'were taken out'],
    pendingBarcodes: ['222'],
    goneBarcodes: ['111'],
    quiet: true,
  },
  {
    name: 'a fractional removedCount adds no notice',
    action: 'confirm-milk',
    pendingAfter: 'oats',
    commit: { status: 200, body: { ...committedMilk, removedCount: 1.5 } },
    visible: [],
    absent: ['was taken out', 'were taken out'],
    pendingBarcodes: ['222'],
    goneBarcodes: ['111'],
    quiet: true,
  },
  {
    name: 'a null commit body adds no removal notice',
    action: 'confirm-milk',
    pendingAfter: 'oats',
    commit: { status: 200, body: null },
    visible: [],
    absent: ['was taken out', 'were taken out'],
    pendingBarcodes: ['222'],
    goneBarcodes: ['111'],
    quiet: true,
  },
  {
    name: 'an empty commit body adds no removal notice',
    action: 'confirm-milk',
    pendingAfter: 'oats',
    commit: { status: 200, body: null, empty: true },
    visible: [],
    absent: ['was taken out', 'were taken out'],
    pendingBarcodes: ['222'],
    goneBarcodes: ['111'],
    quiet: true,
  },
  {
    name: 'a non-numeric removedCount adds no removal notice',
    action: 'confirm-milk',
    pendingAfter: 'oats',
    commit: { status: 200, body: { ...committedMilk, removedCount: '1' } },
    visible: [],
    absent: ['was taken out', 'were taken out'],
    pendingBarcodes: ['222'],
    goneBarcodes: ['111'],
    quiet: true,
  },
  {
    name: 'the batch banner does not shrink under ten cards',
    action: 'batch',
    pendingAfter: 'crowd',
    cards: crowd,
    batch: {
      updatedCount: 10,
      failedIds: crowd.map((entry) => entry.id),
      failedReasons: crowd.map(() => 'Nothing on hand to take out.'),
    },
    visible: [fallback, 'Nothing on hand to take out.'],
    absent: [],
    once: [fallback],
    pendingBarcodes: crowd.map((entry) => entry.barcode),
    goneBarcodes: [],
    fixed: true,
  },
  {
    name: 'failed cards stay selected for retry',
    action: 'batch',
    pendingAfter: 'oats',
    batch: {
      updatedCount: 2,
      committedIds: ['scan-milk'],
      failedIds: ['scan-oats'],
      failedReasons: ['Nothing on hand to take out.'],
    },
    visible: [fallback],
    absent: [],
    pendingBarcodes: ['222'],
    goneBarcodes: ['111'],
    stillSelected: true,
  },
  {
    name: 'a reason for a scan that is no longer pending is dropped',
    action: 'batch',
    pendingAfter: 'oats',
    batch: {
      updatedCount: 2,
      failedIds: ['scan-milk', 'scan-oats'],
      failedReasons: ['Already confirmed by someone else.', 'Nothing on hand to take out.'],
    },
    visible: ['Nothing on hand to take out.', fallback],
    absent: ['Already confirmed by someone else.'],
    pendingBarcodes: ['222'],
    goneBarcodes: ['111'],
    cardReason: { barcode: '222', text: 'Nothing on hand to take out.' },
  },
  {
    name: 'product_unresolved shows the server sentence and search',
    action: 'confirm-milk',
    pendingAfter: 'both',
    commit: {
      status: 400,
      body: { error: 'Choose which item this is first.', code: 'product_unresolved' },
    },
    visible: ['Choose which item this is first.', 'Which item is this?'],
    absent: ['No close match found.'],
    pendingBarcodes: ['111', '222'],
    goneBarcodes: [],
    cardWithoutAlert: '111',
    unlinked: 'search',
    toast: 'Choose which item this is first.',
  },
  {
    name: 'batch product_unresolved opens search only on that card',
    action: 'batch',
    pendingAfter: 'both',
    batch: {
      updatedCount: 2,
      failedIds: ['scan-milk', 'scan-oats'],
      failedReasons: ['Choose which item this is first.', 'Nothing on hand to take out.'],
      failedCodes: ['product_unresolved', 'none_on_hand'],
    },
    visible: ['Choose which item this is first.', 'Nothing on hand to take out.', 'Which item is this?'],
    absent: ['No close match found.'],
    pendingBarcodes: ['111', '222'],
    goneBarcodes: [],
    cardReason: { barcode: '111', text: 'Choose which item this is first.' },
    searchOn: ['111'],
    noSearchOn: ['222'],
  },
  {
    name: 'a batch without failedCodes still shows only the reason',
    action: 'batch',
    pendingAfter: 'oats',
    batch: {
      updatedCount: 2,
      committedIds: ['scan-milk'],
      failedIds: ['scan-oats'],
      failedReasons: ['Nothing on hand to take out.'],
    },
    visible: ['Nothing on hand to take out.', fallback],
    absent: ['Which item is this?', 'No close match found.'],
    once: [fallback],
    pendingBarcodes: ['222'],
    goneBarcodes: ['111'],
    cardReason: { barcode: '222', text: 'Nothing on hand to take out.' },
    noSearchOn: ['222'],
  },
  {
    name: 'link_target_gone drops the stale suggestion without another commit',
    action: 'confirm-milk',
    pendingAfter: 'both',
    household: [householdItem('stale', 'Stale Oats', 1), householdItem('kept', 'Kept Oats', 2)],
    commit: {
      status: 409,
      body: {
        error: 'Which item is this?',
        code: 'no_item',
        suggestions: [
          { itemId: 'item-stale', productId: 'prod-stale', name: 'Stale Oats', onHand: 1 },
          { itemId: 'item-kept', productId: 'prod-kept', name: 'Kept Oats', onHand: 2 },
        ],
      },
    },
    relink: {
      name: 'Stale Oats',
      remaining: 'Kept Oats',
      status: 409,
      body: {
        error: "That item isn't available anymore. Pick another.",
        code: 'link_target_gone',
      },
    },
    visible: ["That item isn't available anymore. Pick another.", 'Kept Oats', 'Which item is this?'],
    absent: ['Stale Oats', 'No close match found.', 'No inventory item is available'],
    pendingBarcodes: ['111', '222'],
    goneBarcodes: [],
    cardWithoutAlert: '111',
    toast: "That item isn't available anymore. Pick another.",
    instanceFetches: 0,
    hideMore: '111',
  },
  {
    name: 'link_target_gone with nothing left keeps search and the no-match line',
    action: 'confirm-milk',
    pendingAfter: 'both',
    household: [householdItem('stale', 'Stale Oats', 1)],
    commit: {
      status: 409,
      body: {
        error: 'Which item is this?',
        code: 'no_item',
        suggestions: [
          { itemId: 'item-stale', productId: 'prod-stale', name: 'Stale Oats', onHand: 1 },
        ],
      },
    },
    relink: {
      name: 'Stale Oats',
      remaining: null,
      status: 409,
      body: {
        error: "That item isn't available anymore. Pick another.",
        code: 'link_target_gone',
      },
    },
    visible: ["That item isn't available anymore. Pick another.", 'No close match found.', 'Which item is this?'],
    absent: ['Stale Oats', 'No inventory item is available'],
    pendingBarcodes: ['111', '222'],
    goneBarcodes: [],
    hideMore: '111',
    cardWithoutAlert: '111',
    toast: "That item isn't available anymore. Pick another.",
  },
  {
    name: 'More stays hidden while the link card is open',
    action: 'confirm-milk',
    pendingAfter: 'both',
    commit: { status: 409, body: { error: 'barcode is not linked', code: 'no_item', suggestions: [] } },
    visible: ['Which item is this?', 'No close match found.'],
    absent: ['No inventory item is available'],
    pendingBarcodes: ['111', '222'],
    goneBarcodes: [],
    hideMore: '111',
    cardWithoutAlert: '111',
  },
  {
    name: 'a wiped product confirms nothing on hand only in the toast',
    action: 'confirm-milk',
    pendingAfter: 'both',
    openMore: true,
    household: [householdItem('oats', 'Oats', 1)],
    commit: { status: 409, body: { error: 'Nothing on hand to take out.', code: 'none_on_hand' } },
    visible: ['Nothing on hand to take out.'],
    absent: ['No inventory item is available'],
    pendingBarcodes: ['111', '222'],
    goneBarcodes: [],
    toast: 'Nothing on hand to take out.',
  },
  {
    name: 'a single confirm replaces the batch reason so the sentence shows once',
    action: 'batch-then-milk',
    pendingAfter: 'both',
    batch: {
      updatedCount: 2,
      failedIds: ['scan-milk', 'scan-oats'],
      failedReasons: ['Nothing on hand to take out.', 'Already confirmed by someone else.'],
    },
    commit: { status: 409, body: { error: 'Nothing on hand to take out.', code: 'none_on_hand' } },
    visible: ['Nothing on hand to take out.', 'Already confirmed by someone else.', fallback],
    absent: [],
    pendingBarcodes: ['111', '222'],
    goneBarcodes: [],
    cardWithoutAlert: '111',
    cardReason: { barcode: '222', text: 'Already confirmed by someone else.' },
    toast: 'Nothing on hand to take out.',
  },
  {
    name: 'a stock-out with no household item says nothing is on hand',
    action: 'more-milk',
    pendingAfter: 'both',
    household: [householdItem('oats', 'Oats', 1)],
    visible: ['Nothing on hand to take out.'],
    absent: ['No inventory item is available'],
    pendingBarcodes: ['111', '222'],
    goneBarcodes: [],
  },
  {
    name: 'confirming a wiped product with More open says nothing is on hand once',
    action: 'confirm-milk-more',
    pendingAfter: 'both',
    household: [householdItem('oats', 'Oats', 1)],
    commit: {
      status: 409,
      body: { error: 'Nothing on hand to take out.', code: 'none_on_hand' },
    },
    visible: ['Nothing on hand to take out.'],
    absent: ['No inventory item is available'],
    pendingBarcodes: ['111', '222'],
    goneBarcodes: [],
    cardWithoutAlert: '111',
    toast: 'Nothing on hand to take out.',
  },
  {
    name: 'an empty search lists household items, including zero on hand',
    action: 'confirm-milk',
    pendingAfter: 'both',
    commit: { status: 409, body: { error: 'barcode is not linked', code: 'no_item', suggestions: [] } },
    household: searchHousehold,
    catalog: searchCatalog,
    searchLook: { query: '', present: ['Empty Oats', 'Plain Rice'], absent: ['Catalog Oats'] },
    visible: ['Which item is this?'],
    absent: ['Catalog Oats', 'No inventory item is available'],
    pendingBarcodes: ['111', '222'],
    goneBarcodes: [],
  },
  {
    name: 'search matches a household item without matching case',
    action: 'confirm-milk',
    pendingAfter: 'both',
    commit: { status: 409, body: { error: 'barcode is not linked', code: 'no_item', suggestions: [] } },
    household: searchHousehold,
    catalog: searchCatalog,
    searchLook: { query: 'oats', present: ['Empty Oats'], absent: ['Plain Rice', 'Catalog Oats'] },
    visible: ['Which item is this?'],
    absent: ['Catalog Oats'],
    pendingBarcodes: ['111', '222'],
    goneBarcodes: [],
  },
  {
    name: 'picking a household search result commits that product',
    action: 'confirm-milk',
    pendingAfter: 'oats',
    commit: { status: 409, body: { error: 'barcode is not linked', code: 'no_item', suggestions: [] } },
    household: searchHousehold,
    catalog: searchCatalog,
    searchPick: { query: 'oats', option: 'Empty Oats', productId: 'prod-empty', absent: ['Plain Rice', 'Catalog Oats'] },
    visible: [],
    absent: ['Catalog Oats'],
    pendingBarcodes: ['222'],
    goneBarcodes: ['111'],
    quiet: true,
  },
  {
    name: 'search with no household match says nothing matched',
    action: 'confirm-milk',
    pendingAfter: 'both',
    commit: { status: 409, body: { error: 'barcode is not linked', code: 'no_item', suggestions: [] } },
    household: searchHousehold,
    catalog: searchCatalog,
    searchLook: { query: 'zzzz', present: [], absent: ['Empty Oats', 'Plain Rice', 'Catalog Oats'], unmatched: true },
    visible: ['No matching products.', 'Which item is this?'],
    absent: [],
    pendingBarcodes: ['111', '222'],
    goneBarcodes: [],
  },
  {
    name: 'the link search opens on focus and click and closes on blur',
    action: 'confirm-milk',
    pendingAfter: 'both',
    commit: { status: 409, body: { error: 'barcode is not linked', code: 'no_item', suggestions: [] } },
    household: searchHousehold,
    catalog: searchCatalog,
    searchLook: { query: '', present: ['Empty Oats', 'Plain Rice'], absent: ['Catalog Oats'], toggle: true },
    visible: ['Which item is this?'],
    absent: [],
    pendingBarcodes: ['111', '222'],
    goneBarcodes: [],
  },
  {
    name: 'an all-success batch with no failedIds shows no banner',
    action: 'batch',
    pendingAfter: 'none',
    batch: { updatedCount: 2, committedIds: ['scan-milk', 'scan-oats'] },
    visible: [],
    absent: [fallback, 'was taken out', 'were taken out'],
    pendingBarcodes: [],
    goneBarcodes: ['111', '222'],
    quiet: true,
  },
];

describe('scan commit outcomes', () => {
  beforeEach(() => {
    FakeEventSource.instances = [];
    notifications.clean();
    vi.stubGlobal('EventSource', FakeEventSource);
  });

  afterEach(() => {
    notifications.clean();
    vi.unstubAllGlobals();
  });

  it.each(cases)('$name', async (row) => {
    let pending: PendingSet = row.cards ? 'crowd' : 'both';
    let instanceFetches = 0;
    let inventoryFetches = 0;
    const commitBodies: { linkProductId?: string }[] = [];
    const fetchMock = vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url.includes('batch-commit') && init?.method === 'POST') {
        pending = row.pendingAfter;
        return Promise.resolve(jsonResponse(row.batch ?? {}));
      }
      if (url.endsWith('/commit') && init?.method === 'POST') {
        pending = row.pendingAfter;
        const parsed = init.body ? JSON.parse(String(init.body)) as { linkProductId?: string } : {};
        commitBodies.push(parsed);
        if (parsed.linkProductId && row.relink) {
          return Promise.resolve(jsonResponse(row.relink.body, row.relink.status));
        }
        if (parsed.linkProductId && row.searchPick) {
          return Promise.resolve(jsonResponse({ ...committedMilk, productId: parsed.linkProductId }));
        }
        const commit = row.commit ?? { status: 200, body: committedMilk };
        if (commit.empty) return Promise.resolve(new Response(null, { status: commit.status }));
        return Promise.resolve(jsonResponse(commit.body, commit.status));
      }
      if (url.includes('/instances')) {
        instanceFetches += 1;
        const first = [goneInstance, keptInstance];
        const body = instanceFetches === 1 ? first : (row.instancesAfter ?? first);
        return Promise.resolve(jsonResponse(body));
      }
      if (url.includes('status=pending')) {
        const body = pending === 'crowd' ? (row.cards ?? []) : pendingEntries(pending);
        return Promise.resolve(jsonResponse(body));
      }
      if (url.includes('status=flagged') || url.includes('status=qr')) return Promise.resolve(jsonResponse([]));
      if (url === '/api/inventory' || url.startsWith('/api/inventory?')) {
        inventoryFetches += 1;
        return Promise.resolve(jsonResponse(row.household ?? inventory));
      }
      if (url === '/api/products' || url.startsWith('/api/products?')) {
        return Promise.resolve(jsonResponse(row.catalog ?? []));
      }
      if (url.endsWith('/api/scanner/config')) {
        return Promise.resolve(jsonResponse({
          stockInBarcode: 'STOCK_IN',
          stockOutBarcode: 'STOCK_OUT',
          currentMode: 'stock_out',
          connected: false,
        }));
      }
      return Promise.reject(new Error(`Unexpected request: ${init?.method ?? 'GET'} ${url}`));
    });
    vi.stubGlobal('fetch', fetchMock);

    render(
      <MantineProvider>
        <Notifications />
        <ScanQueuePage />
      </MantineProvider>,
    );

    const milkCard = row.cards
      ? null
      : await screen.findByRole('article', { name: 'Scan 111' });
    if (row.cards) {
      expect(await screen.findAllByRole('article')).toHaveLength(row.cards.length);
    } else {
      expect(screen.getByRole('article', { name: 'Scan 222' })).toBeInTheDocument();
    }

    if (row.action === 'confirm-milk-instance' && milkCard) {
      fireEvent.click(within(milkCard).getByRole('button', { name: 'More' }));
      fireEvent.click(await within(milkCard).findByRole('radio', { name: goneLabel }));
    }

    if (row.keepsUndo) {
      showUndo('Stopped restocking', 'Oats stay on the list.', () => {});
      expect(await screen.findByRole('button', { name: 'Undo' })).toBeInTheDocument();
    }

    if (row.action === 'batch' || row.action === 'batch-then-milk') {
      const count = (row.cards ?? [milk, oats]).length;
      fireEvent.click(screen.getByRole('checkbox', { name: 'Select all eligible scans for batch approval' }));
      fireEvent.click(screen.getByRole('button', { name: `Confirm ${count} selected scans` }));
    }
    if (row.action === 'batch-then-milk' && milkCard) {
      await waitFor(() => {
        expect(within(milkCard).getByRole('alert')).toHaveTextContent('Nothing on hand to take out.');
      });
      fireEvent.click(within(milkCard).getByRole('button', { name: 'Confirm' }));
    } else if (row.action === 'more-milk' && milkCard) {
      fireEvent.click(within(milkCard).getByRole('button', { name: 'More' }));
    } else if (row.action === 'confirm-milk-more' && milkCard) {
      fireEvent.click(within(milkCard).getByRole('button', { name: 'More' }));
      fireEvent.click(within(milkCard).getByRole('button', { name: 'Confirm' }));
    } else if (row.action !== 'batch' && milkCard) {
      if (row.openMore) {
        fireEvent.click(within(milkCard).getByRole('button', { name: 'More' }));
        expect(within(milkCard).getByText('Nothing on hand to take out.')).toBeInTheDocument();
      }
      fireEvent.click(within(milkCard).getByRole('button', { name: 'Confirm' }));
    }

    if (row.relink && milkCard) {
      const beforeInventory = inventoryFetches;
      fireEvent.click(await within(milkCard).findByRole('button', { name: `Link ${row.relink.name}` }));
      await waitFor(() => expect(inventoryFetches).toBeGreaterThan(beforeInventory));
    }
    if (row.searchLook && milkCard) {
      const search = await within(milkCard).findByLabelText('Search products');
      if (row.searchLook.toggle) {
        fireEvent.focus(search);
        expect(search).toHaveAttribute('data-expanded', 'true');
        for (const name of row.searchLook.present) {
          expect(await within(milkCard).findByRole('option', { name, hidden: true })).toBeInTheDocument();
        }
        fireEvent.blur(search);
        expect(search.getAttribute('data-expanded')).not.toBe('true');
        fireEvent.click(search);
        expect(search).toHaveAttribute('data-expanded', 'true');
      }
      if (row.searchLook.query !== '') {
        fireEvent.change(search, { target: { value: row.searchLook.query } });
      } else if (!row.searchLook.toggle) {
        fireEvent.focus(search);
      }
      for (const name of row.searchLook.present) {
        expect(await within(milkCard).findByRole('option', { name, hidden: true })).toBeInTheDocument();
      }
      for (const name of row.searchLook.absent) {
        expect(within(milkCard).queryByRole('option', { name, hidden: true })).not.toBeInTheDocument();
      }
      if (row.searchLook.unmatched) {
        expect(within(milkCard).getByText('No matching products.')).toBeInTheDocument();
      }
    }
    if (row.searchPick && milkCard) {
      const search = await within(milkCard).findByLabelText('Search products');
      fireEvent.change(search, { target: { value: row.searchPick.query } });
      fireEvent.focus(search);
      fireEvent.click(search);
      expect(await within(milkCard).findByRole('option', { name: row.searchPick.option, hidden: true })).toBeInTheDocument();
      for (const name of row.searchPick.absent) {
        expect(within(milkCard).queryByRole('option', { name, hidden: true })).not.toBeInTheDocument();
      }
      fireEvent.click(within(milkCard).getByRole('option', { name: row.searchPick.option, hidden: true }));
    }

    await waitFor(() => {
      for (const barcode of row.goneBarcodes) {
        expect(screen.queryByRole('article', { name: `Scan ${barcode}` })).not.toBeInTheDocument();
      }
      for (const text of row.visible) {
        expect(screen.getAllByText(text).length).toBeGreaterThan(0);
      }
      for (const text of row.absent) {
        expect(screen.queryByText(text, { exact: false })).not.toBeInTheDocument();
      }
      expect(screen.queryByText('No inventory item is available', { exact: false })).not.toBeInTheDocument();
      for (const text of row.once ?? []) {
        expect(screen.getAllByText(text)).toHaveLength(1);
      }
      if (row.cardWithoutAlert !== undefined) {
        const card = screen.getByRole('article', { name: `Scan ${row.cardWithoutAlert}` });
        expect(within(card).queryByRole('alert')).not.toBeInTheDocument();
      }
      if (row.quiet) {
        const extra = screen.getAllByRole('alert').filter((alert) => !alert.textContent?.includes('Mode:'));
        expect(extra).toEqual([]);
      }
      if (row.instanceFetches !== undefined) {
        expect(instanceFetches).toBe(row.instanceFetches);
      }
      if (row.instancesAfter?.length === 1) {
        expect(screen.getByRole('radio', { name: keptLabel })).toBeInTheDocument();
      }
      if (row.fixed) {
        const pinned = screen.getAllByRole('alert').filter((alert) => {
          const text = alert.textContent ?? '';
          return text.includes(fallback) || text.startsWith('Only ');
        });
        expect(pinned.length).toBeGreaterThan(0);
        for (const notice of pinned) {
          expect(notice).toHaveStyle({ flexShrink: '0' });
        }
      }
      if (row.toast !== undefined) {
        const leaves = screen.getAllByText(row.toast).filter((node) => node.childElementCount === 0);
        expect(leaves).toHaveLength(1);
        expect(leaves[0]?.closest('.restock-toast')).not.toBeNull();
        expect(leaves[0]?.textContent).toBe(row.toast);
        for (const article of screen.queryAllByRole('article')) {
          expect(within(article).queryByText(row.toast)).not.toBeInTheDocument();
        }
      }
      if (row.keepsUndo) {
        expect(screen.getByRole('button', { name: 'Undo' })).toBeInTheDocument();
      }
      if (row.stillSelected) {
        expect(screen.getByRole('button', { name: `Confirm ${row.pendingBarcodes.length} selected scans` })).toBeInTheDocument();
        for (const barcode of row.pendingBarcodes) {
          const card = screen.getByRole('article', { name: `Scan ${barcode}` });
          expect(within(card).getByRole('checkbox')).toBeChecked();
        }
      }
    });

    for (const barcode of row.pendingBarcodes) {
      const card = screen.getByRole('article', { name: `Scan ${barcode}` });
      expect(within(card).getByRole('button', { name: 'Confirm' })).toBeInTheDocument();
    }
    if (row.cardReason) {
      const card = screen.getByRole('article', { name: `Scan ${row.cardReason.barcode}` });
      expect(within(card).getByRole('alert')).toHaveTextContent(row.cardReason.text);
    }
    if (row.cardOnce) {
      const card = screen.getByRole('article', { name: `Scan ${row.cardOnce.barcode}` });
      expect(within(card).getAllByText(row.cardOnce.text)).toHaveLength(1);
    }
    if (row.unlinked) {
      const card = screen.getByRole('article', { name: 'Scan 111' });
      expect(within(card).getByRole('region', { name: 'Which item is this?' })).toBeInTheDocument();
      expect(within(card).getByLabelText('Search products')).toBeInTheDocument();
      expect(within(card).queryByText('Not in your pantry', { exact: false })).not.toBeInTheDocument();
      expect(within(card).queryByText('Nothing on hand', { exact: false })).not.toBeInTheDocument();
      if (row.unlinked === 'empty') {
        expect(within(card).getByText('No close match found.')).toBeInTheDocument();
        expect(within(card).queryByRole('button', { name: /^Link / })).not.toBeInTheDocument();
      } else if (row.unlinked === 'search') {
        expect(within(card).queryByText('No close match found.')).not.toBeInTheDocument();
        expect(within(card).queryByRole('button', { name: /^Link / })).not.toBeInTheDocument();
      } else {
        expect(within(card).getByRole('button', { name: 'Link Rolled Oats Nearby' })).toBeInTheDocument();
        expect(within(card).queryByText('No close match found.')).not.toBeInTheDocument();
      }
    }
    for (const barcode of row.searchOn ?? []) {
      const card = screen.getByRole('article', { name: `Scan ${barcode}` });
      expect(within(card).getByRole('region', { name: 'Which item is this?' })).toBeInTheDocument();
      expect(within(card).getByLabelText('Search products')).toBeInTheDocument();
      expect(within(card).queryByText('No close match found.')).not.toBeInTheDocument();
    }
    for (const barcode of row.noSearchOn ?? []) {
      const card = screen.getByRole('article', { name: `Scan ${barcode}` });
      expect(within(card).queryByLabelText('Search products')).not.toBeInTheDocument();
      expect(within(card).queryByRole('region', { name: 'Which item is this?' })).not.toBeInTheDocument();
    }
    if (row.relink) {
      const card = screen.getByRole('article', { name: 'Scan 111' });
      expect(within(card).queryByRole('button', { name: `Link ${row.relink.name}` })).not.toBeInTheDocument();
      if (row.relink.remaining === null) {
        expect(within(card).getByText('No close match found.')).toBeInTheDocument();
        expect(within(card).getByLabelText('Search products')).toBeInTheDocument();
        expect(within(card).queryByRole('button', { name: /^Link / })).not.toBeInTheDocument();
      } else {
        expect(within(card).getByRole('button', { name: `Link ${row.relink.remaining}` })).toBeInTheDocument();
        const search = within(card).getByLabelText('Search products');
        fireEvent.focus(search);
        expect(within(card).queryByRole('option', { name: row.relink.name, hidden: true })).not.toBeInTheDocument();
        expect(await within(card).findByRole('option', { name: row.relink.remaining, hidden: true })).toBeInTheDocument();
      }
      expect(commitBodies).toEqual([{}, { linkProductId: 'prod-stale' }]);
    }
    if (row.hideMore) {
      const card = screen.getByRole('article', { name: `Scan ${row.hideMore}` });
      expect(within(card).queryByRole('button', { name: 'More' })).not.toBeInTheDocument();
    }
    if (row.searchPick) {
      expect(commitBodies).toContainEqual({ linkProductId: row.searchPick.productId });
    }
    if (row.dismiss) {
      expect(screen.getByRole('button', { name: 'Dismiss' })).toBeInTheDocument();
      expect(screen.queryByRole('button', { name: /^Link / })).not.toBeInTheDocument();
    }
  });

  it('keeps an open link search when a different card commits', async () => {
    let pending: PendingSet = 'both';
    const household = [{
      item: {
        id: 'item-empty',
        userId: 'user-1',
        productId: 'prod-empty',
        product: product('prod-empty', 'Empty Oats'),
        targetQuantity: null,
        createdAt: '2026-03-20T10:00:00Z',
      },
      instanceCount: 0,
      nearExpiryCount: 0,
      expiredCount: 0,
      needsAttention: false,
    }];
    const fetchMock = vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url.endsWith('/commit') && init?.method === 'POST') {
        if (url.includes('scan-oats')) {
          pending = 'milk';
          return Promise.resolve(jsonResponse({ ...oats, status: 'committed' }));
        }
        return Promise.resolve(jsonResponse({
          error: 'barcode is not linked',
          code: 'no_item',
          suggestions: [],
        }, 409));
      }
      if (url.includes('status=pending')) return Promise.resolve(jsonResponse(pendingEntries(pending)));
      if (url.includes('status=flagged') || url.includes('status=qr')) return Promise.resolve(jsonResponse([]));
      if (url === '/api/inventory' || url.startsWith('/api/inventory?')) return Promise.resolve(jsonResponse(household));
      if (url === '/api/products' || url.startsWith('/api/products?')) return Promise.resolve(jsonResponse([product('prod-catalog', 'Catalog Oats')]));
      if (url.endsWith('/api/scanner/config')) {
        return Promise.resolve(jsonResponse({
          stockInBarcode: 'STOCK_IN',
          stockOutBarcode: 'STOCK_OUT',
          currentMode: 'stock_out',
          connected: false,
        }));
      }
      return Promise.reject(new Error(`Unexpected request: ${init?.method ?? 'GET'} ${url}`));
    });
    vi.stubGlobal('fetch', fetchMock);

    render(<MantineProvider><ScanQueuePage /></MantineProvider>);
    const milkCard = await screen.findByRole('article', { name: 'Scan 111' });
    const oatsCard = screen.getByRole('article', { name: 'Scan 222' });
    fireEvent.click(within(milkCard).getByRole('button', { name: 'Confirm' }));
    const search = await within(milkCard).findByLabelText('Search products');
    fireEvent.change(search, { target: { value: 'Oats' } });
    expect(await within(milkCard).findByRole('option', { name: 'Empty Oats', hidden: true })).toBeInTheDocument();

    fireEvent.click(within(oatsCard).getByRole('button', { name: 'Confirm' }));

    await waitFor(() => {
      expect(screen.queryByRole('article', { name: 'Scan 222' })).not.toBeInTheDocument();
    });
    const stillOpen = screen.getByRole('article', { name: 'Scan 111' });
    expect(within(stillOpen).getByRole('region', { name: 'Which item is this?' })).toBeInTheDocument();
    const stillSearch = within(stillOpen).getByLabelText('Search products');
    expect(stillSearch).toHaveValue('Oats');
    fireEvent.focus(stillSearch);
    expect(await within(stillOpen).findByRole('option', { name: 'Empty Oats', hidden: true })).toBeInTheDocument();
    expect(within(stillOpen).queryByRole('option', { name: 'Catalog Oats', hidden: true })).not.toBeInTheDocument();
  });
});
