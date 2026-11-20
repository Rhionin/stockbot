import { useEffect, useRef } from 'react';
import { Button, Group, Text } from '@mantine/core';
import {
  SHOPPING_LIST_UNDO_VISIBLE_LABEL,
  shoppingListAfterRemoveGroupMessage,
  shoppingListAfterRemoveProductMessage,
} from '@stockbot/shared/shoppingListCopy';

export type ShoppingListRemovedStatusProps = {
  productName: string;
  groupName?: string;
  focusUndoOnMount?: boolean;
  onUndoFocus?: () => void;
  onUndo: () => void;
  undoPending?: boolean;
  undoError?: string;
};

export function ShoppingListRemovedStatus({
  productName,
  groupName,
  focusUndoOnMount = false,
  onUndoFocus,
  onUndo,
  undoPending = false,
  undoError = '',
}: ShoppingListRemovedStatusProps) {
  const undoRef = useRef<HTMLButtonElement>(null);
  const message = groupName
    ? shoppingListAfterRemoveGroupMessage(groupName)
    : shoppingListAfterRemoveProductMessage(productName);

  useEffect(() => {
    if (!focusUndoOnMount || undoRef.current === null) return;
    undoRef.current.focus();
    onUndoFocus?.();
  }, [focusUndoOnMount, message, onUndoFocus]);

  return (
    <Group gap="xs" align="stretch" wrap="nowrap" className="shopping-removed-status-block">
      <Group gap="sm" wrap="wrap" align="center" className="shopping-removed-status-row">
        <Text role="status" size="sm" className="shopping-removed-status">
          {message}
        </Text>
        <Button
          ref={undoRef}
          type="button"
          size="xs"
          variant="subtle"
          className="shopping-removed-undo"
          aria-label={SHOPPING_LIST_UNDO_VISIBLE_LABEL}
          loading={undoPending}
          disabled={undoPending}
          onClick={() => onUndo()}
        >
          {SHOPPING_LIST_UNDO_VISIBLE_LABEL}
        </Button>
      </Group>
      {undoError !== '' && (
        <Text role="alert" size="sm" c="red">
          {undoError}
        </Text>
      )}
    </Group>
  );
}
