import { code128Bars } from './code128';

export const PAIRING_CODE128_QUIET_MODULES = 10;
export const PAIRING_CODE128_MIN_MODULE_PX = 2;

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

/** Integer CSS pixels per narrow module for on-screen pairing barcodes. */
export function computePairingCode128ModulePx(containerWidth: number, modules: number): number {
  const totalModules = modules + 2 * PAIRING_CODE128_QUIET_MODULES;
  if (containerWidth <= 0 || totalModules <= 0) return PAIRING_CODE128_MIN_MODULE_PX;
  const fit = Math.floor(containerWidth / totalModules);
  return Math.max(PAIRING_CODE128_MIN_MODULE_PX, fit);
}

export function pairingCode128SvgWidthPx(modulePx: number, modules: number): number {
  return modulePx * (modules + 2 * PAIRING_CODE128_QUIET_MODULES);
}

export function pairingCode128BarRuns(value: string): Array<{ x: number; width: number }> {
  return mergedBarRuns(code128Bars(value));
}
