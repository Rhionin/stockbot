// A USB or Bluetooth scanner types the barcode as keystrokes and ends with
// Enter. Nothing but speed tells that apart from a person at the keyboard.
export const WEDGE_MAX_GAP_MS = 100;
export const WEDGE_MIN_LENGTH = 4;
export const WEDGE_MAX_LENGTH = 64;

export interface WedgeKeyEvent {
  key: string;
  ctrlKey?: boolean;
  metaKey?: boolean;
  altKey?: boolean;
  target: unknown;
  preventDefault: () => void;
  stopPropagation: () => void;
}

interface FieldLike {
  tagName?: string;
  isContentEditable?: boolean;
}

const TEXT_ENTRY_TAGS = new Set(['INPUT', 'TEXTAREA', 'SELECT']);

export function isTextEntry(target: unknown): boolean {
  const field = target as FieldLike | null;
  if (field === null || typeof field !== 'object') return false;
  if (field.isContentEditable === true) return true;
  return TEXT_ENTRY_TAGS.has((field.tagName ?? '').toUpperCase());
}

export interface WedgeReader {
  handleKey: (event: WedgeKeyEvent) => void;
}

// Reads a scan from keystrokes that land anywhere on the page. Typing in any
// other field is left alone, and so are keys that arrive at a person's pace.
// `ownField` is the dedicated scanner input, which takes whatever it is given.
export function createWedgeReader(
  onScan: (barcode: string) => void,
  ownField: () => unknown,
  now: () => number = Date.now,
): WedgeReader {
  let buffer = '';
  let lastAt = 0;
  let overflowed = false;

  function handleKey(event: WedgeKeyEvent) {
    if (
      event.target === ownField()
      || isTextEntry(event.target)
      || event.ctrlKey === true
      || event.metaKey === true
      || event.altKey === true
    ) {
      buffer = '';
      overflowed = false;
      return;
    }
    const at = now();
    const fast = (buffer === '' && !overflowed) || at - lastAt <= WEDGE_MAX_GAP_MS;
    if (event.key === 'Enter') {
      const code = buffer;
      const runaway = overflowed;
      buffer = '';
      overflowed = false;
      if (!fast || runaway || code.length < WEDGE_MIN_LENGTH) return;
      event.preventDefault();
      event.stopPropagation();
      onScan(code);
      return;
    }
    if (event.key.length !== 1) {
      // Shift and friends ride along inside a scan. Anything else is a person.
      if (!['Shift', 'CapsLock', 'AltGraph'].includes(event.key)) {
        buffer = '';
        overflowed = false;
      }
      return;
    }
    if (!fast) overflowed = false;
    buffer = fast ? buffer + event.key : event.key;
    lastAt = at;
    if (overflowed) buffer = '';
    else if (buffer.length > WEDGE_MAX_LENGTH) {
      buffer = '';
      overflowed = true;
    }
    // Keys of a scan in progress must not press a focused button, as Space would.
    if (buffer.length >= 2) event.preventDefault();
  }

  return { handleKey };
}
