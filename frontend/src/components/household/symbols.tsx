import { useCallback, useEffect, useLayoutEffect, useRef, useState } from 'react';
import QRCode from 'qrcode';
import { PAIRING_ON_SCREEN_TOO_SMALL_HINT } from './kitCopy456';
import {
  PAIRING_CODE128_QUIET_MODULES,
  computePairingCode128Layout,
  pairingCode128BarRuns,
  pairingCode128ModuleCount,
  type PairingCode128Layout,
} from './pairingScreenCode128';

const BAR_HEIGHT_PX = 96;
const EMPTY_LAYOUT: PairingCode128Layout = {
  moduleCssPx: 2,
  moduleDevicePx: 2,
  svgWidthCssPx: 0,
  totalModules: 0,
};

export function Code128Mark({ value, quietZone = false }: { value: string; quietZone?: boolean }) {
  const measureRef = useRef<HTMLDivElement>(null);
  const [layout, setLayout] = useState(EMPTY_LAYOUT);
  const barModules = pairingCode128ModuleCount(value);
  const runs = pairingCode128BarRuns(value);
  const totalModules = layout.totalModules || barModules + 2 * PAIRING_CODE128_QUIET_MODULES;

  const measure = useCallback(() => {
    const container = measureRef.current;
    if (container == null) return;
    let width = container.clientWidth;
    if (width === 0 && container.parentElement != null) {
      width = container.parentElement.clientWidth;
    }
    const modules = pairingCode128ModuleCount(value);
    setLayout(computePairingCode128Layout(
      width,
      modules,
      window.devicePixelRatio || 1,
    ));
  }, [value]);

  useLayoutEffect(() => {
    measure();
  }, [measure, value]);

  useEffect(() => {
    const container = measureRef.current;
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
      width={layout.svgWidthCssPx}
      height={BAR_HEIGHT_PX}
      viewBox={`0 0 ${totalModules} 48`}
      preserveAspectRatio="none"
      shapeRendering="crispEdges"
      data-module-css-px={layout.moduleCssPx}
      data-module-device-px={layout.moduleDevicePx}
      data-quiet-modules={PAIRING_CODE128_QUIET_MODULES}
      data-modules={barModules}
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
      <div className="pairing-code128-frame" ref={measureRef}>
        {svg}
      </div>
      {layout.moduleDevicePx === 1 && (
        <p className="pairing-barcode-too-small-hint">{PAIRING_ON_SCREEN_TOO_SMALL_HINT}</p>
      )}
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
