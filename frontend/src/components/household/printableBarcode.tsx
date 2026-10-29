import { code128Bars } from './code128';

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
    runs.push({ x: start, width: index - start });
  }
  return runs;
}

export function PrintableBarcode({
  value,
  moduleMm,
  heightMm,
}: {
  value: string;
  moduleMm: number;
  heightMm: number;
}) {
  const bars = code128Bars(value);
  const modules = bars.length;
  const widthMm = modules * moduleMm;
  const quietMm = Math.max(6, 10 * moduleMm);
  const runs = mergedBarRuns(bars);

  return (
    <div className="printable-barcode-block" style={{ padding: `${quietMm}mm` }}>
      <svg
        data-barcode
        data-value={value}
        data-modules={modules}
        className="printable-barcode-svg"
        width={`${widthMm}mm`}
        height={`${heightMm}mm`}
        viewBox={`0 0 ${modules} 48`}
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
