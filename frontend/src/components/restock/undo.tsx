import { Button } from '@mantine/core';
import { notifications } from '@mantine/notifications';

export interface UndoToast {
  title?: string;
  detail: string;
  onUndo?: () => void;
  color?: string;
}

let toastSerial = 0;

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
    classNames: { root: 'restock-toast' },
    message: (
      <span>
        <span>{toast.detail}</span>
        {toast.onUndo ? (
          <>
            {' '}
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
          </>
        ) : null}
      </span>
    ),
  });
}
