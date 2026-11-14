import { describe, expect, it, vi } from 'vitest';
import type { ShoppingListGroupMember } from '@stockbot/shared/types';
import {
  barcodeTail,
  buildTripBuyViews,
  deterministicBarcode,
  formatBought,
  groupThumbnailsCollide,
  lastBoughtDiffers,
  memberOnHand,
  pickDifferentiatorField,
  thumbnailsCollidePair,
} from './tripBuyDifferentiator';
import {
  TRIP_BUY_BOUGHT_CURRENT_YEAR_EXAMPLE,
  TRIP_BUY_BOUGHT_OTHER_YEAR_EXAMPLE,
  TRIP_BUY_SAME_NAME_SENTENCE,
} from './tripBuyCopy';

const member = (overrides: Partial<ShoppingListGroupMember> & Pick<ShoppingListGroupMember, 'itemId' | 'name'>): ShoppingListGroupMember => ({
  productId: `prod-${overrides.itemId}`,
  onHand: 0,
  ...overrides,
});

describe('tripBuyDifferentiator', () => {
  it('shows size when photos and names collide', () => {
    const members = [
      member({ itemId: 'a', name: 'KROGER CUT GREEN BEANS', netAmount: 14.5, netUnit: 'oz' }),
      member({ itemId: 'b', name: 'Kroger Cut Green Beans', netAmount: 8, netUnit: 'oz' }),
    ];
    expect(pickDifferentiatorField(members)).toBe('size');
    const views = buildTripBuyViews(members);
    expect(views[0]?.accessibleName).toBe('Kroger Cut Green Beans, 14.5 oz');
    expect(views[1]?.accessibleName).toBe('Kroger Cut Green Beans, 8 oz');
  });

  it('skips differentiator when image URLs differ', () => {
    const members = [
      member({ itemId: 'a', name: 'Sample Product A', imageUrl: 'https://example.test/a.jpg' }),
      member({ itemId: 'b', name: 'Sample Product B', imageUrl: 'https://example.test/b.jpg' }),
    ];
    expect(pickDifferentiatorField(members)).toBeUndefined();
    expect(buildTripBuyViews(members).every((view) => view.differentiator === undefined)).toBe(true);
  });

  it('uses barcode tail only when tails differ', () => {
    const members = [
      member({ itemId: 'a', name: 'Same Label', barcodes: ['000000111111'] }),
      member({ itemId: 'b', name: 'SAME LABEL', barcodes: ['000000222222'] }),
    ];
    expect(pickDifferentiatorField(members)).toBe('barcode');
    expect(buildTripBuyViews(members)[0]?.differentiator).toBe('…1111');
    const sameTail = [
      member({ itemId: 'c', name: 'Same Label', barcodes: ['990000004821'] }),
      member({ itemId: 'd', name: 'same label', barcodes: ['880000004821'] }),
    ];
    expect(pickDifferentiatorField(sameTail)).toBe('fallback');
    expect(buildTripBuyViews(sameTail)[0]?.differentiator).toBe(TRIP_BUY_SAME_NAME_SENTENCE);
  });

  it('picks deterministic barcode sorted ascending', () => {
    expect(deterministicBarcode(['999', '111', '555'])).toBe('111');
    expect(barcodeTail(['000000011111', '999999999999'])).toBe('…1111');
  });

  it('treats missing onHand as absent from the on-hand differentiator', () => {
    const withMissing = [
      { itemId: 'a', productId: 'p1', name: 'Same', onHand: 2 } as ShoppingListGroupMember,
      { itemId: 'b', productId: 'p2', name: 'same' } as ShoppingListGroupMember,
    ];
    expect(memberOnHand(withMissing[1]!)).toBeUndefined();
    expect(pickDifferentiatorField(withMissing)).not.toBe('onHand');
  });

  it('ignores same-day last bought when picking a field', () => {
    const sameDay = [
      member({ itemId: 'a', name: 'Same', lastStockedAt: '2026-10-03T10:00:00Z' }),
      member({ itemId: 'b', name: 'same', lastStockedAt: '2026-10-03T22:00:00Z' }),
    ];
    expect(lastBoughtDiffers(sameDay)).toBe(false);
    const differentDay = [
      member({ itemId: 'c', name: 'Same', lastStockedAt: '2026-10-03T10:00:00Z' }),
      member({ itemId: 'd', name: 'same', lastStockedAt: '2026-10-04T10:00:00Z' }),
    ];
    expect(lastBoughtDiffers(differentDay)).toBe(true);
    expect(pickDifferentiatorField(differentDay)).toBe('lastBought');
  });

  it('formats bought copy in America/Denver', () => {
    vi.stubEnv('TZ', 'UTC');
    expect(formatBought('2026-10-03T12:00:00Z', new Date('2026-11-01T00:00:00Z'))).toBe(TRIP_BUY_BOUGHT_CURRENT_YEAR_EXAMPLE);
    expect(formatBought('2025-10-03T12:00:00Z', new Date('2026-11-01T00:00:00Z'))).toBe(TRIP_BUY_BOUGHT_OTHER_YEAR_EXAMPLE);
  });

  it('detects thumbnail collisions per pair rules', () => {
    const a = member({ itemId: 'a', name: 'A', imageUrl: 'https://example.test/x.jpg' });
    const b = member({ itemId: 'b', name: 'B', imageUrl: 'https://example.test/x.jpg' });
    const c = member({ itemId: 'c', name: 'C' });
    const d = member({ itemId: 'd', name: 'D', imageUrl: 'https://example.test/y.jpg' });
    expect(thumbnailsCollidePair(a, b)).toBe(true);
    expect(thumbnailsCollidePair(a, c)).toBe(false);
    expect(groupThumbnailsCollide([a, b])).toBe(true);
    expect(groupThumbnailsCollide([c, member({ itemId: 'e', name: 'E' })])).toBe(true);
    expect(groupThumbnailsCollide([a, d])).toBe(false);
  });
});