import { describe, expect, it } from 'vitest';
import { processingBarcodeLine, processingCardLabel, qrCardLabel } from './queueCards';

describe('queue card copy', () => {
  it('names a QR card with its preview, and without one when the payload is empty', () => {
    expect(qrCardLabel('example.com')).toBe('This was a QR code, not the barcode. example.com');
    expect(qrCardLabel('')).toBe('This was a QR code, not the barcode');
  });

  it('names a processing card by its barcode', () => {
    expect(processingBarcodeLine('999')).toBe('Barcode: 999');
    expect(processingCardLabel('999')).toBe('Scan 999 processing');
  });
});
