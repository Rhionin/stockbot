import { describe, expect, it } from 'vitest';
import { emptyProcessing, processingAdd, processingFail, processingSettle } from './processingNotices';
import type { ProcessingNotice } from './types';

const notice = (id: string, barcode = '999'): ProcessingNotice => ({
  id,
  userId: 'user-1',
  barcode,
  direction: 'stock_in',
  scannedAt: '2026-10-16T12:00:00Z',
});

describe('processing notices', () => {
  it('settles the oldest matching lookup and does not accept it again', () => {
    let state = processingAdd(emptyProcessing, notice('a'));
    state = processingSettle(state, { barcode: '999', direction: 'stock_in', status: 'pending' });
    expect(state.notices).toEqual([]);
    expect(processingAdd(state, notice('a')).notices).toEqual([]);
  });

  it('leaves the state alone for a scan that settles nothing', () => {
    const state = processingAdd(emptyProcessing, notice('a'));
    expect(processingSettle(state, { barcode: '111', direction: 'stock_in', status: 'pending' })).toBe(state);
    expect(processingSettle(state, { barcode: '999', direction: 'stock_in', status: 'committed' })).toBe(state);
  });

  it('ignores a failure for an id it never showed', () => {
    const state = processingAdd(emptyProcessing, notice('a'));
    const result = processingFail(state, { id: 'other', barcode: '999', message: 'No.' });
    expect(result.state).toBe(state);
    expect(result.message).toBeNull();
  });

  it('removes a failed lookup, returns its message and does not accept it again', () => {
    const state = processingAdd(emptyProcessing, notice('a'));
    const result = processingFail(state, { id: 'a', barcode: '999', message: "Couldn't look up that barcode." });
    expect(result.state.notices).toEqual([]);
    expect(result.message).toBe("Couldn't look up that barcode.");
    expect(processingAdd(result.state, notice('a')).notices).toEqual([]);
  });
});
