import { Badge, Text } from '@mantine/core';
import type { ExternalSource } from '@stockbot/shared/types';
import { DATABASE_NAMES } from './databaseNames';

export interface ProvenanceBadgeProps {
  externalSource?: ExternalSource;
  // A filled badge reads as a button. Inventory only needs the source named.
  quiet?: boolean;
}

export const ProvenanceBadge = ({ externalSource, quiet = false }: ProvenanceBadgeProps) => {
  if (!externalSource || !(externalSource in DATABASE_NAMES)) {
    return null;
  }

  const name = DATABASE_NAMES[externalSource];
  const label = `Product data from ${name}`;

  if (quiet) {
    return (
      <Text size="xs" c="dimmed" aria-label={label}>
        {name}
      </Text>
    );
  }

  return (
    <Badge
      size="xs"
      variant="light"
      aria-label={label}
    >
      {name}
    </Badge>
  );
};
