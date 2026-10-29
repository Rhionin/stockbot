import { useCallback, useEffect, useLayoutEffect, useRef, useState } from 'react';
import QRCode from 'qrcode';
import {
  PAIRING_CODE128_MIN_MODULE_PX,
  PAIRING_CODE128_QUIET_MODULES,
  computePairingCode128ModulePx,
  pairingCode128BarRuns,
  pairingCode128ModuleCount,
  pairingCode128SvgWidthPx,
} from './pairingScreenCode128';

const BAR_HEIGHT_PX = 96;

export function Code128Mark({ value, quietZone = false }: { value: string; quietZone?: boolean }) {
  const scrollRef = useRef<HTMLDivElement>(null);
  const [modulePx, setModulePx] = useState(PAIRING_CODE128_MIN_MODULE_PX);
  const modules = pairingCode128ModuleCount(value);
  const totalModules = modules + 2 * PAIRING_CODE128_QUIET_MODULES;
  const runs = pairingCode128BarRuns(value);
  const svgWidthPx = pairingCode128SvgWidthPx(modulePx, modules);

  const measure = useCallback(() => {
    const container = scrollRef.current;
    if (container == null) return;
    const count = pairingCode128ModuleCount(value);
    setModulePx(computePairingCode128ModulePx(container.clientWidth, count));
  }, [value]);

  useLayoutEffect(() => {
    measure();
  }, [measure, value]);

  useEffect(() => {
    const container = scrollRef.current;
    if (container == null || typeof ResizeObserver === 'undefined') return undefined;
    const observer = new ResizeObserver(() => measure());
    observer.observe(container);
    return () => observer.disconnect();
  }, [measure, value]);

  const svg = (
    <svg
      className="pairing-code128"
      role="img"
      aria-label={`Code 128 ${value}`}
      width={svgWidthPx}
      height={BAR_HEIGHT_PX}
      viewBox={`0 0 ${totalModules} 48`}
      preserveAspectRatio="none"
      shapeRendering="crispEdges"
      data-module-px={modulePx}
      data-quiet-modules={PAIRING_CODE128_QUIET_MODULES}
      data-modules={modules}
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
  );

  if (!quietZone) return svg;

  return (
    <div className="code128-quiet-zone">
      <div className="pairing-code128-scroll" ref={scrollRef}>
        {svg}
      </div>
    </div>
  );
}

export function QrMark({ value, className }: { value: string; className?: string }) {
  const [markup, setMarkup] = useState('');
  useEffect(() => {
    let cancelled = false;
    void QRCode.toString(value, { type: 'svg', margin: 1, errorCorrectionLevel: 'M', color: { dark: '#134e4a', light: '#ffffff' } })
      .then((svg) => {
        if (!cancelled) setMarkup(svg);
      })
      .catch(() => {
        if (!cancelled) setMarkup('');
      });
    return () => {
      cancelled = true;
    };
  }, [value]);
  return (
    <div
      className={className ? `pairing-qr ${className}` : 'pairing-qr'}
      role="img"
      aria-label={`QR code ${value}`}
      dangerouslySetInnerHTML={{ __html: markup }}
    />
  );
}
