import { useCallback, useEffect, useRef, useState } from 'react';

export const CAPTURE_HIGHLIGHT_MS = 4000;

// The card for a code that was just scanned stands out for a few seconds,
// matched by barcode because the card may not exist yet when the scan is made.
export function useCaptureHighlight() {
  const [barcode, setBarcode] = useState('');
  const timer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);

  useEffect(() => () => clearTimeout(timer.current), []);

  const clear = useCallback(() => {
    clearTimeout(timer.current);
    setBarcode('');
  }, []);

  const mark = useCallback((next: string) => {
    clearTimeout(timer.current);
    setBarcode(next);
    timer.current = setTimeout(() => setBarcode(''), CAPTURE_HIGHLIGHT_MS);
  }, []);

  return { barcode, mark, clear };
}
