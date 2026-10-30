import type { Device } from '@stockbot/shared/contract';

export const DEFAULT_INACTIVITY_REVERT_SECONDS = 300;
export const DEFAULT_DIRECTION: 'stock_in' | 'stock_out' = 'stock_out';

export const PRINT_ACTUAL_SIZE_INSTRUCTION = 'Print at Actual size (100%), not Fit to page.';
export const PRINT_PAIRING_CODE_LABEL = 'Print setup & wall sheet';
export const PAIRING_ON_SCREEN_TOO_SMALL_HINT =
  'Too small to scan? Print the setup sheet instead.';
export const CONNECT_SCANNER_OR_HUB_LABEL = 'Connect a scanner or hub';

export function formatPairingExpiryTime(
  expiresAt: string | Date,
  locale = 'en-US',
  timeZone?: string,
): string {
  const date = typeof expiresAt === 'string' ? new Date(expiresAt) : expiresAt;
  if (!(date instanceof Date) || Number.isNaN(date.getTime())) {
    return '—';
  }
  return date.toLocaleTimeString(locale, {
    hour: 'numeric',
    minute: '2-digit',
    hour12: true,
    ...(timeZone ? { timeZone } : {}),
  });
}

export function pairingScannerScreenFallback(): string {
  return "Can't scan it? Open the app on your phone or computer and scan this barcode from the screen.";
}

export const PAIRING_DIALOG_STEP_TWO =
  'When the scanner screen says "Scan the pairing barcode to connect", scan the barcode below.';

export const PAIRING_DIALOG_WIFI_SETUP_STEP_ONE =
  'Plug in the scanner. If its screen says READY TO SET UP, join the Wi-Fi network it shows (Scanner-Setup-…) on your phone, then enter your home Wi-Fi name and password.';

export function setupSheetPairingStepCopy(): string {
  return 'When your scanner says "Scan the pairing barcode to connect", hold it 10–30 cm (4–12 in) from this barcode and pull the trigger.';
}

export function setupSheetExpiryLine(expiresAt: string | Date, locale = 'en-US', timeZone?: string): string {
  const time = formatPairingExpiryTime(expiresAt, locale, timeZone);
  return `Use within 10 minutes · expires at ${time}`;
}

export function setupSheetExpiryHelpCopy(): string {
  return 'It works once. If it expires, get a new one in the app and print this page again.';
}

export function setupSheetCutLineCopy(): string {
  return '✂ Cut along the dashed line. Tape the bottom half next to your scanner.';
}

export type PairingKeyboardContext = 'scanner' | 'hub';

export function pairingKeyboardFallback(code: string, context: PairingKeyboardContext): string | null {
  if (context === 'scanner') {
    return pairingScannerScreenFallback();
  }
  return `No printer? Plug a USB keyboard into the hub, type this code, and press Enter: ${code}`;
}

export function pairingDialogTitle(context: 'scanner' | 'devices'): string {
  return context === 'scanner' ? 'Connect a scanner' : CONNECT_SCANNER_OR_HUB_LABEL;
}

export function deviceUntitledLabel(kind: string): string {
  if (kind === 'esp32_scanner') return 'Scanner';
  if (kind === 'pi_hub' || kind === 'box_hub') return 'Hub';
  return 'Hub';
}

export function renameDeviceModalTitle(kind: string): string {
  if (kind === 'esp32_scanner') return 'Rename scanner';
  if (kind === 'pi_hub' || kind === 'box_hub') return 'Rename hub';
  return 'Rename device';
}

export function disconnectDeviceModalTitle(kind: string): string {
  if (kind === 'esp32_scanner') return 'Disconnect scanner';
  if (kind === 'pi_hub' || kind === 'box_hub') return 'Disconnect hub';
  return 'Disconnect hub';
}

export type WallBarcodeSettings = {
  inactivityRevertSeconds: number;
  defaultDirection: 'stock_in' | 'stock_out';
};

export function defaultWallBarcodeSettings(): WallBarcodeSettings {
  return {
    inactivityRevertSeconds: DEFAULT_INACTIVITY_REVERT_SECONDS,
    defaultDirection: DEFAULT_DIRECTION,
  };
}

export function resolveWallBarcodeSettings(
  devices: Pick<Device, 'kind' | 'revokedAt' | 'inactivityRevertSeconds' | 'defaultDirection'>[],
): WallBarcodeSettings {
  const scanners = devices.filter((row) => row.kind === 'esp32_scanner' && row.revokedAt == null);
  if (scanners.length === 0) return defaultWallBarcodeSettings();
  const settingsFor = (row: (typeof scanners)[number]): WallBarcodeSettings => ({
    inactivityRevertSeconds: row.inactivityRevertSeconds ?? 300,
    defaultDirection: row.defaultDirection ?? 'stock_out',
  });
  if (scanners.length === 1) return settingsFor(scanners[0]);
  const first = settingsFor(scanners[0]);
  const allMatch = scanners.every((row) => {
    const next = settingsFor(row);
    return next.inactivityRevertSeconds === first.inactivityRevertSeconds
      && next.defaultDirection === first.defaultDirection;
  });
  return allMatch ? first : defaultWallBarcodeSettings();
}

export function formatInactivityDuration(seconds: number): string {
  const minutes = Math.trunc(seconds / 60);
  if (minutes === 1) return '1 minute';
  return `${minutes} minutes`;
}

export function stockDirectionLabel(direction: 'stock_in' | 'stock_out'): string {
  return direction === 'stock_in' ? 'Stock in' : 'Stock out';
}

export function wallBarcodeStockOutCopy(settings: WallBarcodeSettings): string {
  void settings;
  return 'Scan before you take things out. This is the normal mode.';
}

export function wallBarcodeStockInCopy(settings: WallBarcodeSettings): string {
  const duration = formatInactivityDuration(settings.inactivityRevertSeconds);
  const revert = stockDirectionLabel(settings.defaultDirection);
  return `Scan before you put groceries away. Goes back to ${revert} ${duration} after your last scan.`;
}

export const SYMCODE_DISCLOSURE_LABEL = 'Using a Symcode handheld?';
