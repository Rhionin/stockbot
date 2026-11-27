import { Anchor, Group, Stack, Text } from '@mantine/core';
import { Link } from 'react-router-dom';
import { groupGoalCopy } from './groupGoalCopy';
import { groupedD2Line, type GroupedGoalContext } from './groupedProductGoal';

export function GroupedProductSettings({ ctx }: { ctx: GroupedGoalContext }) {
  return (
    <Stack gap={4}>
      <Text size="sm">{groupGoalCopy.d1(ctx.groupName)}</Text>
      <Text size="sm" c="dimmed">{groupedD2Line(ctx)}</Text>
      <Group gap="xs">
        <Anchor component={Link} to={`/groups/${ctx.groupId}#keep`} size="sm">
          {groupGoalCopy.openGroupSettings}
        </Anchor>
      </Group>
    </Stack>
  );
}