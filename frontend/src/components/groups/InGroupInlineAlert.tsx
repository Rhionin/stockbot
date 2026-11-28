import { Anchor, Text } from '@mantine/core';
import { Link } from 'react-router-dom';
import type { InGroupErrorMembers } from '@stockbot/shared/types';
import { groupGoalCopy } from './groupGoalCopy';

export function InGroupInlineAlert({ members }: { members: InGroupErrorMembers }) {
  return (
    <Text size="sm" c="red">
      {groupGoalCopy.inGroup}{' '}
      <Anchor component={Link} to={`/groups/${members.groupId}#keep`} size="sm">
        {groupGoalCopy.openGroupSettings}
      </Anchor>
    </Text>
  );
}