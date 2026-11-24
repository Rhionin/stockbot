export const ZXING_WASM_URL = '/zxing/zxing_reader.wasm';

export function zxingLocateFile(name: string, prefix: string): string {
  return name.endsWith('.wasm') ? ZXING_WASM_URL : prefix + name;
}

let installed: Promise<void> | null = null;

// expo-camera loads this same module when the browser has no BarcodeDetector.
// Left alone, it fetches the WASM from fastly.jsdelivr.net at scan time.
export function installSelfHostedZxing(
  load: () => Promise<Pick<typeof import('barcode-detector'), 'setZXingModuleOverrides'>> = () => import('barcode-detector'),
): Promise<void> {
  installed ??= load().then(({ setZXingModuleOverrides }) => {
    setZXingModuleOverrides({ locateFile: zxingLocateFile });
  });
  return installed;
}
