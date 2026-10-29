import { describe, expect, it } from 'vitest';
import {
  CONNECT_SCANNER_OR_HUB_LABEL,
  PRINT_PAIRING_CODE_LABEL,
  SYMCODE_DISCLOSURE_LABEL,
  defaultWallBarcodeSettings,
  deviceUntitledLabel,
  disconnectDeviceModalTitle,
  formatInactivityDuration,
  formatPairingExpiryTime,
  pairingDialogTitle,
  pairingKeyboardFallback,
  pairingPrintBody,
  renameDeviceModalTitle,
  resolveWallBarcodeSettings,
  wallBarcodeStockInCopy,
  wallBarcodeStockOutCopy,
} from './kitCopy456';

describe('kit copy helpers (#456 table B)', () => {
  it('pairing print and keyboard fallbacks', () => {
    expect(pairingPrintBody('Test Household', '2026-06-15T14:05:00.000Z', 'en-US', 'UTC')).toBe(
      'Scan this barcode with your scanner to connect it to Test Household. It works once and expires at 2:05 PM.',
    );
    expect(pairingKeyboardFallback('SBP-2345-6789-X', 'hub')).toBe(
      'No printer? Plug a USB keyboard into the hub, type this code, and press Enter: SBP-2345-6789-X',
    );
    expect(pairingKeyboardFallback('SBP-2345-6789-X', 'scanner')).toBe(
      "Can't scan it? Scan the same barcode from your phone or computer screen in Stockbot.",
    );
  });

  it('labels, devices, and wall barcodes (B2, B4, B7–B10)', () => {
    expect(PRINT_PAIRING_CODE_LABEL).toBe('Print setup & wall sheet');
    expect(CONNECT_SCANNER_OR_HUB_LABEL).toBe('Connect a scanner or hub');
    expect(pairingDialogTitle('scanner')).toBe('Connect a scanner');
    expect(pairingDialogTitle('devices')).toBe('Connect a scanner or hub');
    expect(formatPairingExpiryTime('2026-06-15T14:05:00.000Z', 'en-US', 'UTC')).toBe('2:05 PM');
    expect(deviceUntitledLabel('esp32_scanner')).toBe('Scanner');
    expect(deviceUntitledLabel('pi_hub')).toBe('Hub');
    expect(renameDeviceModalTitle('esp32_scanner')).toBe('Rename scanner');
    expect(renameDeviceModalTitle('pi_hub')).toBe('Rename hub');
    expect(disconnectDeviceModalTitle('esp32_scanner')).toBe('Disconnect scanner');
    expect(disconnectDeviceModalTitle('box_hub')).toBe('Disconnect hub');
    expect(formatInactivityDuration(60)).toBe('1 minute');
    expect(formatInactivityDuration(180)).toBe('3 minutes');
    const threeMin = { inactivityRevertSeconds: 180, defaultDirection: 'stock_out' as const };
    expect(wallBarcodeStockOutCopy(threeMin)).toBe(
      'Scan before you take things out. This is the normal mode.',
    );
    expect(wallBarcodeStockInCopy(threeMin)).toBe(
      'Scan before you put groceries away. Goes back to Stock out 3 minutes after your last scan.',
    );
    expect(resolveWallBarcodeSettings([
      { kind: 'esp32_scanner', revokedAt: null, inactivityRevertSeconds: 180, defaultDirection: 'stock_out' },
    ])).toEqual(threeMin);
    expect(resolveWallBarcodeSettings([])).toEqual(defaultWallBarcodeSettings());
    expect(SYMCODE_DISCLOSURE_LABEL).toBe('Using a Symcode handheld?');
  });
});
