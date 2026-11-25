import { describe, expect, it } from 'vitest';
import { scannerModeFromEventData } from './scannerModeEvent';

describe('scannerModeFromEventData', () => {
  it('accepts JSON strings that encode stock_in or stock_out', () => {
    expect(scannerModeFromEventData(JSON.stringify('stock_in'))).toBe('stock_in');
    expect(scannerModeFromEventData(JSON.stringify('stock_out'))).toBe('stock_out');
  });

  it('accepts a legacy object with a mode field', () => {
    expect(scannerModeFromEventData(JSON.stringify({ mode: 'stock_in' }))).toBe('stock_in');
    expect(scannerModeFromEventData(JSON.stringify({ mode: 'stock_out' }))).toBe('stock_out');
  });

  it('returns null for invalid JSON', () => {
    expect(scannerModeFromEventData('{')).toBeNull();
    expect(scannerModeFromEventData('not json')).toBeNull();
  });

  it('returns null for JSON values that are not supported directions', () => {
    expect(scannerModeFromEventData(JSON.stringify('stock_up'))).toBeNull();
    expect(scannerModeFromEventData(JSON.stringify(42))).toBeNull();
    expect(scannerModeFromEventData(JSON.stringify(['stock_in']))).toBeNull();
    expect(scannerModeFromEventData(JSON.stringify({ mode: 'stock_up' }))).toBeNull();
    expect(scannerModeFromEventData(JSON.stringify({ direction: 'stock_in' }))).toBeNull();
  });

  it('returns null when mode is missing, null, or the wrong type', () => {
    expect(scannerModeFromEventData(JSON.stringify(null))).toBeNull();
    expect(scannerModeFromEventData(JSON.stringify({}))).toBeNull();
    expect(scannerModeFromEventData(JSON.stringify({ mode: null }))).toBeNull();
    expect(scannerModeFromEventData(JSON.stringify({ mode: 1 }))).toBeNull();
  });
});
