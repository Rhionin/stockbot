// Formats requested from the browser BarcodeDetector and from the phone camera.
// Keep this list in one place so the Expo app scans the same codes as the web scanner.

export const DEFAULT_BARCODE_FORMATS = [
  'ean_13',
  'ean_8',
  'upc_a',
  'upc_e',
  'code_128',
  'code_39',
  'itf',
  'qr_code',
] as const;

export type WebBarcodeFormat = (typeof DEFAULT_BARCODE_FORMATS)[number];
