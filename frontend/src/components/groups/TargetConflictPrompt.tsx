import { Button, Group, Stack, Text } from '@mantine/core';
import type { GroupTarget, TargetConflictMember } from '@stockbot/shared/types';
import { conflictLine, targetChoices, type GroupSupply } from './copy';

export interface TargetConflictPromptProps {
  group: GroupSupply | null;
  members: TargetConflictMember[];
  onChoose: (target: GroupTarget) => void;
  onCancel: () => void;
}

export const TargetConflictPrompt = ({ group, members, onChoose, onCancel }: TargetConflictPromptProps) => {
  const choices = targetChoices(group, members);
  return (
    <Stack gap={4}>
      <Text size="sm" fw={600}>
        These products have their own supply setting. Which should the whole group use?
      </Text>
      {members.map((member) => (
        <Text key={member.productId} size="sm">{conflictLine(member)}</Text>
      ))}
      <Group gap="xs" wrap="wrap">
        {choices.map((choice) => (
          <Button key={choice.label} size="xs" variant="light" onClick={() => onChoose(choice.target)}>
            {choice.label}
          </Button>
        ))}
        <Button size="xs" variant="default" onClick={onCancel}>Cancel</Button>
      </Group>
    </Stack>
  );
};
