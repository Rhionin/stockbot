import { useEffect, useRef } from 'react';
import { Text } from '@mantine/core';
import {
  shoppingListAfterRemoveGroupMessage,
  shoppingListAfterRemoveProductMessage,
} from '@stockbot/shared/shoppingListCopy';

export type ShoppingListRemovedStatusProps = {
  productName: string;
  groupName?: string;
  focusOnMount?: boolean;
  onFocused?: () => void;
};

export function ShoppingListRemovedStatus({
  productName,
  groupName,
  focusOnMount = false,
  onFocused,
}: ShoppingListRemovedStatusProps) {
  const statusRef = useRef<HTMLParagraphElement>(null);
  const message = groupName
    ? shoppingListAfterRemoveGroupMessage(groupName)
    : shoppingListAfterRemoveProductMessage(productName);

  useEffect(() => {
    if (!focusOnMount || statusRef.current === null) return;
    statusRef.current.focus();
    onFocused?.();
  }, [focusOnMount, message, onFocused]);

  return (
    <Text
      ref={statusRef}
      tabIndex={-1}
      role="status"
      size="sm"
      className="shopping-removed-status"
    >
      {message}
    </Text>
  );
}
