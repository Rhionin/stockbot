import { useEffect, useRef, useState, type CSSProperties } from 'react';
import { registerScannerInput } from './focusScannerInput.web';
import { createWedgeReader } from './wedge';
import { WEDGE_INPUT_LABEL, WEDGE_INPUT_TEST_ID, type WedgeInputProps } from './wedgeInput';

const HIDDEN: CSSProperties = {
  position: 'absolute',
  width: 1,
  height: 1,
  margin: -1,
  padding: 0,
  border: 0,
  overflow: 'hidden',
  clip: 'rect(0 0 0 0)',
  whiteSpace: 'nowrap',
};

export function WedgeInput({ onScan, active = true, takeFocus = active }: WedgeInputProps) {
  const [value, setValue] = useState('');
  const inputRef = useRef<HTMLInputElement>(null);
  const onScanRef = useRef(onScan);

  useEffect(() => {
    onScanRef.current = onScan;
  }, [onScan]);

  // Focusing without scrolling: on a phone the field is clipped, and a scroll
  // would open the soft keyboard and stop the queue taking pans.
  useEffect(() => {
    const input = inputRef.current;
    if (!active || input === null) return;
    return registerScannerInput(input);
  }, [active]);

  useEffect(() => {
    if (takeFocus) inputRef.current?.focus({ preventScroll: true });
  }, [takeFocus]);

  useEffect(() => {
    if (!active) return;
    const reader = createWedgeReader((barcode) => onScanRef.current(barcode), () => inputRef.current);
    document.addEventListener('keydown', reader.handleKey, true);
    return () => document.removeEventListener('keydown', reader.handleKey, true);
  }, [active]);

  return (
    <input
      ref={inputRef}
      data-testid={WEDGE_INPUT_TEST_ID}
      aria-label={WEDGE_INPUT_LABEL}
      tabIndex={-1}
      inputMode="none"
      data-1p-ignore
      data-lpignore="true"
      autoComplete="off"
      autoCapitalize="off"
      autoCorrect="off"
      spellCheck={false}
      value={value}
      style={HIDDEN}
      onChange={(event) => setValue(event.currentTarget.value)}
      onKeyDown={(event) => {
        if (event.key !== 'Enter') return;
        event.preventDefault();
        const barcode = value.trim();
        setValue('');
        if (barcode !== '') onScanRef.current(barcode);
      }}
    />
  );
}
