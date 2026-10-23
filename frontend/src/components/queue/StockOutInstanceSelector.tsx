import { useEffect, useState } from 'react';
import { Alert, Loader, Radio, Stack, Text } from '@mantine/core';
import { listItemInstances } from '../../api/client';
import type { ItemInstanceWithStatus } from '../../types';
import { formatExpiryDate, sortInstancesUseOldestFirst } from './queueUtils';

export interface StockOutInstanceSelectorProps {
  itemId: string;
  value: string;
  onChange: (instanceId: string) => void;
  reloadKey?: number;
}

const formatInstanceLabel = (instance: ItemInstanceWithStatus): string =>
  instance.expiresAt === null
    ? 'No expiration date'
    : `Expires ${formatExpiryDate(instance.expiresAt)}`;

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
          setError(requestError instanceof Error ? requestError.message : 'Unable to load instances.');
        }
      })
      .finally(() => {
        if (active) setLoading(false);
      });
    return () => {
      active = false;
    };
  }, [itemId, reloadKey]);

  if (loading) return <Loader size="sm" aria-label="Loading item instances" />;
  if (error !== '') return <Alert color="red">{error}</Alert>;
  if (instances.length === 0) return <Text c="dimmed">No available instances.</Text>;

  return (
    <Radio.Group
      label="Instance to remove"
      description="Instances are ordered by earliest expiration date."
      value={value}
      onChange={onChange}
    >
      <Stack gap="xs" mt="xs">
        <Radio value="" label="Use oldest available automatically" />
        {instances.map((instance) => (
          <Radio key={instance.id} value={instance.id} label={formatInstanceLabel(instance)} />
        ))}
      </Stack>
    </Radio.Group>
  );
};
