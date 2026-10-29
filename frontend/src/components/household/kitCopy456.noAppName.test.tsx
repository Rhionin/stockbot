import { render } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { KitPrintFooter } from './kitPrintFooter';
import { PAIRING_DIALOG_STEP_TWO, PAIRING_DIALOG_WIFI_SETUP_STEP_ONE } from './kitCopy456';
import * as kitCopy456 from './kitCopy456';

const SAMPLE_EXPIRY = '2026-06-15T14:05:00.000Z';

/** Kit print sheet titles and body copy (footer app name is checked separately). */
const SETUP_SHEET_STATIC_COPY = [
  'Connect your scanner',
  'For ',
  'Tip: you can also press the mode button on the scanner to switch.',
  'Scanner mode barcodes',
  'Tape this next to your scanner. Scan a barcode to switch modes.',
  'Taking things off the shelf',
  'Putting groceries away',
  'Symcode handheld only: turn off QR codes',
  'Scan this once after connecting a Symcode handheld so it ignores QR codes on food packages. The built-in scanner already does this.',
];

const PAIRING_DIALOG_STATIC_COPY = [
  PAIRING_DIALOG_STEP_TWO,
  PAIRING_DIALOG_WIFI_SETUP_STEP_ONE,
  'Setting up a brand-new scanner?',
  'Getting a pairing barcode…',
];

function kitCopy456Strings(): string[] {
  return [
    kitCopy456.PRINT_ACTUAL_SIZE_INSTRUCTION,
    kitCopy456.PRINT_PAIRING_CODE_LABEL,
    kitCopy456.CONNECT_SCANNER_OR_HUB_LABEL,
    kitCopy456.PAIRING_DIALOG_STEP_TWO,
    kitCopy456.PAIRING_DIALOG_WIFI_SETUP_STEP_ONE,
    kitCopy456.SYMCODE_DISCLOSURE_LABEL,
    kitCopy456.pairingScannerScreenFallback(),
    kitCopy456.setupSheetPairingStepCopy(),
    kitCopy456.setupSheetExpiryHelpCopy(),
    kitCopy456.setupSheetCutLineCopy(),
    kitCopy456.setupSheetExpiryLine(SAMPLE_EXPIRY, 'en-US', 'UTC'),
    kitCopy456.pairingKeyboardFallback('SBP-1', 'hub') ?? '',
    kitCopy456.pairingDialogTitle('scanner'),
    kitCopy456.pairingDialogTitle('devices'),
    kitCopy456.deviceUntitledLabel('esp32_scanner'),
    kitCopy456.renameDeviceModalTitle('esp32_scanner'),
    kitCopy456.disconnectDeviceModalTitle('esp32_scanner'),
    kitCopy456.wallBarcodeStockOutCopy({ inactivityRevertSeconds: 180, defaultDirection: 'stock_out' }),
    kitCopy456.wallBarcodeStockInCopy({ inactivityRevertSeconds: 180, defaultDirection: 'stock_out' }),
    kitCopy456.formatInactivityDuration(180),
    kitCopy456.stockDirectionLabel('stock_out'),
  ];
}

describe('kit copy avoids app name in customer strings (#375)', () => {
  it('kitCopy456 exports, dialog copy, and sheet static copy omit Stockbot', () => {
    const strings = [
      ...kitCopy456Strings(),
      ...PAIRING_DIALOG_STATIC_COPY,
      ...SETUP_SHEET_STATIC_COPY,
    ];
    for (const text of strings) {
      expect(text, `unexpected app name in: ${text.slice(0, 80)}`).not.toMatch(/Stockbot/i);
    }
  });

  it('allows Stockbot only in the configured print footer meta', () => {
    const { container } = render(<KitPrintFooter printableName="Setup & wall sheet" />);
    expect(container.textContent).toMatch(/Stockbot/);
  });
});
