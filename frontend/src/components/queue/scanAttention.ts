import type { ProcessingFailure, ScanDirection, ScanEntry } from '@stockbot/shared/types';
import { qrPreview } from './qrPreview';
import { BATCH_GAP_MS } from './queueUtils';

// The alert waits out the same gap that splits scan sessions. A new in or out
// scan restarts the wait; the notice fires once, when that session has gone quiet.
export const BATCH_SETTLE_MS = BATCH_GAP_MS;

export type BatchDirection = 'stock_in' | 'stock_out';

export interface AttentionNotice {
  title: string;
  body: string;
  tag: string;
  direction: ScanDirection | null;
}

export interface AttentionClock {
  setTimer(callback: () => void, delayMs: number): number;
  clearTimer(id: number): void;
}

interface OpenBatch {
  ids: Set<string>;
  timer: number | null;
}

const emptyBatch = (): OpenBatch => ({ ids: new Set(), timer: null });

const browserClock: AttentionClock = {
  setTimer(callback, delayMs) {
    return window.setTimeout(callback, delayMs);
  },
  clearTimer(id) {
    window.clearTimeout(id);
  },
};

const sentence = (message: string, fallback: string): string => {
  const text = message.trim() || fallback;
  return /[.!?]$/.test(text) ? text : `${text}.`;
};

// Flagged rows with no direction are reviewed on the Stock out tab.
const reviewDirection = (direction: ScanDirection | null): BatchDirection =>
  direction === 'stock_in' ? 'stock_in' : 'stock_out';

export const failureNotice = (failure: ProcessingFailure): AttentionNotice => ({
  title: 'Scan failed',
  body: `${sentence(failure.message, "The scan didn't finish.")} Open the scan queue to try again.`,
  tag: `pantry-scan-failure-${failure.id || failure.barcode || 'scan'}`,
  direction: null,
});

export const qrNotice = (entry: ScanEntry): AttentionNotice => {
  const preview = qrPreview(entry.barcode);
  return {
    title: 'This was a QR code, not the barcode',
    body: preview === '' ? 'Rescan the barcode.' : `${preview}. Rescan the barcode.`,
    tag: `pantry-scan-qr-${entry.id || 'scan'}`,
    direction: reviewDirection(entry.direction),
  };
};

export const unrecognizedNotice = (entry: ScanEntry): AttentionNotice => {
  const barcode = entry.barcode.trim();
  const subject = barcode === '' ? 'This barcode' : `Barcode ${barcode}`;
  return {
    title: 'Unrecognized product',
    body: `${subject} needs a product. Open the scan queue to match it.`,
    tag: `pantry-scan-unrecognized-${entry.id || barcode || 'scan'}`,
    direction: reviewDirection(entry.direction),
  };
};

export const batchNotice = (direction: BatchDirection, count: number): AttentionNotice => {
  const label = direction === 'stock_in' ? 'Stock in' : 'Stock out';
  const waiting = count === 1 ? '1 scan is waiting' : `${count} scans are waiting`;
  const pronoun = count === 1 ? 'it' : 'them';
  return {
    title: `${label} batch is ready`,
    body: `${waiting}. Open the scan queue to confirm ${pronoun}.`,
    tag: `pantry-scan-batch-${direction}`,
    direction,
  };
};

const isUnrecognized = (entry: ScanEntry): boolean =>
  entry.status === 'flagged' || (entry.status === 'pending' && entry.productId === null);

const batchDirection = (entry: ScanEntry): BatchDirection | null => {
  if (entry.status !== 'pending' || entry.productId === null) return null;
  if (entry.direction === 'stock_in' || entry.direction === 'stock_out') return entry.direction;
  return null;
};

// ScanAttention turns the scan queue's live events into at most one notice per
// failure, one per unrecognized product, and one per in/out session after it
// goes quiet. It does not read the initial queue: only events passed to it.
export class ScanAttention {
  private readonly announced = new Set<string>();
  private readonly batches: Record<BatchDirection, OpenBatch> = {
    stock_in: emptyBatch(),
    stock_out: emptyBatch(),
  };
  private readonly emit: (notice: AttentionNotice) => void;
  private readonly clock: AttentionClock;
  private readonly settleMs: number;

  constructor(
    emit: (notice: AttentionNotice) => void,
    clock: AttentionClock = browserClock,
    settleMs = BATCH_SETTLE_MS,
  ) {
    this.emit = emit;
    this.clock = clock;
    this.settleMs = settleMs;
  }

  noteFailure(failure: ProcessingFailure): void {
    const key = failure.id !== '' ? `failure:${failure.id}` : `failure:${failure.barcode}:${failure.message}`;
    if (this.announced.has(key)) return;
    this.announced.add(key);
    this.emit(failureNotice(failure));
  }

  noteScan(entry: ScanEntry): void {
    if (entry.id === '') return;
    if (entry.status === 'qr') {
      this.drop(entry.id);
      const key = `qr:${entry.id}`;
      if (this.announced.has(key)) return;
      this.announced.add(key);
      this.emit(qrNotice(entry));
      return;
    }
    if (isUnrecognized(entry)) {
      this.drop(entry.id);
      const key = `unrecognized:${entry.id}`;
      if (this.announced.has(key)) return;
      this.announced.add(key);
      this.emit(unrecognizedNotice(entry));
      return;
    }
    if (entry.status === 'committed' || entry.status === 'cancelled') {
      this.drop(entry.id);
      return;
    }
    const direction = batchDirection(entry);
    if (direction === null) return;
    this.place(entry.id, direction);
  }

  dispose(): void {
    this.disarm('stock_in');
    this.disarm('stock_out');
  }

  private place(id: string, direction: BatchDirection): void {
    const other: BatchDirection = direction === 'stock_in' ? 'stock_out' : 'stock_in';
    if (this.batches[other].ids.delete(id) && this.batches[other].ids.size === 0) {
      this.disarm(other);
    }
    this.batches[direction].ids.add(id);
    this.arm(direction);
  }

  private drop(id: string): void {
    for (const direction of ['stock_in', 'stock_out'] as const) {
      if (!this.batches[direction].ids.delete(id)) continue;
      if (this.batches[direction].ids.size === 0) this.disarm(direction);
    }
  }

  private arm(direction: BatchDirection): void {
    const batch = this.batches[direction];
    this.disarm(direction);
    batch.timer = this.clock.setTimer(() => {
      batch.timer = null;
      const count = batch.ids.size;
      batch.ids.clear();
      if (count > 0) this.emit(batchNotice(direction, count));
    }, this.settleMs);
  }

  private disarm(direction: BatchDirection): void {
    const batch = this.batches[direction];
    if (batch.timer === null) return;
    this.clock.clearTimer(batch.timer);
    batch.timer = null;
  }
}
