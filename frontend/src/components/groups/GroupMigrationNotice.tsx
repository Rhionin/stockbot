import { useState } from 'react';
import { Button, Stack, Text } from '@mantine/core';
import type { ProductGroup } from '@stockbot/shared/types';
import { dismissGroupMigrationNotice } from '../../api/client';
import { migrationNoticeBody, groupGoalCopy } from './groupGoalCopy';
import { inlineFailure } from '@stockbot/shared/contract';

export function GroupMigrationNoticeCard({
  group,
  onUpdated,
}: {
  group: ProductGroup;
  onUpdated: (next: ProductGroup) => void;
}) {
  const notice = group.migrationNotice;
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);
  if (!notice) return null;

  const keptDontRestock = group.target?.kind === 'dont_restock';
  const itemsValue = notice.case === 3 ? group.target?.kind === 'items' ? group.target.value : undefined : undefined;
  const monthsValue = notice.case === 4
    ? group.target?.kind === 'months'
      ? group.target.value
      : group.windowMonths ?? undefined
    : undefined;
  const body = migrationNoticeBody(notice, keptDontRestock, itemsValue, monthsValue);
  if (body === '') return null;

  const dismiss = async () => {
    setBusy(true);
    setError('');
    try {
      const next = await dismissGroupMigrationNotice(group.id);
      onUpdated(next);
    } catch (requestError) {
      setError(inlineFailure(requestError, groupGoalCopy.migrationDismissError) ?? '');
    } finally {
      setBusy(false);
    }
  };

  return (
    <Stack gap={6} className="group-migration-note">
      <Text size="sm">{body}</Text>
      {notice.case === 3 && notice.products.length > 0 && (
        <Text size="xs" c="dimmed">
          {notice.products.slice(0, 3).map((row) => row.name).join(', ')}
          {notice.products.length > 3 ? ` and ${notice.products.length - 3} more` : ''}
        </Text>
      )}
      <Button
        size="xs"
        variant="light"
        loading={busy}
        onClick={() => void dismiss()}
        aria-label={groupGoalCopy.f4a11y}
      >
        {groupGoalCopy.f4}
      </Button>
      {error !== '' && <Text size="sm" c="red">{error}</Text>}
    </Stack>
  );
}