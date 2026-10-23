import type { ReactElement } from 'react';
import { StyleSheet } from 'react-native';
import { act, fireEvent, waitFor, within } from '@testing-library/react-native';
import { SafeAreaProvider } from 'react-native-safe-area-context';
import {
  BATCH_FAILURE_FALLBACK,
  BUSY_SENTENCE,
  NOTHING_ON_HAND_BODY,
  NOTHING_ON_HAND_TITLE,
  NO_SPACE_SENTENCE,
  type BatchCommitResponse,
} from '../../../frontend/src/api/contract';
import type { InventoryItem, PantryLinkSuggestion, ScanEntry } from '../../../frontend/src/types';
import { ApiError, type StockbotClient } from '../api/client';
import { renderScreen } from '../test/renderScreen';
import { colors } from '../ui/theme';
import { QueueScreen } from './QueueScreen';

const entry = (overrides: Partial<ScanEntry> = {}): ScanEntry => ({
  id: 'scan-1',
  userId: 'user-1',
  barcode: '111222333444',
  scannedAt: '2026-10-16T12:00:00Z',
  direction: 'stock_in',
  unitCount: 1,
  expiresAt: null,
  status: 'pending',
  productId: 'prod-1',
  product: {
    id: 'prod-1',
    name: 'Oat milk',
    category: 'Dairy',
    unitOfMeasure: 'carton',
  },
  committedAt: null,
  createdAt: '2026-10-16T12:00:00Z',
  ...overrides,
});

function householdItem(name: string, productId: string, instanceCount: number, category = 'Pantry'): InventoryItem {
  return {
    item: {
      id: `item-${productId}`,
      userId: 'user-1',
      productId,
      product: { id: productId, name, category, unitOfMeasure: 'bag' },
      targetQuantity: null,
      createdAt: '2026-10-16T12:00:00Z',
    },
    instanceCount,
    nearExpiryCount: 0,
    expiredCount: 0,
    needsAttention: false,
  };
}

function client(overrides: Partial<StockbotClient> = {}): StockbotClient {
  return {
    getSession: jest.fn(),
    getScannerConfig: jest.fn(),
    setScannerMode: jest.fn(),
    createScan: jest.fn(),
    listPending: jest.fn(async () => [entry(), entry({ id: 'scan-2', barcode: '999', product: null, productId: null, direction: 'stock_out' })]),
    commitScan: jest.fn(async () => entry({ status: 'committed' })),
    cancelScan: jest.fn(async () => entry({ status: 'cancelled' })),
    batchCommit: jest.fn(async () => ({ updatedCount: 2, committedIds: ['scan-1', 'scan-2'] })),
    getInventoryList: jest.fn(async () => [householdItem('Oat milk', 'prod-1', 2)]),
    ...overrides,
  };
}

describe('QueueScreen', () => {
  it('lists pending scans and confirms one', async () => {
    const api = client();
    const screen = await renderScreen(<QueueScreen client={api} warning="" />);

    expect(await screen.findByText('Oat milk')).toBeTruthy();
    expect(screen.getByText('2 scans are waiting')).toBeTruthy();
    expect(screen.getByText('999')).toBeTruthy();

    await fireEvent.press(screen.getByLabelText('Confirm Oat milk'));

    await waitFor(() => expect(api.commitScan).toHaveBeenCalledWith('scan-1'));
  });

  it('confirms every waiting scan', async () => {
    const api = client();
    const screen = await renderScreen(<QueueScreen client={api} warning="" />);
    await fireEvent.press(await screen.findByLabelText('Confirm all'));

    await waitFor(() => expect(api.batchCommit).toHaveBeenCalledWith(['scan-1', 'scan-2']));
    expect(screen.queryByTestId('confirm-toast')).toBeNull();
    expect(screen.queryByText('Only 1 was on hand, so 1 was taken out.')).toBeNull();
  });

  it.each([
    {
      label: 'one failure',
      failures: [{ id: 'scan-2', reason: 'direction not set', code: '' }],
    },
    {
      label: 'several failures',
      failures: [
        { id: 'scan-1', reason: 'Nothing on hand to take out.', code: 'none_on_hand' },
        { id: 'scan-2', reason: 'Choose which item this is first.', code: 'product_unresolved' },
      ],
    },
  ])('batch $label puts each reason on its card', async ({ failures }) => {
    const api = client({
      listPending: jest.fn(async () => [
        entry({ direction: 'stock_out' }),
        entry({ id: 'scan-2', barcode: '999', product: null, productId: null, direction: 'stock_out' }),
      ]),
      batchCommit: jest.fn(async () => ({
        updatedCount: 0,
        failedIds: failures.map((failure) => failure.id),
        failedReasons: failures.map((failure) => failure.reason),
        failedCodes: failures.map((failure) => failure.code ?? ''),
      })),
    });
    const screen = await renderScreen(<QueueScreen client={api} warning="" />);

    await fireEvent.press(await screen.findByLabelText('Confirm all'));

    expect(await screen.findByText('Some scans could not be confirmed.')).toBeTruthy();
    expect(screen.getAllByText('Some scans could not be confirmed.')).toHaveLength(1);
    expect(screen.queryByTestId('confirm-toast')).toBeNull();
    for (const failure of failures) {
      const card = within(screen.getByTestId(`scan-${failure.id}`));
      expect(card.getByText(failure.reason)).toBeTruthy();
      expect(card.queryByText('Some scans could not be confirmed.')).toBeNull();
    }
    const unresolved = failures.find((failure) => failure.code === 'product_unresolved');
    if (unresolved) {
      const card = within(screen.getByTestId(`scan-${unresolved.id}`));
      expect(card.getByText('Which item is this?')).toBeTruthy();
      expect(card.getByLabelText('Search')).toBeTruthy();
      expect(card.queryByText('No close match found.')).toBeNull();
    }
    const stockedOut = failures.find((failure) => failure.code === 'none_on_hand');
    if (stockedOut) {
      expect(within(screen.getByTestId(`scan-${stockedOut.id}`)).queryByLabelText('Search')).toBeNull();
    }
  });

  const stocked = (id: string, name: string) => entry({
    id,
    barcode: id,
    direction: 'stock_out',
    productId: `prod-${id}`,
    product: { id: `prod-${id}`, name, category: 'Pantry', unitOfMeasure: 'bag' },
  });

  const otherFailureCodes = [
    { code: 'instance_gone', message: 'That one was already taken out. Pick another.' },
    { code: 'not_pending', message: 'This scan was already handled.' },
    { code: 'product_unresolved', message: 'Choose which item this is first.' },
    { code: 'no_item', message: 'Which item is this?' },
    { code: 'link_target_gone', message: "That item isn't available anymore. Pick another." },
    { code: 'scan_not_found', message: 'This scan is no longer in the queue.' },
    { code: 'direction_unset', message: 'Choose stock in or stock out first.' },
    { code: 'internal', message: 'Something went wrong. Try again.' },
  ] as const;

  it.each([
    { label: 'busy and nothing committed', code: 'busy', message: BUSY_SENTENCE, committed: 0, banner: BUSY_SENTENCE },
    { label: 'busy and one committed', code: 'busy', message: BUSY_SENTENCE, committed: 1, banner: `1 confirmed. ${BUSY_SENTENCE}` },
    { label: 'busy and many committed', code: 'busy', message: BUSY_SENTENCE, committed: 4, banner: `4 confirmed. ${BUSY_SENTENCE}` },
    { label: 'no_space and nothing committed', code: 'no_space', message: NO_SPACE_SENTENCE, committed: 0, banner: NO_SPACE_SENTENCE },
    { label: 'no_space and one committed', code: 'no_space', message: NO_SPACE_SENTENCE, committed: 1, banner: `1 confirmed. ${NO_SPACE_SENTENCE}` },
    { label: 'no_space and many committed', code: 'no_space', message: NO_SPACE_SENTENCE, committed: 3, banner: `3 confirmed. ${NO_SPACE_SENTENCE}` },
    ...otherFailureCodes.map((row) => ({
      label: row.code,
      code: row.code,
      message: row.message,
      committed: 2,
      banner: BATCH_FAILURE_FALLBACK,
    })),
  ])('batch $label', async ({ code, message, committed, banner }) => {
    const failed = [stocked('scan-fail', 'Short loaf')];
    if (committed === 0) failed.push(stocked('scan-fail-2', 'Second loaf'));
    const kept = Array.from({ length: committed }, (_, index) => stocked(`scan-ok-${index}`, `Kept ${index}`));
    const cards = [...kept, ...failed];
    const api = client({
      listPending: jest.fn()
        .mockResolvedValueOnce(cards)
        .mockResolvedValue(failed),
      batchCommit: jest.fn(async () => ({
        updatedCount: cards.length,
        committedIds: kept.map((card) => card.id),
        failedIds: failed.map((card) => card.id),
        failedReasons: failed.map(() => message),
        failedCodes: failed.map(() => code),
      })),
      getInventoryList: jest.fn(async () => cards.map((card) => householdItem(card.product?.name ?? 'Item', card.productId ?? '', 2))),
    });
    const screen = await renderScreen(<QueueScreen client={api} warning="" />);

    await fireEvent.press(await screen.findByLabelText('Confirm all'));
    await waitFor(() => expect(api.listPending).toHaveBeenCalledTimes(2));

    expect(screen.getAllByText(banner)).toHaveLength(1);
    expectBanner(screen, 'failure', [banner]);
    if (banner !== BATCH_FAILURE_FALLBACK) {
      expect(screen.queryByText(BATCH_FAILURE_FALLBACK)).toBeNull();
    }
    for (const card of failed) {
      const row = within(screen.getByTestId(`scan-${card.id}`));
      if (code === 'busy' || code === 'no_space') {
        expect(row.queryByText(message)).toBeNull();
      } else if (code !== 'no_item') {
        expect(row.getByText(message)).toBeTruthy();
        expect(row.queryByText(BATCH_FAILURE_FALLBACK)).toBeNull();
      }
    }
  });

  it('a busy card beside amber keeps one red sentence', async () => {
    const amber = stocked('scan-amber', 'Rice');
    const busy = stocked('scan-busy', 'Oats');
    const kept = [stocked('scan-ok-0', 'Kept 0'), stocked('scan-ok-1', 'Kept 1')];
    const cards = [...kept, amber, busy];
    const api = client({
      listPending: jest.fn()
        .mockResolvedValueOnce(cards)
        .mockResolvedValue([amber, busy]),
      batchCommit: jest.fn(async () => ({
        updatedCount: 2,
        committedIds: kept.map((card) => card.id),
        failedIds: [amber.id, busy.id],
        failedReasons: [NOTHING_ON_HAND_TITLE, BUSY_SENTENCE],
        failedCodes: ['none_on_hand', 'busy'],
      })),
      getInventoryList: jest.fn(async () => cards.map((card) => householdItem(card.product?.name ?? 'Item', card.productId ?? '', 2))),
    });
    const screen = await renderScreen(<QueueScreen client={api} warning="" />);

    await fireEvent.press(await screen.findByLabelText('Confirm all'));
    const banner = `2 confirmed. ${BUSY_SENTENCE}`;
    expect(await screen.findByText(banner)).toBeTruthy();
    expect(screen.getAllByText(banner)).toHaveLength(1);
    expectBanner(screen, 'failure', [banner]);
    expect(screen.queryByText(BATCH_FAILURE_FALLBACK)).toBeNull();
    expect(within(screen.getByTestId('scan-scan-amber')).getByText(NOTHING_ON_HAND_TITLE)).toBeTruthy();
    expect(within(screen.getByTestId('scan-scan-busy')).queryByText(BUSY_SENTENCE)).toBeNull();
  });

  it.each([
    { label: 'nothing to say', partial: false },
    { label: 'a partial line', partial: true },
  ])('an amber-only batch with $label shows no failure sentence', async ({ partial }) => {
    const amber = stocked('scan-amber', 'Rice');
    const second = stocked('scan-also', 'Beans');
    const kept = partial ? [stocked('scan-ok-0', 'Tortilla chips')] : [];
    const cards = [...kept, amber, second];
    const line = 'Only 1 of 3 Tortilla chips was on hand, so 1 was taken out.';
    const api = client({
      listPending: jest.fn()
        .mockResolvedValueOnce(cards)
        .mockResolvedValue([amber, second]),
      batchCommit: jest.fn(async () => ({
        updatedCount: kept.length,
        committedIds: kept.map((card) => card.id),
        failedIds: [amber.id, second.id],
        failedReasons: [NOTHING_ON_HAND_TITLE, NOTHING_ON_HAND_TITLE],
        failedCodes: ['none_on_hand', 'none_on_hand'],
        ...(partial ? { stockOutCounts: { 'scan-ok-0': { removedCount: 1, requestedCount: 3 } } } : {}),
      })),
      getInventoryList: jest.fn(async () => cards.map((card) => householdItem(card.product?.name ?? 'Item', card.productId ?? '', 2))),
    });
    const screen = await renderScreen(<QueueScreen client={api} warning="" />);

    await fireEvent.press(await screen.findByLabelText('Confirm all'));
    expect(await within(await screen.findByTestId('scan-scan-amber')).findByText(NOTHING_ON_HAND_TITLE)).toBeTruthy();
    expect(screen.queryByText(BATCH_FAILURE_FALLBACK)).toBeNull();
    expect(screen.queryByText(BUSY_SENTENCE)).toBeNull();
    if (partial) {
      expectBanner(screen, 'success', [line]);
    } else {
      expectBanner(screen, 'gone', []);
    }
  });

  const pears: PantryLinkSuggestion[] = [
    { itemId: 'item-pears', productId: 'prod-pears', name: 'Sliced Pears', onHand: 3 },
  ];

  it.each([
    { label: 'suggestions', suggestions: pears },
    { label: 'no suggestions', suggestions: [] as PantryLinkSuggestion[] },
  ])('stock-out no_item with $label offers a link', async ({ suggestions }) => {
    const api = client({
      listPending: jest.fn(async () => [entry({ direction: 'stock_out' })]),
      commitScan: jest.fn(async () => {
        throw new ApiError(409, 'Which item is this?', 'no_item', suggestions);
      }),
    });
    const screen = await renderScreen(<QueueScreen client={api} warning="" />);

    await fireEvent.press(await screen.findByLabelText('Confirm Oat milk'));

    expect(await screen.findByText('Which item is this?')).toBeTruthy();
    expect(screen.getByLabelText('Search')).toBeTruthy();
    expect(screen.queryByText(/Not in your pantry/)).toBeNull();
    expect(screen.queryByText('Nothing on hand to take out.')).toBeNull();
    if (suggestions.length === 0) {
      expect(screen.getByText('No close match found.')).toBeTruthy();
      expect(screen.queryByLabelText(/Link /)).toBeNull();
    } else {
      expect(screen.getByText('Sliced Pears')).toBeTruthy();
      expect(screen.getByText('3 in stock')).toBeTruthy();
      expect(screen.getByLabelText('Link Sliced Pears')).toBeTruthy();
      expect(screen.queryByTestId('confirm-toast')).toBeNull();
      expect(screen.queryByText('No close match found.')).toBeNull();
    }
  });

  it('an absent product shows the link offer, not an amber card', async () => {
    const api = client({
      listPending: jest.fn(async () => [entry({ direction: 'stock_out' })]),
      getInventoryList: jest.fn(async () => []),
      commitScan: jest.fn(async () => {
        throw new ApiError(409, 'Which item is this?', 'no_item', pears);
      }),
    });
    const screen = await renderScreen(<QueueScreen client={api} warning="" />);

    expect(screen.queryByText(NOTHING_ON_HAND_TITLE)).toBeNull();
    await fireEvent.press(await screen.findByLabelText('Confirm Oat milk'));

    expect(await screen.findByText('Which item is this?')).toBeTruthy();
    expect(screen.getByText('Sliced Pears')).toBeTruthy();
    expect(screen.queryByText(NOTHING_ON_HAND_TITLE)).toBeNull();
    expect(screen.queryByText(NOTHING_ON_HAND_BODY)).toBeNull();
  });

  it('links the picked stock-out suggestion', async () => {
    const commitScan = jest.fn(async (_id: string, linkProductId?: string) => {
      if (linkProductId !== 'prod-pears') throw new ApiError(409, 'Which item is this?', 'no_item', pears);
      return entry({ status: 'committed' });
    });
    const api = client({
      listPending: jest.fn(async () => [entry({ direction: 'stock_out' })]),
      commitScan,
    });
    const screen = await renderScreen(<QueueScreen client={api} warning="" />);

    await fireEvent.press(await screen.findByLabelText('Confirm Oat milk'));
    await fireEvent.press(await screen.findByLabelText('Link Sliced Pears'));

    await waitFor(() => expect(commitScan).toHaveBeenLastCalledWith('scan-1', 'prod-pears'));
    await waitFor(() => expect(screen.queryByText('Which item is this?')).toBeNull());
  });

  it('dismisses the unlinked card and leaves the scan waiting', async () => {
    const api = client({
      listPending: jest.fn(async () => [entry({ direction: 'stock_out' })]),
      commitScan: jest.fn(async () => {
        throw new ApiError(409, 'Which item is this?', 'no_item', pears);
      }),
    });
    const screen = await renderScreen(<QueueScreen client={api} warning="" />);

    await fireEvent.press(await screen.findByLabelText('Confirm Oat milk'));
    await fireEvent.press(await screen.findByLabelText('Dismiss'));

    await waitFor(() => expect(screen.queryByText('Which item is this?')).toBeNull());
    expect(screen.getByText('Oat milk')).toBeTruthy();
    expect(api.commitScan).toHaveBeenCalledTimes(1);
  });

  it.each([
    {
      label: 'partial stock-out',
      direction: 'stock_out' as const,
      counts: { removedCount: 1, requestedCount: 3 },
      note: 'Only 1 was on hand, so 1 was taken out.',
    },
    {
      label: 'equal counts',
      direction: 'stock_out' as const,
      counts: { removedCount: 3, requestedCount: 3 },
      note: '',
    },
    {
      label: 'stock-in',
      direction: 'stock_in' as const,
      counts: {},
      note: '',
    },
  ])('$label shows the short-shelf note only when removedCount is below requestedCount', async ({ direction, counts, note }) => {
    const api = client({
      listPending: jest.fn(async () => [entry({ direction, unitCount: 3 })]),
      commitScan: jest.fn(async () => ({
        ...entry({
          id: 'scan-partial',
          barcode: 'scan-partial',
          direction,
          unitCount: 3,
          status: 'committed',
          productId: 'prod-partial',
        }),
        ...counts,
      })),
    });
    const screen = await renderScreen(<QueueScreen client={api} warning="" />);

    await fireEvent.press(await screen.findByLabelText('Confirm Oat milk'));

    await waitFor(() => expect(api.listPending).toHaveBeenCalledTimes(2));
    if (note === '') {
      expect(screen.queryByTestId('confirm-toast')).toBeNull();
      expect(screen.queryByText('Only 1 was on hand, so 1 was taken out.')).toBeNull();
    } else {
      expect(within(screen.getByTestId('confirm-toast')).getByText(note)).toBeTruthy();
    }
  });

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
  const batchResponse = (body: BatchCommitResponse): BatchCommitResponse => body;
  const wasLine = 'Only 1 of 3 Tortilla chips was on hand, so 1 was taken out.';
  const wereLine = 'Only 2 of 5 Tortilla chips were on hand, so 2 were taken out.';
  const manyLine = '2 cards had fewer on hand than scanned, so only what was on hand was taken out.';
  const oneCardLine = '1 card had fewer on hand than scanned, so only what was on hand was taken out.';
  const batchCard = (id: string, name: string, direction: 'stock_in' | 'stock_out', unitCount: number) => entry({
    id,
    barcode: id,
    direction,
    unitCount,
    productId: `prod-${id}`,
    product: { id: `prod-${id}`, name, category: 'Pantry', unitOfMeasure: 'bag' },
  });

  it.each([
    {
      label: 'one partial card',
      cards: [
        batchCard('scan-batch-partial', 'Tortilla chips', 'stock_out', partialCount.requestedCount),
        batchCard('scan-batch-in', 'Batch In', 'stock_in', 2),
      ],
      kept: [] as ReturnType<typeof entry>[],
      batch: batchResponse({
        updatedCount: 2,
        committedIds: ['scan-batch-partial', 'scan-batch-in'],
        stockOutCounts: { 'scan-batch-partial': partialCount },
      }),
      lines: [wasLine],
      absent: ['Some scans could not be confirmed.', wereLine, manyLine],
      banner: [wasLine],
    },
    {
      label: 'a removed count above one',
      cards: [
        batchCard('scan-batch-partial', 'Tortilla chips', 'stock_out', 5),
        batchCard('scan-batch-in', 'Batch In', 'stock_in', 2),
      ],
      kept: [] as ReturnType<typeof entry>[],
      batch: batchResponse({
        updatedCount: 2,
        committedIds: ['scan-batch-partial', 'scan-batch-in'],
        stockOutCounts: { 'scan-batch-partial': { removedCount: 2, requestedCount: 5 } },
      }),
      lines: [wereLine],
      absent: ['Some scans could not be confirmed.', wasLine, manyLine],
      banner: [wereLine],
    },
    {
      label: 'two partial cards',
      cards: [
        batchCard('scan-batch-partial', 'Tortilla chips', 'stock_out', partialCount.requestedCount),
        batchCard('scan-batch-second', 'Rolled oats', 'stock_out', 4),
      ],
      kept: [] as ReturnType<typeof entry>[],
      batch: batchResponse({
        updatedCount: 2,
        committedIds: ['scan-batch-partial', 'scan-batch-second'],
        stockOutCounts: {
          'scan-batch-partial': partialCount,
          'scan-batch-second': { removedCount: 2, requestedCount: 4 },
        },
      }),
      lines: [manyLine],
      absent: ['Some scans could not be confirmed.', wasLine, wereLine, 'Tortilla chips', 'Rolled oats'],
      banner: [manyLine],
    },
    {
      label: 'one short card with no name',
      cards: [
        entry({
          id: 'scan-batch-partial',
          barcode: 'scan-batch-partial',
          direction: 'stock_out',
          unitCount: partialCount.requestedCount,
          productId: null,
          product: null,
        }),
        batchCard('scan-batch-in', 'Batch In', 'stock_in', 2),
      ],
      kept: [] as ReturnType<typeof entry>[],
      batch: batchResponse({
        updatedCount: 2,
        committedIds: ['scan-batch-partial', 'scan-batch-in'],
        stockOutCounts: { 'scan-batch-partial': partialCount },
      }),
      lines: [oneCardLine],
      absent: [
        '1 cards had fewer on hand than scanned, so only what was on hand was taken out.',
        'Unknown product',
        'Some scans could not be confirmed.',
        wasLine,
        wereLine,
        manyLine,
      ],
      banner: [oneCardLine],
    },
    {
      label: 'the partial batch fixture',
      cards: [
        batchCard('scan-batch-full', 'Batch Full', 'stock_out', fullCount.requestedCount),
        batchCard('scan-batch-partial', 'Tortilla chips', 'stock_out', partialCount.requestedCount),
        batchCard('scan-batch-in', 'Batch In', 'stock_in', 2),
        batchCard('scan-batch-none', 'Batch Empty', 'stock_out', 1),
      ],
      kept: [batchCard('scan-batch-none', 'Batch Empty', 'stock_out', 1)],
      batch: batchResponse(batchContract.body),
      lines: [wasLine, 'Nothing on hand to take out.'],
      absent: [wereLine, manyLine, 'Some scans could not be confirmed.'],
      banner: [wasLine],
    },
    {
      label: 'no short card',
      cards: [
        batchCard('scan-batch-full', 'Batch Full', 'stock_out', fullCount.requestedCount),
        batchCard('scan-batch-partial', 'Tortilla chips', 'stock_out', partialCount.requestedCount),
      ],
      kept: [] as ReturnType<typeof entry>[],
      batch: batchResponse({
        updatedCount: 2,
        committedIds: ['scan-batch-full', 'scan-batch-partial'],
        stockOutCounts: {
          'scan-batch-full': fullCount,
          'scan-batch-partial': { removedCount: partialCount.requestedCount, requestedCount: partialCount.requestedCount },
        },
      }),
      lines: [] as string[],
      absent: ['Some scans could not be confirmed.', wasLine, wereLine, manyLine],
      banner: [] as string[],
    },
  ])('batch $label', async ({ cards, kept, batch, lines, absent, banner }) => {
    const listed = [
      householdItem('Tortilla chips', 'prod-scan-batch-partial', 3),
      householdItem('Batch Full', 'prod-scan-batch-full', 2),
      householdItem('Batch Empty', 'prod-scan-batch-none', 1),
      householdItem('Rolled oats', 'prod-scan-batch-second', 4),
    ];
    const api = client({
      listPending: jest.fn()
        .mockResolvedValueOnce(cards)
        .mockResolvedValue(kept),
      batchCommit: jest.fn(async () => batch),
      getInventoryList: jest.fn(async () => listed),
    });
    const screen = await renderScreen(<QueueScreen client={api} warning="" />);

    await fireEvent.press(await screen.findByLabelText('Confirm all'));
    await waitFor(() => expect(api.listPending).toHaveBeenCalledTimes(2));

    for (const line of lines) expect(screen.getByText(line)).toBeTruthy();
    for (const line of absent) expect(screen.queryByText(line)).toBeNull();
    if (lines.includes(wasLine)) expect(screen.getAllByText(wasLine)).toHaveLength(1);
    if (banner.length === 0) {
      expect(screen.queryByTestId('batch-banner')).toBeNull();
    } else {
      const bannerNode = screen.getByTestId('batch-banner');
      const texts = bannerNode.children.filter((child): child is Exclude<typeof child, string> => typeof child !== 'string');
      expect(texts.map((node) => node.props.children)).toEqual(banner);
      for (const node of texts) {
        const line = String(node.props.children);
        const flat = StyleSheet.flatten(node.props.style);
        const color = flat && typeof flat === 'object' && 'color' in flat ? flat.color : undefined;
        expect(color).toBe(line === 'Some scans could not be confirmed.' ? colors.danger : colors.ink);
      }
      const host = StyleSheet.flatten(bannerNode.props.style);
      const wash = host && typeof host === 'object' && 'backgroundColor' in host ? host.backgroundColor : undefined;
      const edge = host && typeof host === 'object' && 'borderColor' in host ? host.borderColor : undefined;
      const success = banner[0] !== 'Some scans could not be confirmed.';
      expect(wash).toBe(success ? colors.successWash : undefined);
      expect(edge).toBe(success ? colors.successEdge : undefined);
    }
    const failed = cards.find((card) => card.id === 'scan-batch-none');
    if (failed && kept.some((card) => card.id === failed.id)) {
      const card = within(screen.getByTestId('scan-scan-batch-none'));
      expect(card.getByText('Nothing on hand to take out.')).toBeTruthy();
      expect(card.queryByText(wasLine)).toBeNull();
      expect(card.queryByText('Some scans could not be confirmed.')).toBeNull();
    }
  });

  function framed(ui: ReactElement) {
    return (
      <SafeAreaProvider
        initialMetrics={{
          insets: { top: 0, right: 0, bottom: 0, left: 0 },
          frame: { x: 0, y: 0, width: 390, height: 844 },
        }}
      >
        {ui}
      </SafeAreaProvider>
    );
  }

  async function reloadQueue(screen: Awaited<ReturnType<typeof renderScreen>>) {
    const lists = screen.root?.queryAll((node) => node.props.refreshControl != null) ?? [];
    const refresh = lists[0]?.props.refreshControl?.props?.onRefresh as undefined | (() => Promise<void>);
    if (refresh === undefined) throw new Error('The queue list has no refresh.');
    await act(async () => {
      await refresh();
    });
  }

  function expectBanner(
    screen: Awaited<ReturnType<typeof renderScreen>>,
    state: 'gone' | 'success' | 'failure',
    lines: string[],
  ) {
    const banner = screen.queryByTestId('batch-banner');
    if (state === 'gone') {
      expect(banner).toBeNull();
      return;
    }
    if (banner === null) throw new Error('The batch banner is missing.');
    const texts = banner.children.filter((child): child is Exclude<typeof child, string> => typeof child !== 'string');
    expect(texts.map((node) => String(node.props.children))).toEqual(lines);
    texts.forEach((node, index) => {
      const flat = StyleSheet.flatten(node.props.style);
      const color = flat && typeof flat === 'object' && 'color' in flat ? flat.color : undefined;
      expect(color).toBe(state === 'failure' && index === 0 ? colors.danger : colors.ink);
    });
    const host = StyleSheet.flatten(banner.props.style);
    const wash = host && typeof host === 'object' && 'backgroundColor' in host ? host.backgroundColor : undefined;
    const edge = host && typeof host === 'object' && 'borderColor' in host ? host.borderColor : undefined;
    expect(wash).toBe(state === 'success' ? colors.successWash : undefined);
    expect(edge).toBe(state === 'success' ? colors.successEdge : undefined);
  }

  function expectCleared(
    screen: Awaited<ReturnType<typeof renderScreen>>,
    state: 'gone' | 'failure',
  ) {
    expect(screen.queryByText(wasLine)).toBeNull();
    if (state === 'gone') {
      expectBanner(screen, 'gone', []);
      return;
    }
    expectBanner(screen, 'failure', ['Some scans could not be confirmed.']);
  }

  it.each([
    { label: 'a failed retry', trigger: 'confirm-fail', banner: 'gone' as const },
    { label: 'a successful confirm', trigger: 'confirm-ok', banner: 'gone' as const },
    { label: 'a failed link', trigger: 'link-fail', banner: 'gone' as const },
    { label: 'a successful link', trigger: 'link-ok', banner: 'gone' as const },
    { label: 'a new batch', trigger: 'batch', banner: 'gone' as const },
    { label: 'a removed card', trigger: 'removed', banner: 'gone' as const },
    { label: 'a new scan', trigger: 'arrived', banner: 'gone' as const },
    { label: 'leaving the queue', trigger: 'leave', banner: 'gone' as const },
  ])('clears the partial line on $label', async ({ trigger, banner }) => {
    const keptFailure = batchCard('scan-keep', 'Kept oil', 'stock_out', 1);
    const secondFailure = batchCard('scan-also', 'Second oil', 'stock_out', 1);
    const partialCard = batchCard('scan-batch-partial', 'Tortilla chips', 'stock_out', partialCount.requestedCount);
    const unresolved = entry({
      id: 'scan-open',
      barcode: 'scan-open',
      direction: 'stock_out',
      productId: null,
      product: null,
    });
    const linking = trigger === 'link-fail' || trigger === 'link-ok';
    const blocking = !linking && trigger !== 'confirm-fail' && trigger !== 'confirm-ok' && trigger !== 'batch';
    const cards = linking ? [partialCard, unresolved] : [partialCard, keptFailure, secondFailure];
    const kept = linking ? [unresolved] : [keptFailure, secondFailure];
    const arrived = entry({
      id: 'scan-new',
      barcode: '555666777888',
      productId: 'prod-new',
      product: { id: 'prod-new', name: 'New rice', category: 'Pantry', unitOfMeasure: 'bag' },
    });
    const after = trigger === 'removed' ? [] : trigger === 'arrived' ? [...kept, arrived] : kept;
    const batch = batchResponse({
      updatedCount: cards.length,
      committedIds: ['scan-batch-partial'],
      failedIds: kept.map((card) => card.id),
      failedReasons: kept.map(() => linking ? 'Choose which item this is first.' : 'Nothing on hand to take out.'),
      failedCodes: kept.map(() => {
        if (linking) return 'product_unresolved';
        return blocking ? 'none_on_hand' : '';
      }),
      stockOutCounts: { 'scan-batch-partial': partialCount },
    });
    const listPending = jest.fn()
      .mockResolvedValueOnce(cards)
      .mockResolvedValueOnce(kept)
      .mockResolvedValue(after);
    const commitScan = jest.fn(async () => {
      if (trigger === 'confirm-fail' || trigger === 'link-fail') {
        throw new ApiError(409, 'Nothing on hand to take out.', 'none_on_hand');
      }
      return entry({ status: 'committed' });
    });
    const batchCommit = jest.fn()
      .mockResolvedValueOnce(batch)
      .mockResolvedValue({
        updatedCount: kept.length,
        failedIds: kept.map((card) => card.id),
        failedReasons: kept.map(() => 'Nothing on hand to take out.'),
        failedCodes: kept.map(() => 'none_on_hand'),
      });
    const api = client({
      listPending,
      commitScan,
      batchCommit,
      getInventoryList: jest.fn(async () => [
        householdItem('Tortilla chips', 'prod-scan-batch-partial', 3),
        householdItem('Kept oil', 'prod-scan-keep', 1),
        householdItem('Second oil', 'prod-scan-also', 1),
        householdItem('Oats', 'prod-oats', 2, 'Grain'),
      ]),
    });
    const screen = await renderScreen(<QueueScreen client={api} warning="" />);

    await fireEvent.press(await screen.findByLabelText('Confirm all'));
    await waitFor(() => expect(listPending).toHaveBeenCalledTimes(2));
    expect(screen.getByText(wasLine)).toBeTruthy();
    if (blocking && !linking) {
      expect(screen.queryByText('Some scans could not be confirmed.')).toBeNull();
      expectBanner(screen, 'success', [wasLine]);
    } else {
      expect(screen.getByText('Some scans could not be confirmed.')).toBeTruthy();
    }

    if (trigger === 'batch') {
      await fireEvent.press(screen.getByLabelText('Confirm all'));
      await waitFor(() => expect(batchCommit).toHaveBeenCalledTimes(2));
      expect(screen.queryByText(wasLine)).toBeNull();
    } else if (trigger === 'confirm-fail' || trigger === 'confirm-ok') {
      await fireEvent.press(screen.getByLabelText('Confirm Kept oil'));
      await waitFor(() => expect(commitScan).toHaveBeenCalled());
    } else if (trigger === 'link-fail' || trigger === 'link-ok') {
      await fireEvent(screen.getByLabelText('Search'), 'focus');
      await fireEvent.press(await screen.findByLabelText('Link Oats'));
      await waitFor(() => expect(commitScan).toHaveBeenCalled());
    } else if (trigger === 'removed' || trigger === 'arrived') {
      await reloadQueue(screen);
      await waitFor(() => expect(listPending).toHaveBeenCalledTimes(3));
    } else {
      await screen.rerender(framed(<QueueScreen client={api} warning="" focused={false} />));
    }

    expectCleared(screen, banner);
    if (trigger === 'confirm-fail') {
      expect(screen.getByText('Kept oil')).toBeTruthy();
      expect(screen.queryByText('Some scans could not be confirmed.')).toBeNull();
      expect(screen.getByText(NOTHING_ON_HAND_BODY)).toBeTruthy();
      expect(screen.queryByLabelText('Confirm Kept oil')).toBeNull();
      expect(screen.queryByTestId('confirm-toast')).toBeNull();
    }
    if (trigger === 'link-fail') expect(screen.getByText('Which item is this?')).toBeTruthy();
    if (trigger === 'arrived') expect(screen.getByText('New rice')).toBeTruthy();
    if (trigger === 'removed') expect(screen.queryByText('Kept oil')).toBeNull();
  });

  it.each([
    { label: 'same cards keep the line', next: 'refresh' },
    { label: 'a network error during confirm-all', next: 'network' },
  ])('batch $label', async ({ next }) => {
    const cards = [
      batchCard('scan-batch-partial', 'Tortilla chips', 'stock_out', partialCount.requestedCount),
      batchCard('scan-batch-in', 'Batch In', 'stock_in', 2),
    ];
    const listPending = jest.fn(async () => cards);
    const batchCommit = jest.fn()
      .mockResolvedValueOnce(batchResponse({
        updatedCount: 2,
        committedIds: ['scan-batch-partial', 'scan-batch-in'],
        stockOutCounts: { 'scan-batch-partial': partialCount },
      }))
      .mockRejectedValueOnce(new Error('Something went wrong. Try again.'));
    const api = client({
      listPending,
      batchCommit,
      getInventoryList: jest.fn(async () => [
        householdItem('Tortilla chips', 'prod-scan-batch-partial', 3),
        householdItem('Batch In', 'prod-scan-batch-in', 2),
      ]),
    });
    const screen = await renderScreen(<QueueScreen client={api} warning="" />);

    await fireEvent.press(await screen.findByLabelText('Confirm all'));
    await waitFor(() => expect(listPending).toHaveBeenCalledTimes(2));
    expectBanner(screen, 'success', [wasLine]);

    if (next === 'refresh') {
      await reloadQueue(screen);
      await waitFor(() => expect(listPending).toHaveBeenCalledTimes(3));
      expect(screen.getAllByText(wasLine)).toHaveLength(1);
      expect(screen.queryByText('Some scans could not be confirmed.')).toBeNull();
      expect(screen.queryByText('Something went wrong. Try again.')).toBeNull();
      expectBanner(screen, 'success', [wasLine]);
      expect(screen.getByText('Tortilla chips')).toBeTruthy();
      expect(screen.getByText('Batch In')).toBeTruthy();
      return;
    }

    await fireEvent.press(screen.getByLabelText('Confirm all'));
    expect(await screen.findByText('Something went wrong. Try again.')).toBeTruthy();
    expect(screen.queryByText(wasLine)).toBeNull();
    expect(screen.queryByText('Some scans could not be confirmed.')).toBeNull();
    expectBanner(screen, 'failure', ['Something went wrong. Try again.']);
    expect(screen.getByText('Tortilla chips')).toBeTruthy();
    expect(screen.getByText('Batch In')).toBeTruthy();
  });

  it.each([
    { label: 'the same failed card', refresh: 'same' },
    { label: 'different cards', refresh: 'different' },
  ])('pull to refresh with $label', async ({ refresh }) => {
    const failed = batchCard('scan-batch-none', 'Batch Empty', 'stock_out', 1);
    const arrived = entry({
      id: 'scan-new',
      barcode: '555666777888',
      productId: 'prod-new',
      product: { id: 'prod-new', name: 'New rice', category: 'Pantry', unitOfMeasure: 'bag' },
    });
    const cards = [
      batchCard('scan-batch-partial', 'Tortilla chips', 'stock_out', partialCount.requestedCount),
      failed,
    ];
    const listPending = jest.fn()
      .mockResolvedValueOnce(cards)
      .mockResolvedValueOnce([failed])
      .mockResolvedValue(refresh === 'same' ? [failed] : [failed, arrived]);
    const api = client({
      listPending,
      batchCommit: jest.fn(async () => batchResponse({
        updatedCount: 2,
        committedIds: ['scan-batch-partial'],
        failedIds: ['scan-batch-none'],
        failedReasons: ['Nothing on hand to take out.'],
        failedCodes: ['none_on_hand'],
        stockOutCounts: { 'scan-batch-partial': partialCount },
      })),
      getInventoryList: jest.fn(async () => [
        householdItem('Tortilla chips', 'prod-scan-batch-partial', 3),
        householdItem('Batch Empty', 'prod-scan-batch-none', 1),
        householdItem('New rice', 'prod-new', 2),
      ]),
    });
    const screen = await renderScreen(<QueueScreen client={api} warning="" />);

    await fireEvent.press(await screen.findByLabelText('Confirm all'));
    await waitFor(() => expect(listPending).toHaveBeenCalledTimes(2));
    expectBanner(screen, 'success', [wasLine]);
    expect(screen.queryByText('Some scans could not be confirmed.')).toBeNull();
    expect(screen.getByText('Nothing on hand to take out.')).toBeTruthy();

    await reloadQueue(screen);
    await waitFor(() => expect(listPending).toHaveBeenCalledTimes(3));
    expect(screen.getByText('Batch Empty')).toBeTruthy();
    if (refresh === 'same') {
      expect(screen.getAllByText(wasLine)).toHaveLength(1);
      expect(screen.queryByText('Some scans could not be confirmed.')).toBeNull();
      expectBanner(screen, 'success', [wasLine]);
      return;
    }
    expect(screen.getByText('New rice')).toBeTruthy();
    expect(screen.queryByText(wasLine)).toBeNull();
    expect(screen.queryByText('Some scans could not be confirmed.')).toBeNull();
    expectBanner(screen, 'gone', []);
  });

  it('none on hand keeps the scan and shows the server sentence', async () => {
    const api = client({
      listPending: jest.fn(async () => [entry({ direction: 'stock_out' })]),
      commitScan: jest.fn(async () => {
        throw new ApiError(409, 'Nothing on hand to take out.', 'none_on_hand');
      }),
    });
    const screen = await renderScreen(<QueueScreen client={api} warning="" />);

    await fireEvent.press(await screen.findByLabelText('Confirm Oat milk'));

    expect(screen.queryByTestId('confirm-toast')).toBeNull();
    expect(screen.getByText(NOTHING_ON_HAND_TITLE)).toBeTruthy();
    expect(screen.getByText(NOTHING_ON_HAND_BODY)).toBeTruthy();
    expect(screen.queryByLabelText('Confirm Oat milk')).toBeNull();
    expect(screen.getByLabelText('Remove Oat milk')).toBeTruthy();
    expect(screen.getByText('Oat milk')).toBeTruthy();
    expect(screen.queryByText('Which item is this?')).toBeNull();
    expect(screen.queryByLabelText('Search')).toBeNull();
  });

  const slices: PantryLinkSuggestion = { itemId: 'item-slices', productId: 'prod-slices', name: 'Pear Slices', onHand: 1 };

  it.each([
    { label: 'the only suggestion', suggestions: pears, remaining: '', empty: true },
    { label: 'one of two suggestions', suggestions: [pears[0], slices], remaining: 'Pear Slices', empty: false },
  ])('link_target_gone drops $label and reloads the household list', async ({ suggestions, remaining, empty }) => {
    const commitScan = jest.fn(async (_id: string, linkProductId?: string) => {
      if (linkProductId === 'prod-pears') {
        throw new ApiError(409, "That item isn't available anymore. Pick another.", 'link_target_gone');
      }
      throw new ApiError(409, 'Which item is this?', 'no_item', suggestions);
    });
    const getInventoryList = jest.fn(async () => [
      householdItem('Oat milk', 'prod-1', 2),
      householdItem('Sliced Pears', 'prod-pears', 3),
      householdItem('Brown Rice', 'prod-brown', 2),
    ]);
    const api = client({
      listPending: jest.fn(async () => [entry({ direction: 'stock_out' })]),
      commitScan,
      getInventoryList,
    });
    const screen = await renderScreen(<QueueScreen client={api} warning="" />);

    await fireEvent.press(await screen.findByLabelText('Confirm Oat milk'));
    if (empty) {
      await fireEvent(screen.getByLabelText('Search'), 'focus');
      expect(screen.getByText('Brown Rice')).toBeTruthy();
    }
    await fireEvent.press(await screen.findByLabelText('Link Sliced Pears'));

    expect(within(await screen.findByTestId('confirm-toast')).getByText("That item isn't available anymore. Pick another.")).toBeTruthy();
    expect(screen.getByText('Oat milk')).toBeTruthy();
    expect(screen.getByLabelText('Search')).toBeTruthy();
    expect(commitScan).toHaveBeenCalledTimes(2);
    await waitFor(() => expect(getInventoryList).toHaveBeenCalledTimes(2));
    expect(getInventoryList).toHaveBeenCalledWith();
    if (empty) {
      expect(screen.getByText('No close match found.')).toBeTruthy();
      expect(screen.queryByText('Sliced Pears')).toBeNull();
      expect(screen.queryByText('Brown Rice')).toBeNull();
      expect(screen.queryByLabelText('Link Brown Rice')).toBeNull();
      expect(screen.queryByLabelText(/Link /)).toBeNull();
    } else {
      expect(screen.getByText(remaining)).toBeTruthy();
      expect(screen.queryByText('No close match found.')).toBeNull();
      expect(screen.queryByText('Sliced Pears')).toBeNull();
    }

    await fireEvent(screen.getByLabelText('Search'), 'focus');
    expect(screen.getByText('Brown Rice')).toBeTruthy();
    expect(screen.queryByText('Sliced Pears')).toBeNull();
    expect(screen.queryByLabelText('Link Sliced Pears')).toBeNull();
  });

  it('keeps the link open when a zero-stock item has nothing on hand', async () => {
    const rice: PantryLinkSuggestion = { itemId: 'item-rice', productId: 'prod-rice', name: 'Rice', onHand: 0 };
    const commitScan = jest.fn(async (_id: string, linkProductId?: string) => {
      if (linkProductId === 'prod-rice') throw new ApiError(409, 'Nothing on hand to take out.', 'none_on_hand');
      throw new ApiError(409, 'Which item is this?', 'no_item', [rice]);
    });
    const api = client({
      listPending: jest.fn(async () => [entry({ direction: 'stock_out' })]),
      commitScan,
      getInventoryList: jest.fn(async () => [householdItem('Oat milk', 'prod-1', 2), householdItem('Rice', 'prod-rice', 0)]),
    });
    const screen = await renderScreen(<QueueScreen client={api} warning="" />);

    await fireEvent.press(await screen.findByLabelText('Confirm Oat milk'));
    await fireEvent.press(await screen.findByLabelText('Link Rice'));

    expect(await screen.findByText('Which item is this?')).toBeTruthy();
    expect(screen.getByLabelText('Link Rice')).toBeTruthy();
    expect(screen.getByText(NOTHING_ON_HAND_TITLE)).toBeTruthy();
    expect(screen.getByText(NOTHING_ON_HAND_BODY)).toBeTruthy();
    expect(screen.queryByTestId('confirm-toast')).toBeNull();
    expect(screen.queryByText('Add it and take it out')).toBeNull();
    expect(screen.getByText('Oat milk')).toBeTruthy();
  });

  it.each([
    { label: 'an empty query', query: '', visible: ['Rice', 'Oats'], hidden: [] as string[], stock: ['0 in stock', '2 in stock'], unmatched: false },
    { label: 'a category', query: 'grain', visible: ['Oats'], hidden: ['Rice'], stock: ['2 in stock'], unmatched: false },
    { label: 'a name and category together', query: 'oats grain', visible: ['Oats'], hidden: ['Rice'], stock: ['2 in stock'], unmatched: false },
    { label: 'no match', query: 'quinoa', visible: [] as string[], hidden: ['Rice', 'Oats'], stock: [] as string[], unmatched: true },
  ])('search with $label lists household items', async ({ query, visible, hidden, stock, unmatched }) => {
    const api = client({
      listPending: jest.fn(async () => [entry({ direction: 'stock_out' })]),
      commitScan: jest.fn(async () => {
        throw new ApiError(409, 'Which item is this?', 'no_item', []);
      }),
      getInventoryList: jest.fn(async () => [
        householdItem('Oat milk', 'prod-1', 5),
        householdItem('Rice', 'prod-rice', 0, 'Pantry'),
        householdItem('Oats', 'prod-oats', 2, 'Grain'),
      ]),
    });
    const screen = await renderScreen(<QueueScreen client={api} warning="" />);

    await fireEvent.press(await screen.findByLabelText('Confirm Oat milk'));
    const search = await screen.findByLabelText('Search');
    if (query === '') await fireEvent(search, 'focus');
    else await fireEvent.changeText(search, query);

    for (const name of visible) expect(screen.getByText(name)).toBeTruthy();
    for (const name of hidden) expect(screen.queryByText(name)).toBeNull();
    for (const line of stock) expect(screen.getByText(line)).toBeTruthy();
    if (unmatched) expect(screen.getByText('No matching products.')).toBeTruthy();
    else expect(screen.queryByText('No matching products.')).toBeNull();
  });

  it('links a household search result', async () => {
    const commitScan = jest.fn(async (_id: string, linkProductId?: string) => {
      if (linkProductId === 'prod-oats') return entry({ status: 'committed' });
      throw new ApiError(409, 'Which item is this?', 'no_item', []);
    });
    const api = client({
      listPending: jest.fn(async () => [entry({ direction: 'stock_out' })]),
      commitScan,
      getInventoryList: jest.fn(async () => [
        householdItem('Oat milk', 'prod-1', 2),
        householdItem('Oats', 'prod-oats', 2, 'Grain'),
      ]),
    });
    const screen = await renderScreen(<QueueScreen client={api} warning="" />);

    await fireEvent.press(await screen.findByLabelText('Confirm Oat milk'));
    await fireEvent.changeText(await screen.findByLabelText('Search'), 'oats');
    await fireEvent.press(await screen.findByLabelText('Link Oats'));

    await waitFor(() => expect(commitScan).toHaveBeenLastCalledWith('scan-1', 'prod-oats'));
  });

  it('batch product unresolved opens search without another commit', async () => {
    const api = client({
      listPending: jest.fn(async () => [
        entry({ direction: 'stock_out' }),
        entry({ id: 'scan-2', barcode: '999', product: null, productId: null, direction: 'stock_out' }),
      ]),
      batchCommit: jest.fn(async () => ({
        updatedCount: 1,
        failedIds: ['scan-1'],
        failedReasons: ['Choose which item this is first.'],
        failedCodes: ['product_unresolved'],
      })),
    });
    const screen = await renderScreen(<QueueScreen client={api} warning="" />);

    await fireEvent.press(await screen.findByLabelText('Confirm all'));

    expect(await screen.findByText('Choose which item this is first.')).toBeTruthy();
    expect(screen.getByText('Which item is this?')).toBeTruthy();
    expect(screen.getByLabelText('Search')).toBeTruthy();
    expect(screen.queryByText('No close match found.')).toBeNull();
    expect(screen.getByText('Oat milk')).toBeTruthy();
    expect(api.commitScan).not.toHaveBeenCalled();
    expect(screen.queryByLabelText(/Link /)).toBeNull();
  });

  it.each([
    { label: 'never stocked', direction: 'stock_out' as const, productId: 'prod-new' as string | null, name: 'Quinoa', count: null as number | null, sentence: false },
    { label: 'wiped', direction: 'stock_out' as const, productId: 'prod-wiped', name: 'Rice cakes', count: null, sentence: false },
    { label: 'stocked', direction: 'stock_out' as const, productId: 'prod-oats', name: 'Oats', count: 2, sentence: false },
    { label: 'zero on hand', direction: 'stock_out' as const, productId: 'prod-rice', name: 'Rice', count: 0, sentence: true },
    { label: 'stock-in', direction: 'stock_in' as const, productId: 'prod-new', name: 'Quinoa', count: null, sentence: false },
    { label: 'no product yet', direction: 'stock_out' as const, productId: null, name: '999', count: null, sentence: false },
  ])('a $label card is amber when that stock-out has nothing on hand', async ({ direction, productId, name, count, sentence }) => {
    const api = client({
      listPending: jest.fn(async () => [entry({
        direction,
        productId,
        barcode: '999',
        product: productId === null ? null : { id: productId, name, category: 'Pantry', unitOfMeasure: 'bag' },
      })]),
      getInventoryList: jest.fn(async () => (
        count === null || productId === null ? [] : [householdItem(name, productId, count)]
      )),
    });
    const screen = await renderScreen(<QueueScreen client={api} warning="" />);
    const card = within(await screen.findByTestId('scan-scan-1'));

    if (sentence) {
      expect(card.getByText(NOTHING_ON_HAND_TITLE)).toBeTruthy();
      expect(card.getByText(NOTHING_ON_HAND_BODY)).toBeTruthy();
      expect(card.getByText('!')).toBeTruthy();
      expect(card.queryByLabelText(`Confirm ${name}`)).toBeNull();
      expect(card.getByLabelText(`Remove ${name}`)).toBeTruthy();
      expect(screen.getByText('1 with nothing on hand')).toBeTruthy();
      expect(screen.queryByText('Confirm skips the 1 with nothing on hand.')).toBeNull();
      expect(screen.queryByLabelText('Confirm all')).toBeNull();
    } else {
      expect(card.queryByText(NOTHING_ON_HAND_TITLE)).toBeNull();
      expect(card.queryByText(NOTHING_ON_HAND_BODY)).toBeNull();
      expect(card.getByLabelText(`Confirm ${name}`)).toBeTruthy();
      expect(screen.queryByText(/with nothing on hand/)).toBeNull();
    }
    expect(screen.queryByText('No inventory item is available for this product.')).toBeNull();
  });

  it('hides More while the link card is open', async () => {
    const api = client({
      listPending: jest.fn(async () => [entry({ direction: 'stock_out' })]),
      commitScan: jest.fn(async () => {
        throw new ApiError(409, 'Which item is this?', 'no_item', pears);
      }),
    });
    const screen = await renderScreen(<QueueScreen client={api} warning="" />);

    await fireEvent.press(await screen.findByLabelText('Confirm Oat milk'));

    expect(await screen.findByText('Which item is this?')).toBeTruthy();
    expect(screen.queryByText('More')).toBeNull();
    expect(screen.queryByLabelText('More')).toBeNull();
    expect(screen.queryByText('No inventory item is available for this product.')).toBeNull();
  });

  it.each([
    { label: 'one card', blocked: 1, pill: '1 with nothing on hand', skip: 'Confirm skips the 1 with nothing on hand.' },
    { label: 'two cards', blocked: 2, pill: '2 with nothing on hand', skip: 'Confirm skips the 2 with nothing on hand.' },
  ])('confirm all skips $label with nothing on hand', async ({ blocked, pill, skip }) => {
    const empty = entry({
      id: 'scan-empty',
      barcode: '222',
      direction: 'stock_out',
      productId: 'prod-empty',
      product: { id: 'prod-empty', name: 'Rice', category: 'Pantry', unitOfMeasure: 'bag' },
    });
    const second = entry({
      id: 'scan-empty-2',
      barcode: '111',
      direction: 'stock_out',
      productId: 'prod-empty-2',
      product: { id: 'prod-empty-2', name: 'Tea', category: 'Pantry', unitOfMeasure: 'box' },
    });
    const stocked = entry({
      id: 'scan-ok',
      barcode: '333',
      direction: 'stock_out',
      productId: 'prod-ok',
      product: { id: 'prod-ok', name: 'Oats', category: 'Grain', unitOfMeasure: 'bag' },
    });
    const cards = blocked === 1 ? [empty, stocked] : [empty, second, stocked];
    const batchCommit = jest.fn(async () => ({ updatedCount: 1, committedIds: ['scan-ok'] }));
    const api = client({
      listPending: jest.fn(async () => cards),
      batchCommit,
      getInventoryList: jest.fn(async () => [
        householdItem('Rice', 'prod-empty', 0),
        householdItem('Tea', 'prod-empty-2', 0),
        householdItem('Oats', 'prod-ok', 2),
      ]),
    });
    const screen = await renderScreen(<QueueScreen client={api} warning="" />);

    expect(await screen.findByText(pill)).toBeTruthy();
    expect(screen.getByText(skip)).toBeTruthy();
    expect(screen.queryByText('0 with nothing on hand')).toBeNull();
    expect(within(screen.getByTestId('scan-scan-empty')).queryByLabelText('Confirm Rice')).toBeNull();
    expect(within(screen.getByTestId('scan-scan-ok')).getByLabelText('Confirm Oats')).toBeTruthy();

    await fireEvent.press(screen.getByLabelText('Confirm all'));
    await waitFor(() => expect(batchCommit).toHaveBeenCalledWith(['scan-ok']));
  });

  it('does not mark every stock-out amber when the household list fails', async () => {
    const api = client({
      listPending: jest.fn(async () => [entry({ direction: 'stock_out' })]),
      getInventoryList: jest.fn(async () => {
        throw new Error('The shelf did not load.');
      }),
    });
    const screen = await renderScreen(<QueueScreen client={api} warning="" />);

    expect(await screen.findByLabelText('Confirm Oat milk')).toBeTruthy();
    expect(screen.queryByText(NOTHING_ON_HAND_TITLE)).toBeNull();
    expect(screen.queryByText('1 with nothing on hand')).toBeNull();
  });

  it('keeps a stale none_on_hand card amber until a refresh sees a new count', async () => {
    const stocked = [householdItem('Oat milk', 'prod-1', 2)];
    const getInventoryList = jest.fn()
      .mockResolvedValueOnce(stocked)
      .mockResolvedValueOnce(stocked)
      .mockResolvedValueOnce([householdItem('Oat milk', 'prod-1', 4)]);
    const api = client({
      listPending: jest.fn(async () => [entry({ direction: 'stock_out' })]),
      getInventoryList,
      commitScan: jest.fn(async () => {
        throw new ApiError(409, NOTHING_ON_HAND_TITLE, 'none_on_hand');
      }),
    });
    const screen = await renderScreen(<QueueScreen client={api} warning="" />);

    await fireEvent.press(await screen.findByLabelText('Confirm Oat milk'));
    expect(await screen.findByText(NOTHING_ON_HAND_BODY)).toBeTruthy();
    expect(screen.queryByTestId('confirm-toast')).toBeNull();

    await reloadQueue(screen);
    expect(screen.getByText(NOTHING_ON_HAND_TITLE)).toBeTruthy();
    expect(screen.queryByLabelText('Confirm Oat milk')).toBeNull();

    await reloadQueue(screen);
    expect(await screen.findByLabelText('Confirm Oat milk')).toBeTruthy();
    expect(screen.queryByText(NOTHING_ON_HAND_TITLE)).toBeNull();
  });

  it('clears a stale card when a stock-in of that product is confirmed', async () => {
    const stockOut = entry({ id: 'scan-out', direction: 'stock_out' });
    const stockIn = entry({ id: 'scan-in', barcode: '222', direction: 'stock_in' });
    const api = client({
      listPending: jest.fn(async () => [stockOut, stockIn]),
      getInventoryList: jest.fn(async () => [householdItem('Oat milk', 'prod-1', 2)]),
      commitScan: jest.fn(async (id: string) => {
        if (id === 'scan-out') throw new ApiError(409, NOTHING_ON_HAND_TITLE, 'none_on_hand');
        return entry({ id: 'scan-in', status: 'committed', direction: 'stock_in', productId: 'prod-1' });
      }),
    });
    const screen = await renderScreen(<QueueScreen client={api} warning="" />);

    await fireEvent.press(within(await screen.findByTestId('scan-scan-out')).getByLabelText('Confirm Oat milk'));
    expect(await screen.findByText(NOTHING_ON_HAND_BODY)).toBeTruthy();

    const stockInCard = within(screen.getByTestId('scan-scan-in'));
    await fireEvent.press(stockInCard.getByLabelText('Confirm Oat milk'));

    expect(await within(screen.getByTestId('scan-scan-out')).findByLabelText('Confirm Oat milk')).toBeTruthy();
    expect(within(screen.getByTestId('scan-scan-out')).queryByText(NOTHING_ON_HAND_TITLE)).toBeNull();
  });

  it('clears a zero-stock card after a stock-in refresh shows some on hand', async () => {
    const stockOut = entry({ id: 'scan-out', direction: 'stock_out' });
    const stockIn = entry({ id: 'scan-in', barcode: '222', direction: 'stock_in' });
    const getInventoryList = jest.fn()
      .mockResolvedValueOnce([householdItem('Oat milk', 'prod-1', 0)])
      .mockResolvedValue([householdItem('Oat milk', 'prod-1', 1)]);
    const api = client({
      listPending: jest.fn(async () => [stockOut, stockIn]),
      getInventoryList,
      commitScan: jest.fn(async () => entry({
        id: 'scan-in',
        status: 'committed',
        direction: 'stock_in',
        productId: 'prod-1',
      })),
    });
    const screen = await renderScreen(<QueueScreen client={api} warning="" />);

    expect(await within(await screen.findByTestId('scan-scan-out')).findByText(NOTHING_ON_HAND_BODY)).toBeTruthy();
    await fireEvent.press(within(screen.getByTestId('scan-scan-in')).getByLabelText('Confirm Oat milk'));

    expect(await within(screen.getByTestId('scan-scan-out')).findByLabelText('Confirm Oat milk')).toBeTruthy();
    expect(within(screen.getByTestId('scan-scan-out')).queryByText(NOTHING_ON_HAND_TITLE)).toBeNull();
  });

  it('removes a nothing-on-hand scan', async () => {
    const cancelScan = jest.fn(async () => entry({ status: 'cancelled' }));
    const listPending = jest.fn()
      .mockResolvedValueOnce([entry({ direction: 'stock_out', productId: 'prod-missing', product: { id: 'prod-missing', name: 'Tea', category: 'Pantry', unitOfMeasure: 'box' } })])
      .mockResolvedValue([]);
    const api = client({
      listPending,
      cancelScan,
      getInventoryList: jest.fn(async () => [householdItem('Tea', 'prod-missing', 0)]),
    });
    const screen = await renderScreen(<QueueScreen client={api} warning="" />);

    await fireEvent.press(await screen.findByLabelText('Remove Tea'));

    await waitFor(() => expect(cancelScan).toHaveBeenCalledWith('scan-1'));
    expect(await screen.findByText(/Nothing is waiting/)).toBeTruthy();
  });

  it('removing the last amber card leaves no empty banner', async () => {
    const oats = entry({
      id: 'scan-ok',
      barcode: '333',
      direction: 'stock_out',
      productId: 'prod-ok',
      product: { id: 'prod-ok', name: 'Oats', category: 'Grain', unitOfMeasure: 'bag' },
    });
    const rice = entry({
      id: 'scan-rice',
      barcode: '222',
      direction: 'stock_out',
      productId: 'prod-rice',
      product: { id: 'prod-rice', name: 'Rice', category: 'Pantry', unitOfMeasure: 'bag' },
    });
    const listPending = jest.fn()
      .mockResolvedValueOnce([oats, rice])
      .mockResolvedValueOnce([rice])
      .mockResolvedValue([]);
    const api = client({
      listPending,
      batchCommit: jest.fn(async () => ({
        updatedCount: 1,
        committedIds: ['scan-ok'],
        failedIds: ['scan-rice'],
        failedReasons: ['nothing on hand'],
        failedCodes: ['none_on_hand'],
      })),
      cancelScan: jest.fn(async () => entry({ id: 'scan-rice', status: 'cancelled' })),
      getInventoryList: jest.fn(async () => [
        householdItem('Oats', 'prod-ok', 2, 'Grain'),
        householdItem('Rice', 'prod-rice', 0),
      ]),
    });
    const screen = await renderScreen(<QueueScreen client={api} warning="" />);

    await fireEvent.press(await screen.findByLabelText('Confirm all'));
    expect(await screen.findByText(NOTHING_ON_HAND_TITLE)).toBeTruthy();
    await fireEvent.press(screen.getByLabelText('Remove Rice'));

    expect(await screen.findByText(/Nothing is waiting/)).toBeTruthy();
    expectBanner(screen, 'gone', []);
  });

  it('does not bring the banner back for a card the batch never marked', async () => {
    const oats = entry({
      id: 'scan-ok',
      barcode: '333',
      direction: 'stock_out',
      productId: 'prod-ok',
      product: { id: 'prod-ok', name: 'Oats', category: 'Grain', unitOfMeasure: 'bag' },
    });
    const rice = entry({
      id: 'scan-rice',
      barcode: '222',
      direction: 'stock_out',
      productId: 'prod-rice',
      product: { id: 'prod-rice', name: 'Rice', category: 'Pantry', unitOfMeasure: 'bag' },
    });
    const tea = entry({
      id: 'scan-tea',
      barcode: '444',
      direction: 'stock_out',
      productId: 'prod-tea',
      product: { id: 'prod-tea', name: 'Tea', category: 'Pantry', unitOfMeasure: 'box' },
    });
    const listPending = jest.fn()
      .mockResolvedValueOnce([oats, rice])
      .mockResolvedValueOnce([rice])
      .mockResolvedValueOnce([])
      .mockResolvedValue([tea]);
    const api = client({
      listPending,
      batchCommit: jest.fn(async () => ({
        updatedCount: 1,
        committedIds: ['scan-ok'],
        failedIds: ['scan-rice'],
        failedReasons: ['nothing on hand'],
        failedCodes: ['none_on_hand'],
      })),
      cancelScan: jest.fn(async () => entry({ id: 'scan-rice', status: 'cancelled' })),
      getInventoryList: jest.fn()
        .mockResolvedValueOnce([
          householdItem('Oats', 'prod-ok', 2, 'Grain'),
          householdItem('Rice', 'prod-rice', 0),
        ])
        .mockResolvedValueOnce([
          householdItem('Oats', 'prod-ok', 2, 'Grain'),
          householdItem('Rice', 'prod-rice', 0),
        ])
        .mockResolvedValue([householdItem('Tea', 'prod-tea', 0)]),
    });
    const screen = await renderScreen(<QueueScreen client={api} warning="" focused />);

    await fireEvent.press(await screen.findByLabelText('Confirm all'));
    expect(await screen.findByText(NOTHING_ON_HAND_TITLE)).toBeTruthy();
    await fireEvent.press(screen.getByLabelText('Remove Rice'));
    expect(await screen.findByText(/Nothing is waiting/)).toBeTruthy();
    expectBanner(screen, 'gone', []);

    await act(async () => {
      screen.rerender(framed(<QueueScreen client={api} warning="" focused={false} />));
    });
    await act(async () => {
      screen.rerender(framed(<QueueScreen client={api} warning="" focused />));
    });

    expect(await screen.findByText(NOTHING_ON_HAND_TITLE)).toBeTruthy();
    expect(screen.getByText('Tea')).toBeTruthy();
    expectBanner(screen, 'gone', []);
  });

  it('keeps the skip line while a single confirm is in flight', async () => {
    let releaseConfirm: (value: ScanEntry) => void = () => {};
    const rice = entry({
      id: 'scan-rice',
      barcode: '222',
      direction: 'stock_out',
      productId: 'prod-rice',
      product: { id: 'prod-rice', name: 'Rice', category: 'Pantry', unitOfMeasure: 'bag' },
    });
    const oats = entry({
      id: 'scan-ok',
      barcode: '333',
      direction: 'stock_out',
      productId: 'prod-ok',
      product: { id: 'prod-ok', name: 'Oats', category: 'Grain', unitOfMeasure: 'bag' },
    });
    const api = client({
      listPending: jest.fn(async () => [rice, oats]),
      commitScan: jest.fn(() => new Promise<ScanEntry>((resolve) => {
        releaseConfirm = resolve;
      })),
      getInventoryList: jest.fn(async () => [
        householdItem('Rice', 'prod-rice', 0),
        householdItem('Oats', 'prod-ok', 2, 'Grain'),
      ]),
    });
    const screen = await renderScreen(<QueueScreen client={api} warning="" />);

    expect(await screen.findByText('Confirm skips the 1 with nothing on hand.')).toBeTruthy();
    await fireEvent.press(screen.getByLabelText('Confirm Oats'));
    expect(screen.getByText('Confirm skips the 1 with nothing on hand.')).toBeTruthy();
    const confirmAll = screen.getByLabelText('Confirm all');
    expect(confirmAll.props.accessibilityState?.disabled ?? confirmAll.props.disabled).toBe(true);
    await act(async () => {
      releaseConfirm(entry({ id: 'scan-ok', status: 'committed' }));
    });
  });

  it('disables confirm all when every card has nothing on hand', async () => {
    const rice = entry({
      id: 'scan-rice',
      barcode: '222',
      direction: 'stock_out',
      productId: 'prod-rice',
      product: { id: 'prod-rice', name: 'Rice', category: 'Pantry', unitOfMeasure: 'bag' },
    });
    const tea = entry({
      id: 'scan-tea',
      barcode: '333',
      direction: 'stock_out',
      productId: 'prod-tea',
      product: { id: 'prod-tea', name: 'Tea', category: 'Pantry', unitOfMeasure: 'box' },
    });
    const batchCommit = jest.fn();
    const api = client({
      listPending: jest.fn(async () => [rice, tea]),
      batchCommit,
      getInventoryList: jest.fn(async () => [
        householdItem('Rice', 'prod-rice', 0),
        householdItem('Tea', 'prod-tea', 0),
      ]),
    });
    const screen = await renderScreen(<QueueScreen client={api} warning="" />);

    const confirmAll = await screen.findByLabelText('Confirm all');
    expect(confirmAll.props.accessibilityState?.disabled ?? confirmAll.props.disabled).toBe(true);
    expect(screen.getByText('2 with nothing on hand')).toBeTruthy();
    expect(screen.queryByText('Confirm skips the 2 with nothing on hand.')).toBeNull();
    await fireEvent.press(confirmAll);
    expect(batchCommit).not.toHaveBeenCalled();
  });

  it.each([
    { label: 'a single amber card and no Confirm all', cards: 'one' as const },
    { label: 'every card amber and Confirm all disabled', cards: 'all' as const },
  ])('hides the skip line for $label', async ({ cards }) => {
    const rice = entry({
      id: 'scan-rice',
      barcode: '222',
      direction: 'stock_out',
      productId: 'prod-rice',
      product: { id: 'prod-rice', name: 'Rice', category: 'Pantry', unitOfMeasure: 'bag' },
    });
    const tea = entry({
      id: 'scan-tea',
      barcode: '333',
      direction: 'stock_out',
      productId: 'prod-tea',
      product: { id: 'prod-tea', name: 'Tea', category: 'Pantry', unitOfMeasure: 'box' },
    });
    const api = client({
      listPending: jest.fn(async () => (cards === 'one' ? [rice] : [rice, tea])),
      getInventoryList: jest.fn(async () => [
        householdItem('Rice', 'prod-rice', 0),
        householdItem('Tea', 'prod-tea', 0),
      ]),
    });
    const screen = await renderScreen(<QueueScreen client={api} warning="" />);

    expect(await screen.findByText(cards === 'one' ? '1 with nothing on hand' : '2 with nothing on hand')).toBeTruthy();
    expect(screen.queryByText(/Confirm skips the /)).toBeNull();
    if (cards === 'one') {
      expect(screen.queryByLabelText('Confirm all')).toBeNull();
      return;
    }
    const confirmAll = screen.getByLabelText('Confirm all');
    expect(confirmAll.props.accessibilityState?.disabled ?? confirmAll.props.disabled).toBe(true);
  });

  it('a failed none_on_hand card turns amber and the banner is not red because of it', async () => {
    const api = client({
      listPending: jest.fn()
        .mockResolvedValueOnce([
          entry({ id: 'scan-ok', barcode: '333', direction: 'stock_out', productId: 'prod-ok', product: { id: 'prod-ok', name: 'Oats', category: 'Grain', unitOfMeasure: 'bag' } }),
          entry({ direction: 'stock_out' }),
        ])
        .mockResolvedValue([entry({ direction: 'stock_out' })]),
      batchCommit: jest.fn(async () => ({
        updatedCount: 1,
        committedIds: ['scan-ok'],
        failedIds: ['scan-1'],
        failedReasons: ['Nothing on hand to take out.'],
        failedCodes: ['none_on_hand'],
        stockOutCounts: { 'scan-ok': { removedCount: 1, requestedCount: 3 } },
      })),
      getInventoryList: jest.fn(async () => [
        householdItem('Oats', 'prod-ok', 2, 'Grain'),
        householdItem('Oat milk', 'prod-1', 2),
      ]),
    });
    const screen = await renderScreen(<QueueScreen client={api} warning="" />);

    await fireEvent.press(await screen.findByLabelText('Confirm all'));
    expect(await screen.findByText('Nothing on hand to take out.')).toBeTruthy();
    expect(screen.getByText('Only 1 of 3 Oats was on hand, so 1 was taken out.')).toBeTruthy();
    expect(screen.queryByText('Some scans could not be confirmed.')).toBeNull();
    expectBanner(screen, 'success', ['Only 1 of 3 Oats was on hand, so 1 was taken out.']);
  });

  it('a mixed batch stays red until the non-amber card is resolved', async () => {
    const waiting = [
      entry({ direction: 'stock_out' }),
      entry({ id: 'scan-2', barcode: '999', product: null, productId: null, direction: 'stock_out' }),
    ];
    const listPending = jest.fn()
      .mockResolvedValueOnce(waiting)
      .mockResolvedValueOnce(waiting)
      .mockResolvedValue([]);
    const api = client({
      listPending,
      batchCommit: jest.fn(async () => ({
        updatedCount: 0,
        failedIds: ['scan-1', 'scan-2'],
        failedReasons: ['Nothing on hand to take out.', 'Choose which item this is first.'],
        failedCodes: ['none_on_hand', 'product_unresolved'],
      })),
      commitScan: jest.fn(async () => entry({ id: 'scan-2', status: 'committed' })),
      getInventoryList: jest.fn(async () => [householdItem('Oat milk', 'prod-1', 2)]),
    });
    const screen = await renderScreen(<QueueScreen client={api} warning="" />);

    await fireEvent.press(await screen.findByLabelText('Confirm all'));
    expect(await screen.findByText('Some scans could not be confirmed.')).toBeTruthy();
    expect(screen.getByText('Nothing on hand to take out.')).toBeTruthy();
    expect(screen.getByText('Choose which item this is first.')).toBeTruthy();

    await fireEvent.press(screen.getByLabelText('Confirm 999'));
    await waitFor(() => expect(screen.queryByText('Some scans could not be confirmed.')).toBeNull());
  });

  it('does not turn every stock-out amber when inventory is malformed', async () => {
    const api = client({
      listPending: jest.fn(async () => [entry({ direction: 'stock_out' })]),
      getInventoryList: jest.fn(async () => ({ unexpected: true } as never)),
    });
    const screen = await renderScreen(<QueueScreen client={api} warning="" />);

    expect(await screen.findByLabelText('Confirm Oat milk')).toBeTruthy();
    expect(screen.queryByText(NOTHING_ON_HAND_TITLE)).toBeNull();
  });

  it('drops a removed card when the refresh fails and leaves no red banner', async () => {
    const api = client({
      listPending: jest.fn()
        .mockResolvedValueOnce([entry({
          direction: 'stock_out',
          productId: 'prod-rice',
          product: { id: 'prod-rice', name: 'Rice', category: 'Pantry', unitOfMeasure: 'bag' },
        })])
        .mockRejectedValue(new Error('The queue did not load.')),
      cancelScan: jest.fn(async () => entry({ status: 'cancelled' })),
      getInventoryList: jest.fn(async () => [householdItem('Rice', 'prod-rice', 0)]),
    });
    const screen = await renderScreen(<QueueScreen client={api} warning="" />);

    await fireEvent.press(await screen.findByLabelText('Remove Rice'));

    expect(await screen.findByText(/Nothing is waiting/)).toBeTruthy();
    expect(screen.queryByText('The queue did not load.')).toBeNull();
    expect(screen.queryByText('Some scans could not be confirmed.')).toBeNull();
  });

  it('clears amber after the screen regains focus and the shelf has stock', async () => {
    const getInventoryList = jest.fn()
      .mockResolvedValueOnce([householdItem('Oat milk', 'prod-1', 0)])
      .mockResolvedValue([householdItem('Oat milk', 'prod-1', 1)]);
    const api = client({
      listPending: jest.fn(async () => [entry({ direction: 'stock_out' })]),
      getInventoryList,
    });
    const screen = await renderScreen(<QueueScreen client={api} warning="" focused />);

    expect(await screen.findByText(NOTHING_ON_HAND_TITLE)).toBeTruthy();
    await act(async () => {
      screen.rerender(framed(<QueueScreen client={api} warning="" focused={false} />));
    });
    await act(async () => {
      screen.rerender(framed(<QueueScreen client={api} warning="" focused />));
    });

    expect(await screen.findByLabelText('Confirm Oat milk')).toBeTruthy();
    expect(screen.queryByText(NOTHING_ON_HAND_TITLE)).toBeNull();
  });

  it('shows an empty queue as an invitation to scan', async () => {
    const api = client({ listPending: jest.fn(async () => []) });
    const screen = await renderScreen(<QueueScreen client={api} warning="" />);

    expect(await screen.findByText(/Nothing is waiting/)).toBeTruthy();
    expect(screen.queryByLabelText('Confirm all')).toBeNull();
  });
});
