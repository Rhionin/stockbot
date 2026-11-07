import { afterEach, describe, expect, it, vi } from 'vitest';
import {
  SCAN_ALERT_OPEN,
  openScanAlert,
  takeScanAlertDirection,
  type ScanAlertOpenDetail,
} from './browserScanAlert';

const STORAGE_KEY = 'pantry-scan-alert-direction';

describe('openScanAlert and takeScanAlertDirection', () => {
  afterEach(() => {
    sessionStorage.clear();
    vi.restoreAllMocks();
  });

  it('persists stock_in and stock_out then consumes the direction once', () => {
    openScanAlert('stock_in');
    expect(sessionStorage.getItem(STORAGE_KEY)).toBe('stock_in');
    expect(takeScanAlertDirection()).toBe('stock_in');
    expect(sessionStorage.getItem(STORAGE_KEY)).toBeNull();
    expect(takeScanAlertDirection()).toBeNull();

    openScanAlert('stock_out');
    expect(takeScanAlertDirection()).toBe('stock_out');
    expect(sessionStorage.getItem(STORAGE_KEY)).toBeNull();
  });

  it('clears storage for null and other directions', () => {
    sessionStorage.setItem(STORAGE_KEY, 'stock_in');

    openScanAlert(null);
    expect(sessionStorage.getItem(STORAGE_KEY)).toBeNull();
    expect(takeScanAlertDirection()).toBeNull();

    sessionStorage.setItem(STORAGE_KEY, 'stock_out');
    openScanAlert('stock_up' as import('@stockbot/shared/types').ScanDirection);
    expect(sessionStorage.getItem(STORAGE_KEY)).toBeNull();
  });

  it('dispatches a one-shot pantry-scan-alert-open event with direction detail', () => {
    const seen: ScanAlertOpenDetail[] = [];
    const listener = (event: Event) => {
      const custom = event as CustomEvent<ScanAlertOpenDetail>;
      seen.push(custom.detail);
    };
    window.addEventListener(SCAN_ALERT_OPEN, listener);

    openScanAlert('stock_out');
    openScanAlert(null);

    expect(seen).toEqual([{ direction: 'stock_out' }, { direction: null }]);
    window.removeEventListener(SCAN_ALERT_OPEN, listener);
  });

  it('returns null when storage holds an unexpected value', () => {
    sessionStorage.setItem(STORAGE_KEY, 'garbage');
    expect(takeScanAlertDirection()).toBeNull();
    expect(sessionStorage.getItem(STORAGE_KEY)).toBeNull();
  });
});
