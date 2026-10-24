import { RESCAN_COOLDOWN_MS } from '@stockbot/shared/scanTiming';
import { DEFAULT_BARCODE_FORMATS } from '@stockbot/shared/barcodeFormats';
import type { ScannerConfig } from '@stockbot/shared/types';
import { CAMERA_BARCODE_TYPES, EXPO_BARCODE_TYPES } from './formats';
import { decideScan } from './decideScan';

const config: ScannerConfig = {
  stockInBarcode: 'STOCK_IN',
  stockOutBarcode: 'STOCK_OUT',
  currentMode: 'stock_out',
  connected: true,
};

describe('camera barcode formats', () => {
  it('requests every format the web scanner requests', () => {
    expect(Object.keys(EXPO_BARCODE_TYPES).sort()).toEqual([...DEFAULT_BARCODE_FORMATS].sort());
    expect(CAMERA_BARCODE_TYPES).toEqual(DEFAULT_BARCODE_FORMATS.map((format) => EXPO_BARCODE_TYPES[format]));
    expect(EXPO_BARCODE_TYPES.itf).toBe('itf14');
    expect(EXPO_BARCODE_TYPES.qr_code).toBe('qr');
  });
});

describe('decideScan', () => {
  it('waits for scanner config and ignores a blank read', () => {
    expect(decideScan('  ', config, null, 1_000).kind).toBe('ignore');
    expect(decideScan('111', null, null, 1_000)).toEqual({ kind: 'wait' });
  });

  it('turns control barcodes into a mode change and other codes into a product scan', () => {
    expect(decideScan('STOCK_IN', config, null, 1_000)).toEqual({ kind: 'mode', mode: 'stock_in', barcode: 'STOCK_IN' });
    expect(decideScan('STOCK_OUT', config, null, 1_000)).toEqual({ kind: 'mode', mode: 'stock_out', barcode: 'STOCK_OUT' });
    expect(decideScan(' 111222333444 ', config, null, 1_000)).toEqual({ kind: 'product', barcode: '111222333444' });
  });

  it('drops a repeat of the same code until the shared cooldown passes', () => {
    const last = { value: '111', at: 1_000 };
    expect(decideScan('111', config, last, 1_000 + RESCAN_COOLDOWN_MS).kind).toBe('ignore');
    expect(decideScan('111', config, last, 1_000 + RESCAN_COOLDOWN_MS + 1)).toEqual({ kind: 'product', barcode: '111' });
    expect(decideScan('222', config, last, 1_100)).toEqual({ kind: 'product', barcode: '222' });
  });
});
