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

const toastContainer = (): HTMLElement | null => {
  const toast = document.querySelector('.restock-toast');
  if (toast === null) return null;
  return toast.closest<HTMLElement>('[data-position]') ?? (toast as HTMLElement);
};

// The queue pads by the fixed toast so the last card can scroll above it.
const applyToastClearance = () => {
  const container = toastContainer();
  if (container === null) {
    document.documentElement.style.removeProperty(CLEARANCE_VAR);
    clearanceObserver?.disconnect();
    clearanceObserver = undefined;
    return;
  }
  const top = container.getBoundingClientRect().top;
  const clearance = Math.max(0, Math.ceil(window.innerHeight - top));
  document.documentElement.style.setProperty(CLEARANCE_VAR, `${clearance}px`);
  if (clearanceObserver === undefined && typeof ResizeObserver !== 'undefined') {
    clearanceObserver = new ResizeObserver(() => applyToastClearance());
    clearanceObserver.observe(container);
  }
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
