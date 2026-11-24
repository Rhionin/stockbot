export const UNDO_ACTION_LABEL = 'Undo';

export const UNDO_TOAST_MS = 5000;

export interface UndoToast {
  title?: string;
  detail: string;
  onUndo?: () => void;
  color?: string;
  id?: string;
  autoClose?: number | false;
}

export const undoToastAutoClose = (toast: UndoToast): number | false =>
  toast.autoClose === undefined ? UNDO_TOAST_MS : toast.autoClose;

export const undoToastId = (toast: UndoToast, serial: number, now: number): string =>
  toast.id ?? `restock-${now}-${serial}`;

// An Undo that outlived its toast must not reach the server.
export const undoStillOffered = (offeredId: string | null, pressedId: string): boolean =>
  offeredId !== null && offeredId === pressedId;
