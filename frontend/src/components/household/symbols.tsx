import { useEffect, useState } from 'react';
import QRCode from 'qrcode';
import { code128Bars } from './code128';

export function Code128Mark({ value }: { value: string }) {
  const bars = code128Bars(value);
  const width = Math.max(bars.length, 1);
  return (
    <svg
      className="pairing-code128"
      role="img"
      aria-label={`Code 128 ${value}`}
      viewBox={`0 0 ${width} 48`}
      preserveAspectRatio="none"
    >
      {bars.map((on, index) => (on
        ? <rect key={index} x={index} y="0" width="1" height="48" fill="currentColor" />
        : null))}
    </svg>
  );
}

export function QrMark({ value }: { value: string }) {
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
      className="pairing-qr"
      role="img"
      aria-label={`QR code ${value}`}
      dangerouslySetInnerHTML={{ __html: markup }}
    />
  );
}
