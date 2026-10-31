import { Stack } from '@mantine/core';
import type { OperatorHouseholdView } from '@stockbot/shared/contract';
import {
  formatOperatorHouseholdStatLines,
  splitOperatorStatLine,
} from './operatorHouseholdCopy';

function StatLine({ text }: { text: string }) {
  const parts = splitOperatorStatLine(text);
  return (
    <div className="admin-household-stat-line">
      {parts.map((part, index) => (
        <span key={`${index}-${part}`}>
          <span className="admin-household-stat-part">
            {part}
            {index < parts.length - 1 && '\u00a0·'}
          </span>
          {index < parts.length - 1 && ' '}
        </span>
      ))}
    </div>
  );
}

export function OperatorHouseholdStatBlock({ row, now }: { row: OperatorHouseholdView; now?: Date }) {
  const lines = formatOperatorHouseholdStatLines(row, now);
  return (
    <Stack gap={2} className="admin-household-stats" data-testid="operator-household-stats">
      <StatLine text={lines.inventory} />
      <StatLine text={lines.scanners} />
      <StatLine text={lines.scans} />
      <StatLine text={lines.lastActivity} />
    </Stack>
  );
}
