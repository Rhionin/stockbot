// Native BarcodeDetector is fast where browsers ship it (Chrome, Edge, Safari).
// Everywhere else we load ZXing on demand so a phone can still scan.

export interface FramePoint {
  x: number;
  y: number;
}

export interface DecodedBarcode {
  value: string;
  corners: FramePoint[];
  frameWidth: number;
  frameHeight: number;
}

export interface FrameDecoder {
  detect(source: HTMLVideoElement): Promise<DecodedBarcode[]>;
}

export interface BarcodeDetectorResult {
  rawValue: string;
  boundingBox?: { x: number; y: number; width: number; height: number };
  cornerPoints?: FramePoint[];
}

export interface BarcodeDetectorInstance {
  detect(source: CanvasImageSource): Promise<BarcodeDetectorResult[]>;
}

export interface BarcodeDetectorConstructor {
  new (options?: { formats?: string[] }): BarcodeDetectorInstance;
  getSupportedFormats?: () => Promise<string[]>;
}

declare global {
  interface Window {
    BarcodeDetector?: BarcodeDetectorConstructor;
  }
}

export { DEFAULT_BARCODE_FORMATS } from '@stockbot/shared/barcodeFormats';

async function formatsForNativeDetector(requested: readonly string[]): Promise<string[]> {
  const Detector = window.BarcodeDetector;
  if (Detector === undefined || typeof Detector.getSupportedFormats !== 'function') {
    return [...requested];
  }
  try {
    const available = new Set(await Detector.getSupportedFormats());
    const matched = requested.filter((format) => available.has(format));
    if (matched.length > 0) return matched;
    return [...available];
  } catch {
    return [...requested];
  }
}

export function decodedFromNative(result: BarcodeDetectorResult, source: HTMLVideoElement): DecodedBarcode | null {
  const value = result.rawValue.trim();
  if (value === '') return null;
  let corners: FramePoint[] = [];
  if (result.cornerPoints !== undefined && result.cornerPoints.length > 0) {
    corners = result.cornerPoints.map((point) => ({ x: point.x, y: point.y }));
  } else if (result.boundingBox !== undefined) {
    const box = result.boundingBox;
    corners = [
      { x: box.x, y: box.y },
      { x: box.x + box.width, y: box.y },
      { x: box.x + box.width, y: box.y + box.height },
      { x: box.x, y: box.y + box.height },
    ];
  }
  let frameWidth = source.videoWidth || 0;
  let frameHeight = source.videoHeight || 0;
  // A frame can be located before the video element reports its size. Use the
  // corner extents so the outline still has a box to draw in.
  if ((frameWidth === 0 || frameHeight === 0) && corners.length > 0) {
    frameWidth = Math.max(frameWidth, ...corners.map((point) => point.x));
    frameHeight = Math.max(frameHeight, ...corners.map((point) => point.y));
  }
  return { value, corners, frameWidth, frameHeight };
}

async function createNativeDecoder(formats: readonly string[]): Promise<FrameDecoder | null> {
  const Detector = window.BarcodeDetector;
  if (Detector === undefined) return null;
  try {
    const supported = await formatsForNativeDetector(formats);
    const detector = new Detector({ formats: supported });
    return {
      async detect(source) {
        const results = await detector.detect(source);
        return results
          .map((result) => decodedFromNative(result, source))
          .filter((result): result is DecodedBarcode => result !== null);
      },
    };
  } catch {
    return null;
  }
}

export async function createFrameDecoder(formats: readonly string[]): Promise<FrameDecoder | null> {
  if (typeof window.BarcodeDetector !== 'undefined') {
    const native = await createNativeDecoder(formats);
    if (native) return native;
  }
  try {
    const { createZxingFrameDecoder } = await import('./zxingFrameDecoder');
    return createZxingFrameDecoder(formats);
  } catch {
    return null;
  }
}
