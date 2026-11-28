import { Button, Stack, Text } from '@mantine/core';
import type { GroupGoalBlocker, ProductGroup } from '@stockbot/shared/types';
import {
  b4A11y,
  b4Label,
  b5A11y,
  b5Label,
  b1,
  b2,
  b3,
  c1,
  c2a,
  c2b,
  c3a,
  c3b,
  groupGoalCopy,
  statusGoalDisplay,
} from './groupGoalCopy';
import type { ResolvedGroupGoal } from './groupGoalModel';

export function GroupGoalStatusCard({
  group,
  resolved,
  onCountItemsInstead,
  onSwitchAmountUnit,
  onFixSize,
}: {
  group: ProductGroup;
  resolved: ResolvedGroupGoal;
  onCountItemsInstead: () => void;
  onSwitchAmountUnit?: () => void;
  onFixSize?: (productId: string) => void;
}) {
  const target = resolved.target;
  if (!target || target.kind !== 'amount' || target.status === 'ok') return null;
  const blockers = target.blockers ?? [];
  if (blockers.length === 0) return null;

  const goalLabel = statusGoalDisplay(group);
  const goalIsVolume = target.unit === 'fl oz';
  const missing = blockers.filter((row) => row.reason === 'size_missing');
  const mismatch = blockers.filter((row) => row.reason === 'units_mismatch');

  const body = (() => {
    if (missing.length === 1 && mismatch.length === 0) return c3a(missing[0]!.name);
    if (missing.length > 1 && mismatch.length === 0) return c3b(missing.length);
    if (mismatch.length === 1 && missing.length === 0) return c2a(mismatch[0]!.name, goalIsVolume);
    if (mismatch.length > 1 && missing.length === 0) return c2b(mismatch.length, goalIsVolume);
    return [
      ...missing.map((row) => c3a(row.name)),
      ...mismatch.map((row) => c2a(row.name, goalIsVolume)),
    ].join(' ');
  })();

  const showUnitSwitch = resolved.amountAvailable
    && resolved.amountUnit
    && target.unit
    && resolved.amountUnit !== target.unit
    && onSwitchAmountUnit;

  return (
    <section className="group-goal-status" aria-labelledby={`goal-status-${group.id}`}>
      <Text id={`goal-status-${group.id}`} fw={600} className="group-goal-status-heading">
        {c1(goalLabel)}
      </Text>
      <Text size="sm" className="group-goal-status-body">{body}</Text>
      <Stack gap="xs" mt="xs">
        {[...missing, ...mismatch].map((row) => (
          <BlockerRow key={row.productId} row={row} onFix={onFixSize} />
        ))}
      </Stack>
      <Stack gap="xs" mt="sm">
        {showUnitSwitch && (
          <Button
            type="button"
            variant="light"
            className="group-goal-fix"
            onClick={onSwitchAmountUnit}
          >
            {resolved.amountUnit === 'fl oz' ? groupGoalCopy.b7FlOz : groupGoalCopy.b7Oz}
          </Button>
        )}
        <Button type="button" variant="light" className="group-goal-fix" onClick={onCountItemsInstead}>
          {groupGoalCopy.b6}
        </Button>
      </Stack>
    </section>
  );
}

function BlockerRow({
  row,
  onFix,
}: {
  row: GroupGoalBlocker;
  onFix?: (productId: string) => void;
}) {
  const line = row.reason === 'size_missing' ? b1(row.name) : row.sizeKind === 'volume' ? b2(row.name) : b3(row.name);
  const label = row.reason === 'size_missing' ? b4Label : b5Label;
  const a11y = row.reason === 'size_missing' ? b4A11y(row.name) : b5A11y(row.name);
  return (
    <div className="group-goal-blocker-row">
      <Text size="sm">{line}</Text>
      {onFix && (
        <Button
          type="button"
          size="compact-sm"
          className="group-goal-fix"
          aria-label={a11y}
          onClick={() => onFix(row.productId)}
        >
          {label}
        </Button>
      )}
    </div>
  );
}