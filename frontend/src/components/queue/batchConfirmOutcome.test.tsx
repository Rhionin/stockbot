import { useEffect, useState } from 'react';
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

type PendingSet = 'both' | 'oats' | 'milk' | 'none' | 'crowd' | 'kept';

const crowd = Array.from({ length: 10 }, (_, index) => {
  const at = new Date(Date.parse('2026-03-20T16:00:00Z') + index * 20_000).toISOString();
  return scanEntry({
    id: `scan-crowd-${index}`,
    barcode: String(300 + index),
    scannedAt: at,
    createdAt: at,
  });
});

const pendingEntries = (which: PendingSet, kept: ScanEntry[]): ScanEntry[] => {
  if (which === 'both') return [milk, oats];
  if (which === 'oats') return [oats];
  if (which === 'milk') return [milk];
  if (which === 'kept') return kept;
  return [];
};

interface OutcomeCase {
  name: string;
  action: 'batch' | 'confirm-milk' | 'confirm-milk-instance' | 'more-milk' | 'batch-then-milk' | 'confirm-milk-more' | 'batch-then-one' | 'batch-then-batch' | 'batch-then-remove' | 'batch-then-scan' | 'batch-then-tab' | 'batch-then-leave';
  pendingAfter: PendingSet;
  pendingAfterConfirm?: PendingSet;
  pendingAfterScan?: PendingSet;
  pendingAfterRemove?: PendingSet;
  pendingSequence?: PendingSet[];
  holdSecondBatch?: boolean;
  bannerGone?: boolean;
  batches?: unknown[];
  thenSee?: string[];
  thenGone?: string[];
  followBarcode?: string;
  secondConfirmCount?: number;
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
  kept?: ScanEntry[];
  visibleCards?: number;
  confirmCount?: number;
  bannerExact?: string;
  bannerKind?: 'success' | 'failure';
  bannerLine?: string;
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

// testdata/contract/scan-batch-commit-partial.json from PR #300 head b005813.
// Inlined because that file is not on this branch.
const scanBatchCommitPartialJson = `{
  "status": 200,
  "note": "One batch: a full stock-out (3 on hand, unitCount 2), a partial stock-out (1 on hand, unitCount 3), a stock-in of 2, and a stock-out with nothing on hand. stockOutCounts has one entry per committed stock-out, including the full removal. The stock-in and the failed card are absent. The partial card stays in committedIds. failedReasons and failedCodes stay parallel to failedIds.",
  "request": {
    "scanEntryIds": ["scan-batch-full", "scan-batch-partial", "scan-batch-in", "scan-batch-none"],
    "commit": true
  },
  "body": {
    "updatedCount": 4,
    "committedIds": ["scan-batch-full", "scan-batch-partial", "scan-batch-in"],
    "failedIds": ["scan-batch-none"],
    "failedReasons": ["Nothing on hand to take out."],
    "failedCodes": ["none_on_hand"],
    "stockOutCounts": {
      "scan-batch-full": { "removedCount": 2, "requestedCount": 2 },
      "scan-batch-partial": { "removedCount": 1, "requestedCount": 3 }
    }
  }
}
`;

interface BatchContract {
  body: {
    updatedCount: number;
    committedIds: string[];
    failedIds: string[];
    failedReasons: string[];
    failedCodes: string[];
    stockOutCounts: Record<string, { removedCount: number; requestedCount: number }>;
  };
}

const batchContract = JSON.parse(scanBatchCommitPartialJson) as BatchContract;
const fullCount = batchContract.body.stockOutCounts['scan-batch-full'];
const partialCount = batchContract.body.stockOutCounts['scan-batch-partial'];

const fixtureCard = (
  id: string,
  name: string,
  direction: 'stock_in' | 'stock_out',
  unitCount: number,
): ScanEntry => scanEntry({
  id,
  barcode: id,
  direction,
  unitCount,
  productId: `prod-${id}`,
  product: product(`prod-${id}`, name),
});

const chips = fixtureCard('scan-batch-partial', 'Tortilla chips', 'stock_out', partialCount.requestedCount);
const fullCard = fixtureCard('scan-batch-full', 'Batch Full', 'stock_out', fullCount.requestedCount);
const stockInCard = fixtureCard('scan-batch-in', 'Batch In', 'stock_in', 2);
const emptyCard = fixtureCard('scan-batch-none', 'Batch Empty', 'stock_out', 1);
const wereChips = fixtureCard('scan-batch-partial', 'Tortilla chips', 'stock_out', 5);
const secondPartial = fixtureCard('scan-batch-second', 'Rolled oats', 'stock_out', 4);

const wasLine = 'Only 1 of 3 Tortilla chips was on hand, so 1 was taken out.';
const wereLine = 'Only 2 of 5 Tortilla chips were on hand, so 2 were taken out.';
const manyLine = '2 cards had fewer on hand than scanned, so only what was on hand was taken out.';
const unnamedLine = '1 card had fewer on hand than scanned, so only what was on hand was taken out.';
const laterChips = fixtureCard('scan-batch-later', 'Tortilla chips', 'stock_out', 5);
const nameless = scanEntry({
  id: 'scan-batch-partial',
  barcode: 'scan-batch-partial',
  direction: 'stock_out',
  unitCount: partialCount.requestedCount,
  productId: 'prod-scan-batch-partial',
  product: product('prod-scan-batch-partial', ''),
});
const chipsPartial = {
  updatedCount: 1,
  committedIds: ['scan-batch-partial'],
  stockOutCounts: { 'scan-batch-partial': partialCount },
};
const laterPartial = {
  updatedCount: 1,
  committedIds: ['scan-batch-later'],
  stockOutCounts: {
    'scan-batch-later': { removedCount: 2, requestedCount: 5 },
  },
};

const partialBatchRows = (): OutcomeCase[] => {
  const omitted = { ...batchContract.body } as Record<string, unknown>;
  delete omitted.stockOutCounts;
  return [
    {
      name: 'one partial card names the item in the batch banner',
      action: 'batch',
      pendingAfter: 'none',
      cards: [chips],
      batch: {
        updatedCount: 1,
        committedIds: ['scan-batch-partial'],
        stockOutCounts: { 'scan-batch-partial': partialCount },
      },
      visible: [wasLine],
      absent: [fallback, wereLine, manyLine, 'were on hand', 'were taken out'],
      once: [wasLine],
      bannerKind: 'success',
      bannerLine: wasLine,
      pendingBarcodes: [],
      goneBarcodes: ['scan-batch-partial'],
      fixed: true,
      quiet: true,
    },
    {
      name: 'one partial card with more than one on hand uses were',
      action: 'batch',
      pendingAfter: 'none',
      cards: [wereChips],
      batch: {
        updatedCount: 1,
        committedIds: ['scan-batch-partial'],
        stockOutCounts: {
          'scan-batch-partial': { removedCount: 2, requestedCount: 5 },
        },
      },
      visible: [wereLine],
      absent: [fallback, wasLine, manyLine, 'was on hand', 'was taken out'],
      once: [wereLine],
      bannerKind: 'success',
      bannerLine: wereLine,
      pendingBarcodes: [],
      goneBarcodes: ['scan-batch-partial'],
      fixed: true,
      quiet: true,
    },
    {
      name: 'two partial cards share one count line',
      action: 'batch',
      pendingAfter: 'none',
      cards: [chips, secondPartial],
      batch: {
        updatedCount: 2,
        committedIds: ['scan-batch-partial', 'scan-batch-second'],
        stockOutCounts: {
          'scan-batch-partial': partialCount,
          'scan-batch-second': { removedCount: 2, requestedCount: 4 },
        },
      },
      visible: [manyLine],
      absent: [fallback, wasLine, wereLine, 'Tortilla chips', 'Rolled oats'],
      once: [manyLine],
      bannerKind: 'success',
      bannerLine: manyLine,
      pendingBarcodes: [],
      goneBarcodes: ['scan-batch-partial', 'scan-batch-second'],
      fixed: true,
      quiet: true,
    },
    {
      name: 'a batch of full stock-outs adds no partial line',
      action: 'batch',
      pendingAfter: 'none',
      cards: [fullCard, chips],
      batch: {
        updatedCount: 2,
        committedIds: ['scan-batch-full', 'scan-batch-partial'],
        stockOutCounts: {
          'scan-batch-full': fullCount,
          'scan-batch-partial': { removedCount: partialCount.requestedCount, requestedCount: partialCount.requestedCount },
        },
      },
      visible: [],
      absent: [fallback, wasLine, wereLine, manyLine, 'was taken out', 'were taken out', 'cards had fewer'],
      pendingBarcodes: [],
      goneBarcodes: ['scan-batch-full', 'scan-batch-partial'],
      quiet: true,
    },
    {
      name: 'a batch that omits stockOutCounts adds no partial line',
      action: 'batch',
      pendingAfter: 'kept',
      cards: [fullCard, chips, stockInCard, emptyCard],
      kept: [emptyCard],
      visibleCards: 3,
      confirmCount: 3,
      batch: omitted,
      visible: ['Nothing on hand to take out.', fallback],
      absent: [wasLine, wereLine, manyLine, 'was taken out', 'were taken out', 'cards had fewer'],
      once: [fallback, 'Nothing on hand to take out.'],
      bannerExact: fallback,
      pendingBarcodes: ['scan-batch-none'],
      goneBarcodes: ['scan-batch-full', 'scan-batch-partial', 'scan-batch-in'],
      cardReason: { barcode: 'scan-batch-none', text: 'Nothing on hand to take out.' },
    },
    {
      name: 'a mixed batch keeps the failure reason and adds the partial line once',
      action: 'batch',
      pendingAfter: 'kept',
      cards: [fullCard, chips, stockInCard, emptyCard],
      kept: [emptyCard],
      visibleCards: 3,
      confirmCount: 3,
      batch: batchContract.body,
      visible: ['Nothing on hand to take out.', fallback, wasLine],
      absent: [wereLine, manyLine, 'were taken out', 'cards had fewer'],
      once: [fallback, wasLine, 'Nothing on hand to take out.'],
      bannerKind: 'failure',
      bannerLine: wasLine,
      pendingBarcodes: ['scan-batch-none'],
      goneBarcodes: ['scan-batch-full', 'scan-batch-partial', 'scan-batch-in'],
      cardReason: { barcode: 'scan-batch-none', text: 'Nothing on hand to take out.' },
      fixed: true,
    },
    {
      name: 'a partial card with no name uses the count line',
      action: 'batch',
      pendingAfter: 'none',
      cards: [nameless],
      batch: chipsPartial,
      visible: [unnamedLine],
      absent: ['Unknown product', wasLine, wereLine, manyLine, fallback],
      once: [unnamedLine],
      bannerKind: 'success',
      bannerLine: unnamedLine,
      pendingBarcodes: [],
      goneBarcodes: ['scan-batch-partial'],
      fixed: true,
      quiet: true,
    },
    {
      name: 'a later single confirm clears the partial line',
      action: 'batch-then-one',
      pendingAfter: 'kept',
      pendingAfterConfirm: 'none',
      cards: [chips, laterChips],
      kept: [laterChips],
      confirmCount: 2,
      followBarcode: 'scan-batch-later',
      thenSee: [wasLine],
      thenGone: ['scan-batch-partial'],
      batch: chipsPartial,
      commit: { status: 200, body: { id: 'scan-batch-later', status: 'committed' } },
      visible: [],
      absent: [wasLine, wereLine, manyLine, fallback, 'was taken out', 'were taken out', 'cards had fewer'],
      pendingBarcodes: [],
      goneBarcodes: ['scan-batch-partial', 'scan-batch-later'],
      quiet: true,
    },
    {
      name: 'the next batch replaces the partial line',
      action: 'batch-then-batch',
      pendingAfter: 'kept',
      pendingSequence: ['kept', 'none'],
      batches: [chipsPartial, laterPartial],
      cards: [chips, laterChips],
      kept: [laterChips],
      confirmCount: 2,
      secondConfirmCount: 1,
      thenSee: [wasLine],
      thenGone: ['scan-batch-partial'],
      visible: [wereLine],
      absent: [wasLine, manyLine, fallback, 'was on hand', 'was taken out'],
      once: [wereLine],
      bannerKind: 'success',
      bannerLine: wereLine,
      pendingBarcodes: [],
      goneBarcodes: ['scan-batch-partial', 'scan-batch-later'],
      fixed: true,
      quiet: true,
    },
    {
      name: 'a new batch clears the partial line when it starts',
      action: 'batch-then-batch',
      pendingAfter: 'kept',
      pendingSequence: ['kept', 'none'],
      holdSecondBatch: true,
      batches: [chipsPartial, { updatedCount: 1, committedIds: ['scan-batch-later'] }],
      cards: [chips, laterChips],
      kept: [laterChips],
      confirmCount: 2,
      secondConfirmCount: 1,
      thenSee: [wasLine],
      thenGone: ['scan-batch-partial'],
      visible: [],
      absent: [wasLine, fallback, 'cards had fewer'],
      pendingBarcodes: [],
      goneBarcodes: ['scan-batch-partial', 'scan-batch-later'],
      bannerGone: true,
      quiet: true,
    },
    {
      name: 'removing the failed card clears the partial line',
      action: 'batch-then-remove',
      pendingAfter: 'kept',
      pendingAfterRemove: 'none',
      cards: [chips, emptyCard],
      kept: [emptyCard],
      confirmCount: 2,
      followBarcode: 'scan-batch-none',
      thenSee: [wasLine, fallback],
      thenGone: ['scan-batch-partial'],
      batch: {
        updatedCount: 2,
        committedIds: ['scan-batch-partial'],
        failedIds: ['scan-batch-none'],
        failedReasons: ['Nothing on hand to take out.'],
        failedCodes: ['none_on_hand'],
        stockOutCounts: { 'scan-batch-partial': partialCount },
      },
      visible: [],
      absent: [wasLine, fallback, 'Nothing on hand to take out.'],
      pendingBarcodes: [],
      goneBarcodes: ['scan-batch-partial', 'scan-batch-none'],
      bannerGone: true,
      quiet: true,
    },
    {
      name: 'a new scan clears the partial line',
      action: 'batch-then-scan',
      pendingAfter: 'none',
      pendingAfterScan: 'kept',
      cards: [chips],
      kept: [fixtureCard('444444444444', 'Fresh loaf', 'stock_out', 1)],
      batch: chipsPartial,
      thenSee: [wasLine],
      visible: ['Fresh loaf'],
      absent: [wasLine, fallback],
      pendingBarcodes: ['444444444444'],
      goneBarcodes: ['scan-batch-partial'],
      bannerGone: true,
      quiet: true,
    },
    {
      name: 'switching tabs clears the partial line',
      action: 'batch-then-tab',
      pendingAfter: 'none',
      cards: [chips],
      batch: chipsPartial,
      thenSee: [wasLine],
      visible: [],
      absent: [wasLine, fallback],
      pendingBarcodes: [],
      goneBarcodes: ['scan-batch-partial'],
      bannerGone: true,
      quiet: true,
    },
    {
      name: 'leaving the queue clears the partial line',
      action: 'batch-then-leave',
      pendingAfter: 'none',
      cards: [chips],
      batch: chipsPartial,
      thenSee: [wasLine],
      visible: ['Left the queue'],
      absent: [wasLine, fallback],
      pendingBarcodes: [],
      goneBarcodes: ['scan-batch-partial'],
      bannerGone: true,
    },
  ];
};

const outcomeCases: OutcomeCase[] = [...cases, ...partialBatchRows()];

const QueueOrLeft = ({ leaveRef }: { leaveRef: { current: () => void } }) => {
  const [here, setHere] = useState(true);
  useEffect(() => {
    leaveRef.current = () => setHere(false);
  }, [leaveRef]);
  if (!here) return <h1>Left the queue</h1>;
  return <ScanQueuePage />;
};

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

  it.each(outcomeCases)('$name', async (row) => {
    let pending: PendingSet = row.cards ? 'crowd' : 'both';
    let batchRound = 0;
    let releaseSecondBatch = () => {};
    let instanceFetches = 0;
    let inventoryFetches = 0;
    const commitBodies: { linkProductId?: string }[] = [];
    const fetchMock = vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url.includes('batch-commit') && init?.method === 'POST') {
        const round = batchRound;
        pending = row.pendingSequence?.[round] ?? row.pendingAfter;
        const body = row.batches?.[round] ?? row.batch ?? {};
        batchRound += 1;
        if (row.holdSecondBatch && round === 1) {
          return new Promise((resolve) => {
            releaseSecondBatch = () => resolve(jsonResponse(body));
          });
        }
        return Promise.resolve(jsonResponse(body));
      }
      if (url === '/api/scans' && init?.method === 'POST') {
        if (row.pendingAfterScan !== undefined) pending = row.pendingAfterScan;
        return Promise.resolve(jsonResponse({ id: 'scan-new', status: 'pending' }));
      }
      if (init?.method === 'PATCH' && /\/api\/scans\/[^/]+$/.test(url)) {
        pending = row.pendingAfterRemove ?? pending;
        return Promise.resolve(jsonResponse({ status: 'cancelled' }));
      }
      if (url.endsWith('/api/scanner/mode') && init?.method === 'POST') {
        return Promise.resolve(jsonResponse({ mode: 'stock_in' }));
      }
      if (url.endsWith('/commit') && init?.method === 'POST') {
        pending = row.pendingAfterConfirm ?? row.pendingAfter;
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
        const body = pending === 'crowd' ? (row.cards ?? []) : pendingEntries(pending, row.kept ?? []);
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

    const leaveRef = { current: () => {} };
    render(
      <MantineProvider>
        <Notifications />
        {row.action === 'batch-then-leave' ? <QueueOrLeft leaveRef={leaveRef} /> : <ScanQueuePage />}
      </MantineProvider>,
    );

    const milkCard = row.cards
      ? null
      : await screen.findByRole('article', { name: 'Scan 111' });
    if (row.cards) {
      const expectedCards = row.visibleCards ?? row.cards.length;
      if (row.visibleCards !== undefined) {
        await waitFor(() => {
          expect(screen.getAllByRole('article')).toHaveLength(expectedCards);
        });
      } else {
        expect(await screen.findAllByRole('article')).toHaveLength(expectedCards);
      }
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

    if (row.action === 'batch' || row.action === 'batch-then-milk' || row.action === 'batch-then-one' || row.action === 'batch-then-batch' || row.action === 'batch-then-remove' || row.action === 'batch-then-scan' || row.action === 'batch-then-tab' || row.action === 'batch-then-leave') {
      const count = row.confirmCount ?? (row.cards ?? [milk, oats]).length;
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

    if (row.action === 'batch-then-one' || row.action === 'batch-then-batch' || row.action === 'batch-then-remove' || row.action === 'batch-then-scan' || row.action === 'batch-then-tab' || row.action === 'batch-then-leave') {
      await waitFor(() => {
        for (const text of row.thenSee ?? []) expect(screen.getByText(text)).toBeInTheDocument();
        for (const barcode of row.thenGone ?? []) {
          expect(screen.queryByRole('article', { name: `Scan ${barcode}` })).not.toBeInTheDocument();
        }
      });
    }
    if (row.action === 'batch-then-one') {
      const card = screen.getByRole('article', { name: `Scan ${row.followBarcode}` });
      fireEvent.click(within(card).getByRole('button', { name: 'Confirm' }));
    }
    if (row.action === 'batch-then-batch') {
      fireEvent.click(screen.getByRole('checkbox', { name: 'Select all eligible scans for batch approval' }));
      fireEvent.click(screen.getByRole('button', { name: `Confirm ${row.secondConfirmCount} selected scans` }));
    }
    if (row.holdSecondBatch) {
      await waitFor(() => {
        expect(screen.queryByText(row.thenSee?.[0] ?? '')).not.toBeInTheDocument();
        expect(document.querySelector('.scan-batch-banner--success')).toBeNull();
      });
      releaseSecondBatch();
    }
    if (row.action === 'batch-then-remove') {
      const card = screen.getByRole('article', { name: `Scan ${row.followBarcode}` });
      fireEvent.click(within(card).getByRole('button', { name: 'Remove' }));
    }
    if (row.action === 'batch-then-scan') {
      const input = screen.getByLabelText('Barcode scanner input');
      fireEvent.change(input, { target: { value: '444444444444' } });
      fireEvent.keyDown(input, { key: 'Enter' });
    }
    if (row.action === 'batch-then-tab') {
      fireEvent.click(screen.getByRole('tab', { name: 'Stock in' }));
    }
    if (row.action === 'batch-then-leave') {
      leaveRef.current();
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
        const extra = screen.queryAllByRole('alert').filter((alert) => !alert.textContent?.includes('Mode:'));
        expect(extra).toEqual([]);
      }
      if (row.bannerGone) {
        expect(document.querySelector('.scan-batch-banner--success')).toBeNull();
        expect(document.querySelector('.scan-batch-banner')).toBeNull();
      }
      if (row.instanceFetches !== undefined) {
        expect(instanceFetches).toBe(row.instanceFetches);
      }
      if (row.instancesAfter?.length === 1) {
        expect(screen.getByRole('radio', { name: keptLabel })).toBeInTheDocument();
      }
      if (row.fixed) {
        const pinned = [
          ...screen.getAllByRole('alert').filter((alert) => {
            const text = alert.textContent ?? '';
            return text.includes(fallback) || text.startsWith('Only ') || text.includes('cards had fewer');
          }),
          ...document.querySelectorAll('.scan-batch-banner--success, .scan-batch-partial'),
        ];
        expect(pinned.length).toBeGreaterThan(0);
        for (const notice of pinned) {
          expect(notice).toHaveStyle({ flexShrink: '0' });
        }
      }
      if (row.bannerKind === 'success') {
        const line = screen.getByText(row.bannerLine ?? '');
        expect(line).toHaveClass('scan-batch-banner--success');
        expect(line).toHaveAttribute('role', 'status');
        expect(line).not.toHaveAttribute('role', 'alert');
        expect(line.className).not.toMatch(/error|red/i);
        expect(line.closest('[role="alert"]')).toBeNull();
        expect(line.textContent?.replace(/\s+/g, ' ').trim()).toBe(row.bannerLine);
        const failureBanners = screen.queryAllByRole('alert').filter((alert) => alert.classList.contains('scan-batch-banner'));
        expect(failureBanners).toEqual([]);
      }
      if (row.bannerKind === 'failure') {
        const alerts = screen.getAllByRole('alert').filter((alert) => alert.classList.contains('scan-batch-banner'));
        expect(alerts).toHaveLength(1);
        const bannerText = alerts[0]?.textContent?.replace(/\s+/g, ' ').trim();
        expect(bannerText?.startsWith(fallback)).toBe(true);
        expect(bannerText).toContain(row.bannerLine);
        const line = screen.getByText(row.bannerLine ?? '');
        expect(alerts[0]?.contains(line)).toBe(true);
        expect(line.closest('[role="alert"]')).toBe(alerts[0]);
        expect(line).not.toHaveAttribute('role', 'alert');
        expect(line.className).not.toMatch(/error|red/i);
        expect(document.querySelectorAll('.scan-batch-banner--success')).toHaveLength(0);
        const head = within(alerts[0] as HTMLElement).getByText(fallback);
        expect(head.compareDocumentPosition(line) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
      }
      if (row.bannerExact !== undefined) {
        const alerts = screen.getAllByRole('alert').filter((alert) => alert.classList.contains('scan-batch-banner'));
        expect(alerts).toHaveLength(1);
        expect(alerts[0]?.textContent?.replace(/\s+/g, ' ').trim()).toBe(row.bannerExact);
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
      if (url.includes('status=pending')) return Promise.resolve(jsonResponse(pendingEntries(pending, [])));
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
