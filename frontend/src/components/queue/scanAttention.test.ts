import { describe, expect, it } from 'vitest';
import type { ProcessingFailure, ScanEntry } from '../../types';
import { BATCH_GAP_MS } from './queueUtils';
import { BATCH_SETTLE_MS, ScanAttention, type AttentionClock, type AttentionNotice } from './scanAttention';

class ManualClock implements AttentionClock {
  now = 0;
  private nextId = 1;
  private timers = new Map<number, { at: number; fn: () => void }>();

  setTimer(fn: () => void, delayMs: number): number {
    const id = this.nextId;
    this.nextId += 1;
    this.timers.set(id, { at: this.now + delayMs, fn });
    return id;
  }

  clearTimer(id: number): void {
    this.timers.delete(id);
  }

  advance(ms: number): void {
    this.now += ms;
    for (;;) {
      const due = [...this.timers.entries()]
        .filter(([, timer]) => timer.at <= this.now)
        .sort((left, right) => left[1].at - right[1].at || left[0] - right[0]);
      if (due.length === 0) return;
      const [id, timer] = due[0];
      this.timers.delete(id);
      timer.fn();
    }
  }
}

const scan = (overrides: Partial<ScanEntry>): ScanEntry => ({
  id: 'scan-1',
  userId: 'user-1',
  barcode: '111',
  scannedAt: '2026-03-20T10:00:00Z',
  direction: 'stock_in',
  unitCount: 1,
  expiresAt: null,
  status: 'pending',
  productId: 'product-1',
  product: { id: 'product-1', name: 'Milk', category: 'Dairy', unitOfMeasure: 'carton' },
  committedAt: null,
  createdAt: '2026-03-20T10:00:00Z',
  ...overrides,
});

const failure = (overrides: Partial<ProcessingFailure> = {}): ProcessingFailure => ({
  id: 'lookup-1',
  barcode: '999',
  message: "Couldn't look up that barcode.",
  ...overrides,
});

const harness = () => {
  const notices: AttentionNotice[] = [];
  const clock = new ManualClock();
  const attention = new ScanAttention((notice) => {
    notices.push(notice);
  }, clock);
  return { notices, clock, attention };
};

describe('ScanAttention', () => {
  it('uses the scan session gap as the batch quiet period', () => {
    expect(BATCH_SETTLE_MS).toBe(5 * 60 * 1000);
    expect(BATCH_SETTLE_MS).toBe(BATCH_GAP_MS);
  });

  it('notifies once, immediately, when a lookup fails', () => {
    const { notices, attention } = harness();
    attention.noteFailure(failure());
    attention.noteFailure(failure());
    expect(notices).toEqual([
      {
        title: 'Scan failed',
        body: "Couldn't look up that barcode. Open the scan queue to try again.",
        tag: 'pantry-scan-failure-lookup-1',
        direction: null,
      },
    ]);
  });

  it('notifies once, immediately, for an unrecognized product and opens its review tab', () => {
    const { notices, attention } = harness();
    const flagged = scan({
      id: 'flag-1',
      barcode: '000111',
      status: 'flagged',
      direction: null,
      productId: null,
      product: undefined,
    });
    attention.noteScan(flagged);
    attention.noteScan({ ...flagged, unitCount: 2 });
    expect(notices).toEqual([
      {
        title: 'Unrecognized product',
        body: 'Barcode 000111 needs a product. Open the scan queue to match it.',
        tag: 'pantry-scan-unrecognized-flag-1',
        direction: 'stock_out',
      },
    ]);
  });

  it('opens a stock-in unrecognized product on the stock in tab', () => {
    const { notices, attention } = harness();
    attention.noteScan(scan({
      id: 'flag-2',
      status: 'flagged',
      direction: 'stock_in',
      productId: null,
      product: undefined,
    }));
    expect(notices[0]?.direction).toBe('stock_in');
  });

  it('does not notify when a recognized scan arrives', () => {
    const { notices, clock, attention } = harness();
    attention.noteScan(scan({ id: 'milk' }));
    clock.advance(BATCH_SETTLE_MS - 1);
    expect(notices).toEqual([]);
  });

  it('notifies once after the batch stays quiet, and a later scan restarts the wait', () => {
    const { notices, clock, attention } = harness();
    attention.noteScan(scan({ id: 'milk', barcode: '111' }));
    clock.advance(4 * 60 * 1000);
    attention.noteScan(scan({ id: 'oats', barcode: '222', productId: 'product-2' }));
    clock.advance(4 * 60 * 1000);
    expect(notices).toEqual([]);
    clock.advance(60 * 1000);
    expect(notices).toEqual([
      {
        title: 'Stock in batch is ready',
        body: '2 scans are waiting. Open the scan queue to confirm them.',
        tag: 'pantry-scan-batch-stock_in',
        direction: 'stock_in',
      },
    ]);
  });

  it('restarts the quiet period when a scan already in the batch is updated', () => {
    const { notices, clock, attention } = harness();
    attention.noteScan(scan({ id: 'milk', unitCount: 1 }));
    clock.advance(4 * 60 * 1000);
    attention.noteScan(scan({ id: 'milk', unitCount: 2 }));
    clock.advance(4 * 60 * 1000);
    expect(notices).toEqual([]);
    clock.advance(60 * 1000);
    expect(notices).toHaveLength(1);
    expect(notices[0]?.body).toBe('1 scan is waiting. Open the scan queue to confirm it.');
  });

  it('keeps stock in and stock out batches on separate timers', () => {
    const { notices, clock, attention } = harness();
    attention.noteScan(scan({ id: 'in', direction: 'stock_in' }));
    clock.advance(3 * 60 * 1000);
    attention.noteScan(scan({ id: 'out', direction: 'stock_out' }));
    clock.advance(2 * 60 * 1000);
    expect(notices.map((notice) => notice.direction)).toEqual(['stock_in']);
    clock.advance(3 * 60 * 1000);
    expect(notices.map((notice) => notice.direction)).toEqual(['stock_in', 'stock_out']);
  });

  it('does not let a failure or an unrecognized product reset the batch timer', () => {
    const { notices, clock, attention } = harness();
    attention.noteScan(scan({ id: 'milk' }));
    clock.advance(4 * 60 * 1000);
    attention.noteFailure(failure());
    attention.noteScan(scan({
      id: 'flag',
      status: 'flagged',
      productId: null,
      product: undefined,
      barcode: '333',
    }));
    expect(notices.map((notice) => notice.title)).toEqual(['Scan failed', 'Unrecognized product']);
    clock.advance(60 * 1000);
    expect(notices.map((notice) => notice.title)).toEqual([
      'Scan failed',
      'Unrecognized product',
      'Stock in batch is ready',
    ]);
    expect(notices[2]?.body).toBe('1 scan is waiting. Open the scan queue to confirm it.');
  });

  it('skips the batch alert when every scan in it was confirmed', () => {
    const { notices, clock, attention } = harness();
    attention.noteScan(scan({ id: 'milk' }));
    attention.noteScan(scan({ id: 'oats', barcode: '222' }));
    attention.noteScan(scan({ id: 'milk', status: 'committed' }));
    attention.noteScan(scan({ id: 'oats', status: 'cancelled' }));
    clock.advance(BATCH_SETTLE_MS);
    expect(notices).toEqual([]);
  });

  it('counts only the scans still waiting when the batch settles', () => {
    const { notices, clock, attention } = harness();
    attention.noteScan(scan({ id: 'milk' }));
    attention.noteScan(scan({ id: 'oats', barcode: '222' }));
    attention.noteScan(scan({ id: 'milk', status: 'committed' }));
    clock.advance(BATCH_SETTLE_MS);
    expect(notices[0]?.body).toBe('1 scan is waiting. Open the scan queue to confirm it.');
  });

  it('starts a new quiet period after a resolved unrecognized product becomes pending', () => {
    const { notices, clock, attention } = harness();
    attention.noteScan(scan({
      id: 'flag',
      status: 'flagged',
      productId: null,
      product: undefined,
    }));
    clock.advance(4 * 60 * 1000);
    attention.noteScan(scan({ id: 'flag', status: 'pending', productId: 'product-9' }));
    clock.advance(4 * 60 * 1000);
    expect(notices.map((notice) => notice.title)).toEqual(['Unrecognized product']);
    clock.advance(60 * 1000);
    expect(notices.map((notice) => notice.title)).toEqual(['Unrecognized product', 'Stock in batch is ready']);
  });

  it('notifies a QR misread immediately and leaves the batch timer alone', () => {
    const { notices, clock, attention } = harness();
    attention.noteScan(scan({ id: 'milk' }));
    clock.advance(4 * 60 * 1000);
    attention.noteScan(scan({
      id: 'qr-1',
      barcode: 'https://example.com/product/3017620422003',
      status: 'qr',
      productId: null,
      product: undefined,
    }));
    attention.noteScan(scan({
      id: 'qr-1',
      barcode: 'https://example.com/again',
      status: 'qr',
      productId: null,
      product: undefined,
    }));
    expect(notices.map((notice) => notice.title)).toEqual(['This was a QR code, not the barcode']);
    expect(notices[0]?.body).toBe('example.com. Rescan the barcode.');
    expect(notices[0]?.direction).toBe('stock_in');
    clock.advance(60 * 1000);
    expect(notices.map((notice) => notice.title)).toEqual([
      'This was a QR code, not the barcode',
      'Stock in batch is ready',
    ]);
  });

  it('does not fire a batch alert after dispose', () => {
    const { notices, clock, attention } = harness();
    attention.noteScan(scan({ id: 'milk' }));
    attention.dispose();
    clock.advance(BATCH_SETTLE_MS);
    expect(notices).toEqual([]);
  });
});
