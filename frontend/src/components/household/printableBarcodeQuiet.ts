export function printableBarcodeQuietMm(moduleMm: number): number {
  return Math.max(6, 10 * moduleMm);
}
