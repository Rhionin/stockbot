import { describe, expect, it } from 'vitest';
import { computePairingCode128Layout } from './pairingScreenCode128';

describe('pairing Code128 fit boundary (no clipping)', () => {
  it('omits the barcode when the frame is even slightly narrower than 1 device px needs', () => {
    // 200 bar modules + 20 quiet = 220 modules -> 220 CSS px at DPR 1
    expect(computePairingCode128Layout(219, 200, 1).showBarcode).toBe(false);
    expect(computePairingCode128Layout(212, 200, 1).showBarcode).toBe(false);
    expect(computePairingCode128Layout(220, 200, 1)).toMatchObject({ showBarcode: true, moduleDevicePx: 1, svgWidthCssPx: 220 });
  });

  it('never picks 2 device px when 2 px would overflow the frame', () => {
    expect(computePairingCode128Layout(436, 200, 1)).toMatchObject({ moduleDevicePx: 1 });
    expect(computePairingCode128Layout(440, 200, 1)).toMatchObject({ moduleDevicePx: 2, svgWidthCssPx: 440 });
    expect(computePairingCode128Layout(175, 200, 1.25).showBarcode).toBe(false);
    expect(computePairingCode128Layout(176, 200, 1.25)).toMatchObject({ showBarcode: true, moduleDevicePx: 1 });
  });
});
