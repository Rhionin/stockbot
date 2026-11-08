import type { ScanEntry } from '@stockbot/shared/types';
import { mobileNothingOnHandCue } from './screens/scanNothingOnHandRow';

/**
 * Pre–Step 4 web consumer oracle copied from
 * frontend/src/components/queue/ScanQueuePage.tsx L373–378 (base 10316131):
 * pending + stock_out + productId, then pin || onHand === 0.
 * Intentionally does not import scanReportsNothingOnHand so parity is not self-referential.
 */
function baselineWebNothingOnHandCue(entry: ScanEntry, pinned: boolean): boolean {
  if (entry.status !== 'pending' || entry.direction !== 'stock_out' || entry.productId === null) return false;
  if (pinned) return true;
  return entry.onHand === 0;
}

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
    { id: 'T3-null', entry: { onHand: null as unknown as number }, pinned: false, mobile: false, web: false },
    { id: 'T3-positive', entry: { onHand: 2 }, pinned: false, mobile: false, web: false },
    { id: 'T4', entry: { onHand: undefined }, pinned: false, mobile: false, web: false },
    { id: 'T5-absent', entry: { onHand: undefined }, pinned: true, mobile: true, web: true },
    { id: 'T5-positive', entry: { onHand: 2 }, pinned: true, mobile: true, web: true },
    { id: 'T5-null', entry: { onHand: null as unknown as number }, pinned: true, mobile: true, web: true },
    { id: 'T5-zero', entry: { onHand: 0 }, pinned: true, mobile: true, web: true },
    { id: 'T6', entry: { direction: 'stock_in' as const, onHand: 0 }, pinned: false, mobile: false, web: false },
    { id: 'T7', entry: { productId: null, product: undefined, onHand: 0 }, pinned: false, mobile: false, web: false },
  ])('$id mobile=$mobile web=$web', ({ entry, pinned, mobile, web }) => {
    const row = scanFixture(entry);
    expect(mobileNothingOnHandCue(row, pinned)).toBe(mobile);
    expect(baselineWebNothingOnHandCue(row, pinned)).toBe(web);
  });

  it('preserves baseline: web ignores non-pending stock-outs; mobile still cues on response onHand', () => {
    const row = scanFixture({ status: 'committed', onHand: 0 });
    expect(baselineWebNothingOnHandCue(row, false)).toBe(false);
    expect(mobileNothingOnHandCue(row, false)).toBe(true);
  });
});
