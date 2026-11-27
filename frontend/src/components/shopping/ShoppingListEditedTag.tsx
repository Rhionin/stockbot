import { Badge, Tooltip } from '@mantine/core';
import type { AuthSession } from '@stockbot/shared/contract';
import type { ShoppingLineEdited } from '@stockbot/shared/types';
import { groupGoalCopy } from '../groups/groupGoalCopy';
import { editedTripDescription } from './shoppingListEditedCopy';

export function ShoppingListEditedTag({
  edited,
  session,
}: {
  edited: ShoppingLineEdited;
  session: AuthSession | null | undefined;
}) {
  const description = editedTripDescription(edited, session);
  return (
    <Tooltip label={description} withArrow multiline maw={280}>
      <Badge variant="outline" size="sm" aria-describedby={undefined}>
        {groupGoalCopy.g1}
      </Badge>
    </Tooltip>
  );
}