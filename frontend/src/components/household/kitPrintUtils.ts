import { useCallback, useState } from 'react';

export function useKitPrintRoot() {
  const [printing, setPrinting] = useState(false);

  const print = useCallback(() => {
    setPrinting(true);
    const done = () => setPrinting(false);
    window.addEventListener('afterprint', done, { once: true });
    requestAnimationFrame(() => window.print());
  }, []);

  return { printing, print };
}
