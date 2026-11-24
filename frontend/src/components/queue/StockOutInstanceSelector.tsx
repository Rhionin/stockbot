import { useEffect, useState } from 'react';
import { Alert, Loader, Radio, Stack, Text } from '@mantine/core';
import { listItemInstances } from '../../api/client';
import type { ItemInstanceWithStatus } from '@stockbot/shared/types';
import { sortInstancesUseOldestFirst } from '@stockbot/shared/queueUtils';
import {
  formatInstanceLabel,
  INSTANCE_AUTOMATIC_LABEL,
  INSTANCE_PICKER_HINT,
  INSTANCE_PICKER_LABEL,
  INSTANCES_LOAD_FAILURE,
  INSTANCES_LOADING,
  INSTANCES_NONE,
} from '@stockbot/shared/cardDetails';
import { inlineFailure } from '@stockbot/shared/contract';

export interface StockOutInstanceSelectorProps {
  itemId: string;
  value: string;
  onChange: (instanceId: string) => void;
  reloadKey?: number;
}

export const StockOutInstanceSelector = ({
  itemId,
  value,
  onChange,
  reloadKey = 0,
}: StockOutInstanceSelectorProps) => {
  const [instances, setInstances] = useState<ItemInstanceWithStatus[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');

  useEffect(() => {
    let active = true;
    // A later reload replaces a unit the household already took out.
    // eslint-disable-next-line react-hooks/set-state-in-effect
    setLoading(true);
    setError('');
    void listItemInstances(itemId)
      .then((result) => {
        if (active) setInstances(sortInstancesUseOldestFirst(result));
      })
      .catch((requestError: unknown) => {
        if (active) {
          setError(inlineFailure(requestError, INSTANCES_LOAD_FAILURE) ?? '');
        }
      })
      .finally(() => {
        if (active) setLoading(false);
      });
    return () => {
      active = false;
    };
  }, [itemId, reloadKey]);

  if (loading) return <Loader size="sm" aria-label={INSTANCES_LOADING} />;
  if (error !== '') return <Alert color="red">{error}</Alert>;
  if (instances.length === 0) return <Text c="dimmed">{INSTANCES_NONE}</Text>;

  return (
    <Radio.Group
      label={INSTANCE_PICKER_LABEL}
      description={INSTANCE_PICKER_HINT}
      value={value}
      onChange={onChange}
    >
      <Stack gap="xs" mt="xs">
        <Radio value="" label={INSTANCE_AUTOMATIC_LABEL} />
        {instances.map((instance) => (
          <Radio key={instance.id} value={instance.id} label={formatInstanceLabel(instance)} />
        ))}
      </Stack>
    </Radio.Group>
  );
};
