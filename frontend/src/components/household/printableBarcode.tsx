import {
  PAIRING_CODE128_QUIET_MODULES,
  pairingCode128BarRuns,
  pairingCode128ModuleCount,
} from './pairingScreenCode128';
import { printableBarcodeQuietMm } from './printableBarcodeQuiet';

export function PrintableBarcode({
  value,
  moduleMm,
  heightMm,
}: {
  value: string;
  moduleMm: number;
  heightMm: number;
}) {
  const barModules = pairingCode128ModuleCount(value);
  const totalModules = barModules + 2 * PAIRING_CODE128_QUIET_MODULES;
  const widthMm = totalModules * moduleMm;
  const quietMm = printableBarcodeQuietMm(moduleMm);
  const runs = pairingCode128BarRuns(value);

  return (
    <div
      className="printable-barcode-block"
      style={{ padding: `${quietMm}mm` }}
      data-quiet-mm={quietMm}
      data-module-mm={moduleMm}
    >
      <svg
        data-barcode
        data-value={value}
        data-modules={barModules}
        data-total-modules={totalModules}
        className="printable-barcode-svg"
        width={`${widthMm}mm`}
        height={`${heightMm}mm`}
        viewBox={`0 0 ${totalModules} 48`}
        preserveAspectRatio="none"
        shapeRendering="crispEdges"
        role="img"
        aria-label={`Code 128 ${value}`}
      >
        {runs.map((run) => (
          <rect
            key={`${run.x}-${run.width}`}
            x={run.x}
            y="0"
            width={run.width}
            height="48"
            fill="#000000"
          />
        ))}
      </svg>
      <p className="printable-barcode-caption">{value}</p>
    </div>
  );
}
