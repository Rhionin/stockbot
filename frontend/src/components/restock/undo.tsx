import { Button } from '@mantine/core';
import { notifications } from '@mantine/notifications';

export interface UndoToast {
  title?: string;
  detail: string;
  onUndo?: () => void;
  color?: string;
}

let toastSerial = 0;

const CLEARANCE_VAR = '--scan-toast-clearance';
let clearanceObserver: ResizeObserver | undefined;

const visibleToasts = (): HTMLElement[] =>
  [...document.querySelectorAll<HTMLElement>('.restock-toast')].filter((toast) => {
    const box = toast.getBoundingClientRect();
    return box.width > 0 && box.height > 0;
  });

// The queue pads by whichever toast reaches highest, so a second, taller
// toast still leaves room to scroll the last card clear.
export function applyToastClearance(): void {
  // A close timer can fire after the document is gone, such as a test teardown.
  if (typeof document === 'undefined') return;
  const toasts = visibleToasts();
  if (toasts.length === 0) {
    document.documentElement.style.removeProperty(CLEARANCE_VAR);
    clearanceObserver?.disconnect();
    clearanceObserver = undefined;
    return;
  }
  const clearance = toasts.reduce((tallest, toast) => {
    const covered = Math.max(0, Math.ceil(window.innerHeight - toast.getBoundingClientRect().top));
    return Math.max(tallest, covered);
  }, 0);
  document.documentElement.style.setProperty(CLEARANCE_VAR, `${clearance}px`);
  if (typeof ResizeObserver === 'undefined') return;
  if (clearanceObserver === undefined) {
    clearanceObserver = new ResizeObserver(() => applyToastClearance());
  }
  for (const toast of toasts) clearanceObserver.observe(toast);
};

export function showUndo(title: string, detail: string, onUndo: () => void): void;
export function showUndo(toast: UndoToast): void;
export function showUndo(
  titleOrToast: string | UndoToast,
  detail?: string,
  onUndo?: () => void,
): void {
  const toast: UndoToast = typeof titleOrToast === 'string'
    ? { title: titleOrToast, detail: detail ?? '', onUndo }
    : titleOrToast;
  const id = `restock-${Date.now()}-${toastSerial}`;
  toastSerial += 1;
  notifications.show({
    id,
    title: toast.title,
    autoClose: 5000,
    color: toast.color ?? 'teal',
    classNames: {
      root: 'restock-toast',
      body: 'restock-toast-body',
      title: 'restock-toast-title',
      description: 'restock-toast-description',
      closeButton: 'restock-toast-close',
    },
    onOpen: () => applyToastClearance(),
    onClose: () => {
      window.setTimeout(applyToastClearance, 300);
    },
    message: (
      <span className="restock-toast-message">
        <span className="restock-toast-detail">{toast.detail}</span>
        {toast.onUndo ? (
          <Button
            variant="subtle"
            size="compact-sm"
            onClick={() => {
              notifications.hide(id);
              toast.onUndo?.();
            }}
          >
            Undo
          </Button>
        ) : null}
      </span>
    ),
  });
}
