import type { BarcodeType } from 'expo-camera';
import { DEFAULT_BARCODE_FORMATS, type WebBarcodeFormat } from '../../../frontend/src/components/scanner/barcodeFormats';

// Expo names these differently from the browser BarcodeDetector. `itf` on the
// web is Interleaved 2 of 5; the camera module exposes that family as `itf14`.
export const EXPO_BARCODE_TYPES: Record<WebBarcodeFormat, BarcodeType> = {
  ean_13: 'ean13',
  ean_8: 'ean8',
  upc_a: 'upc_a',
  upc_e: 'upc_e',
  code_128: 'code128',
  code_39: 'code39',
  itf: 'itf14',
  qr_code: 'qr',
};

export const CAMERA_BARCODE_TYPES: BarcodeType[] = DEFAULT_BARCODE_FORMATS.map(
  (format) => EXPO_BARCODE_TYPES[format],
);
