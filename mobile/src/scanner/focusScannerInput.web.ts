let current: HTMLInputElement | null = null;

// Tab screens stay mounted behind the one in front, so a page search could
// find the wrong field. The field of the screen in front says so itself.
export function registerScannerInput(input: HTMLInputElement): () => void {
  current = input;
  return () => {
    if (current === input) current = null;
  };
}

export function focusScannerInput(): boolean {
  if (current === null) return false;
  current.focus({ preventScroll: true });
  return true;
}
