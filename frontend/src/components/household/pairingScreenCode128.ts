import { code128Bars } from './code128';

export const PAIRING_CODE128_QUIET_MODULES = 10;
/** Minimum narrow-module width in device pixels. */
export const PAIRING_CODE128_MIN_MODULE_DEVICE_PX = 2;
/** Maximum narrow-module width in CSS pixels on large screens. */
export const PAIRING_CODE128_MAX_MODULE_CSS_PX = 2;

/** @deprecated Use PAIRING_CODE128_MIN_MODULE_DEVICE_PX */
export const PAIRING_CODE128_MIN_MODULE_PX = PAIRING_CODE128_MIN_MODULE_DEVICE_PX;

export type PairingCode128Layout = {
  moduleCssPx: number;
  moduleDevicePx: number;
  svgWidthCssPx: number;
  totalModules: number;
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

/**
 * Sizes the on-screen pairing barcode so the full symbol (quiet zones included) fits
 * the available width, with integer device-pixel module edges.
 */
export function computePairingCode128Layout(
  availableCssWidth: number,
  barModules: number,
  devicePixelRatio: number,
): PairingCode128Layout {
  const totalModules = barModules + 2 * PAIRING_CODE128_QUIET_MODULES;
  const dpr = devicePixelRatio > 0 ? devicePixelRatio : 1;
  if (availableCssWidth <= 0 || totalModules <= 0) {
    const moduleDevicePx = PAIRING_CODE128_MIN_MODULE_DEVICE_PX;
    const moduleCssPx = moduleDevicePx / dpr;
    return {
      moduleCssPx,
      moduleDevicePx,
      svgWidthCssPx: totalModules * moduleCssPx,
      totalModules,
    };
  }

  const fitDevice = Math.floor((availableCssWidth * dpr) / totalModules);
  let moduleDevicePx = Math.max(PAIRING_CODE128_MIN_MODULE_DEVICE_PX, fitDevice);
  let moduleCssPx = moduleDevicePx / dpr;
  if (moduleCssPx * totalModules > availableCssWidth) {
    moduleDevicePx = Math.max(1, fitDevice);
    moduleCssPx = moduleDevicePx / dpr;
  }
  moduleCssPx = Math.min(moduleCssPx, PAIRING_CODE128_MAX_MODULE_CSS_PX);
  moduleDevicePx = Math.max(PAIRING_CODE128_MIN_MODULE_DEVICE_PX, Math.round(moduleCssPx * dpr));
  moduleCssPx = moduleDevicePx / dpr;

  return {
    moduleCssPx,
    moduleDevicePx,
    svgWidthCssPx: totalModules * moduleCssPx,
    totalModules,
  };
}

export function pairingCode128SvgWidthPx(moduleCssPx: number, barModules: number): number {
  return (barModules + 2 * PAIRING_CODE128_QUIET_MODULES) * moduleCssPx;
}

export function pairingCode128BarRuns(value: string): Array<{ x: number; width: number }> {
  return mergedBarRuns(code128Bars(value));
}
