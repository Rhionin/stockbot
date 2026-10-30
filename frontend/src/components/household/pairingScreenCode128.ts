import { code128Bars } from './code128';

export const PAIRING_CODE128_QUIET_MODULES = 10;
/** Target module width in device pixels when the frame has room. */
export const PAIRING_CODE128_TARGET_MODULE_DEVICE_PX = 2;
/** @deprecated Use PAIRING_CODE128_TARGET_MODULE_DEVICE_PX; hint threshold matches this value. */
export const PAIRING_CODE128_MIN_MODULE_DEVICE_PX = PAIRING_CODE128_TARGET_MODULE_DEVICE_PX;

export type PairingCode128Layout = {
  moduleCssPx: number;
  moduleDevicePx: number;
  svgWidthCssPx: number;
  totalModules: number;
  showBarcode: boolean;
};

function mergedBarRuns(bars: boolean[]): Array<{ x: number; width: number }> {
  const runs: Array<{ x: number; width: number }> = [];
  let index = 0;
  while (index < bars.length) {
    if (!bars[index]) {
      index += 1;
      continue;
    }
    const start = index;
    while (index < bars.length && bars[index]) index += 1;
    runs.push({ x: start + PAIRING_CODE128_QUIET_MODULES, width: index - start });
  }
  return runs;
}

export function pairingCode128ModuleCount(value: string): number {
  return code128Bars(value).length;
}

function moduleWidthFits(
  moduleDevicePx: number,
  totalModules: number,
  availableCssWidth: number,
  dpr: number,
): boolean {
  return (totalModules * moduleDevicePx) / dpr <= availableCssWidth + 0.05;
}

function largestFittingModuleDevicePx(
  availableCssWidth: number,
  totalModules: number,
  dpr: number,
): number {
  let moduleDevicePx = Math.max(1, Math.floor((availableCssWidth * dpr) / totalModules));
  while (moduleDevicePx > 1 && !moduleWidthFits(moduleDevicePx, totalModules, availableCssWidth, dpr)) {
    moduleDevicePx -= 1;
  }
  return moduleDevicePx;
}

/**
 * Sizes the on-screen pairing barcode: largest whole device-pixel module width that fits,
 * capped at {@link PAIRING_CODE128_TARGET_MODULE_DEVICE_PX}. Omits the barcode when even 1 device
 * px plus quiet zones would not fit.
 */
export function computePairingCode128Layout(
  availableCssWidth: number,
  barModules: number,
  devicePixelRatio: number,
): PairingCode128Layout {
  const totalModules = barModules + 2 * PAIRING_CODE128_QUIET_MODULES;
  const dpr = devicePixelRatio > 0 ? devicePixelRatio : 1;
  const noBarcode: PairingCode128Layout = {
    moduleCssPx: 0,
    moduleDevicePx: 0,
    svgWidthCssPx: 0,
    totalModules,
    showBarcode: false,
  };

  if (availableCssWidth <= 0 || totalModules <= 0 || !moduleWidthFits(1, totalModules, availableCssWidth, dpr)) {
    return noBarcode;
  }

  const largest = largestFittingModuleDevicePx(availableCssWidth, totalModules, dpr);
  const moduleDevicePx = Math.min(PAIRING_CODE128_TARGET_MODULE_DEVICE_PX, largest);
  const moduleCssPx = moduleDevicePx / dpr;

  return {
    moduleCssPx,
    moduleDevicePx,
    svgWidthCssPx: totalModules * moduleCssPx,
    totalModules,
    showBarcode: true,
  };
}

export function pairingCode128ShowsTooSmallHint(layout: Pick<PairingCode128Layout, 'moduleDevicePx' | 'showBarcode'>): boolean {
  return !layout.showBarcode || layout.moduleDevicePx < PAIRING_CODE128_TARGET_MODULE_DEVICE_PX;
}

export function pairingCode128SvgWidthPx(moduleCssPx: number, barModules: number): number {
  return (barModules + 2 * PAIRING_CODE128_QUIET_MODULES) * moduleCssPx;
}

export function pairingCode128BarRuns(value: string): Array<{ x: number; width: number }> {
  return mergedBarRuns(code128Bars(value));
}
