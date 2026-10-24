// Pre-selects the scan direction applied to new scan entries. Tracks the
// timestamp of the last recorded scan so the pre-selected direction can
// auto-clear after a period of scanner inactivity (Requirement 1.5).
import { useCallback, useEffect, useImperativeHandle, useRef, useState, type Ref } from 'react';
import { SegmentedControl } from '@mantine/core';
import type { ScanDirection } from '@stockbot/shared/types';

// A pre-selected scan direction stays in effect until 5 minutes have elapsed
// since the last recorded scan (Requirement 1.5).
const AUTO_CLEAR_IDLE_MS = 5 * 60 * 1000;

const UNSET_VALUE = '';

export interface ScanDirectionToggleHandle {
  // Called by the parent scanning page each time a new scan is recorded.
  // Resets the 5-minute idle timer so the pre-selected direction persists.
  recordScan: () => void;
}

export interface ScanDirectionToggleProps {
  onDirectionChange?: (direction: ScanDirection | null) => void;
  ref?: Ref<ScanDirectionToggleHandle>;
}

export function ScanDirectionToggle({ onDirectionChange, ref }: ScanDirectionToggleProps) {
  const [direction, setDirection] = useState<ScanDirection | null>(null);
  const timerRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const onDirectionChangeRef = useRef(onDirectionChange);

  useEffect(() => {
    onDirectionChangeRef.current = onDirectionChange;
  }, [onDirectionChange]);

  const clearIdleTimer = useCallback(() => {
    if (timerRef.current !== null) {
      clearTimeout(timerRef.current);
      timerRef.current = null;
    }
  }, []);

  const scheduleAutoClear = useCallback(() => {
    clearIdleTimer();
    timerRef.current = setTimeout(() => {
      setDirection(null);
      onDirectionChangeRef.current?.(null);
    }, AUTO_CLEAR_IDLE_MS);
  }, [clearIdleTimer]);

  useEffect(() => clearIdleTimer, [clearIdleTimer]);

  useImperativeHandle(
    ref,
    () => ({
      recordScan() {
        if (direction !== null) {
          scheduleAutoClear();
        }
      },
    }),
    [direction, scheduleAutoClear],
  );

  function handleChange(value: string) {
    const next = value === UNSET_VALUE ? null : (value as ScanDirection);
    setDirection(next);
    onDirectionChange?.(next);
    if (next !== null) {
      scheduleAutoClear();
    } else {
      clearIdleTimer();
    }
  }

  return (
    <SegmentedControl
      aria-label="Scan direction"
      value={direction ?? UNSET_VALUE}
      onChange={handleChange}
      data={[
        { label: 'Stock In', value: 'stock_in' },
        { label: 'Unset', value: UNSET_VALUE },
        { label: 'Stock Out', value: 'stock_out' },
      ]}
    />
  );
}
