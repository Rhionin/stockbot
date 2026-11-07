import type { ScanEntry } from '@stockbot/shared/types';
import { mobileNothingOnHandCue, webNothingOnHandCue } from './screens/scanNothingOnHandRow';

function scanFixture(overrides: Partial<ScanEntry>): ScanEntry {
  return {
    id: 'scan-1',
    userId: 'user-1',
    barcode: '111',
    scannedAt: '2026-10-16T12:00:00Z',
    direction: 'stock_out',
    unitCount: 1,
    expiresAt: null,
    status: 'pending',
    productId: 'prod-1',
    product: { id: 'prod-1', name: 'Rice', category: 'Pantry', unitOfMeasure: 'bag' },
    committedAt: null,
    createdAt: '2026-10-16T12:00:00Z',
    ...overrides,
  };
}

describe('nothing-on-hand consumer parity (step 4)', () => {
  it.each([
    { id: 'T1', entry: { onHand: 0 as number | undefined }, pinned: false, mobile: true, web: true },
    { id: 'T2', entry: { onHand: 0 }, pinned: false, mobile: true, web: true },
    { id: 'T3-absent', entry: { onHand: undefined }, pinned: false, mobile: false, web: false },
    { id: 'T3-positive', entry: { onHand: 2 }, pinned: false, mobile: false, web: false },
    { id: 'T4', entry: { onHand: undefined }, pinned: false, mobile: false, web: false },
    { id: 'T5-absent', entry: { onHand: undefined }, pinned: true, mobile: true, web: true },
    { id: 'T5-nullish', entry: { onHand: 2 }, pinned: true, mobile: true, web: true },
    { id: 'T5-zero', entry: { onHand: 0 }, pinned: true, mobile: true, web: true },
    { id: 'T6', entry: { direction: 'stock_in' as const, onHand: 0 }, pinned: false, mobile: false, web: false },
    { id: 'T7', entry: { productId: null, product: undefined, onHand: 0 }, pinned: false, mobile: false, web: false },
  ])('$id mobile=$mobile web=$web', ({ entry, pinned, mobile, web }) => {
    const row = scanFixture(entry);
    expect(mobileNothingOnHandCue(row, pinned)).toBe(mobile);
    expect(webNothingOnHandCue(row, pinned)).toBe(web);
  });

  it('preserves baseline: web ignores non-pending stock-outs; mobile still cues on response onHand', () => {
    const row = scanFixture({ status: 'committed', onHand: 0 });
    expect(webNothingOnHandCue(row, false)).toBe(false);
    expect(mobileNothingOnHandCue(row, false)).toBe(true);
  });
});
