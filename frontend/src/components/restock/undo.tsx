import { Button } from '@mantine/core';
import { notifications } from '@mantine/notifications';

export function showUndo(title: string, detail: string, onUndo: () => void) {
  const id = `restock-${Date.now()}`;
  notifications.show({
    id,
    title,
    autoClose: 5000,
    color: 'teal',
    classNames: { root: 'restock-toast' },
    message: (
      <span>
        {detail}{' '}
        <Button
          variant="subtle"
          size="compact-sm"
          onClick={() => {
            notifications.hide(id);
            onUndo();
          }}
        >
          Undo
        </Button>
      </span>
    ),
  });
}
