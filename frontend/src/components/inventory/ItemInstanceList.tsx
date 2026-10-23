import { useCallback, useEffect, useId, useMemo, useState } from 'react';
import { Alert, Badge, Button, Group, Loader, Paper, SimpleGrid, Stack, Text, TextInput, Title } from '@mantine/core';
import { addItemInstance, listItemInstances, stockOutItem } from '../../api/client';
import type { ExpiryStatus, ItemInstanceWithStatus } from '../../types';
import { computeExpiryStatus } from '../../utils/expiry';
import { expiryDateToISOString } from '../queue/queueUtils';
import { inlineFailure } from '../../api/contract';

export interface ItemInstanceListProps {
  itemId: string;
  productName: string;
  onHand: number;
  onHandChange: (delta: number) => void;
  onInventoryChanged: () => void;
}

const sortUseOldestFirst = (
  instances: ItemInstanceWithStatus[],
): ItemInstanceWithStatus[] =>
  [...instances].sort((left, right) => {
    if (left.expiresAt === null) return right.expiresAt === null ? 0 : 1;
    if (right.expiresAt === null) return -1;
    return new Date(left.expiresAt).getTime() - new Date(right.expiresAt).getTime();
  });

const formatDate = (value: string): string =>
  new Intl.DateTimeFormat(undefined, { dateStyle: 'medium', timeZone: 'UTC' }).format(new Date(value));

const expiryBadge = (status: ExpiryStatus) => {
  if (status === 'expired') return <Badge color="red">Expired</Badge>;
  if (status === 'near_expiry') return <Badge color="yellow">Near expiry</Badge>;
  return null;
};

export const ItemInstanceList = ({
  itemId,
  productName,
  onHand,
  onHandChange,
  onInventoryChanged,
}: ItemInstanceListProps) => {
  const nothingOnHandId = useId();
  const [instances, setInstances] = useState<ItemInstanceWithStatus[]>([]);
  const [evaluatedAt, setEvaluatedAt] = useState(() => new Date());
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');
  const [pending, setPending] = useState<'in' | 'out' | null>(null);
  const [expiryOpen, setExpiryOpen] = useState(false);
  const [expiryDate, setExpiryDate] = useState('');

  const loadInstances = useCallback(async () => {
    setLoading(true);
    setError('');
    try {
      const result = await listItemInstances(itemId);
      setInstances(sortUseOldestFirst(result));
      setEvaluatedAt(new Date());
    } catch (requestError) {
      setError(inlineFailure(requestError, 'Unable to load instances.') ?? '');
    } finally {
      setLoading(false);
    }
  }, [itemId]);

  useEffect(() => {
    void Promise.resolve().then(loadInstances);
  }, [loadInstances]);

  const instanceStatuses = useMemo(
    () => new Map(instances.map((instance) => [
      instance.id,
      computeExpiryStatus(instance.expiresAt, evaluatedAt),
    ])),
    [evaluatedAt, instances],
  );

  const stockIn = async () => {
    setPending('in');
    setError('');
    try {
      await addItemInstance(itemId, expiryDateToISOString(expiryDate));
      onHandChange(1);
      onInventoryChanged();
      await loadInstances();
    } catch (requestError) {
      setError(inlineFailure(requestError, 'Unable to stock in.') ?? '');
    } finally {
      setPending(null);
    }
  };

  const stockOut = async () => {
    setPending('out');
    setError('');
    try {
      await stockOutItem(itemId);
      onHandChange(-1);
      onInventoryChanged();
      await loadInstances();
    } catch (requestError) {
      setError(inlineFailure(requestError, 'Unable to stock out.') ?? '');
    } finally {
      setPending(null);
    }
  };

  const nothingOnHand = onHand === 0;

  return (
    <Stack id={`inventory-item-${itemId}`} component="section" aria-labelledby={`instances-${itemId}`} gap="xs">
      <SimpleGrid cols={2} spacing="xs">
        <Button size="xl" fullWidth loading={pending === 'in'} disabled={pending !== null} onClick={() => void stockIn()}>
          Stock in
        </Button>
        <Button
          size="xl"
          fullWidth
          variant="default"
          loading={pending === 'out'}
          disabled={nothingOnHand || pending !== null}
          aria-describedby={nothingOnHand ? nothingOnHandId : undefined}
          onClick={() => void stockOut()}
        >
          Stock out
        </Button>
      </SimpleGrid>
      {expiryOpen ? (
        <TextInput
          size="xs"
          type="date"
          label="Expiration date"
          description="Optional"
          value={expiryDate}
          onChange={(event) => setExpiryDate(event.currentTarget.value)}
        />
      ) : (
        <Button variant="subtle" color="gray" size="compact-sm" px={4} onClick={() => setExpiryOpen(true)}>
          Add expiration
        </Button>
      )}
      {nothingOnHand && (
        <Text id={nothingOnHandId} size="sm" c="dimmed">There is nothing on hand.</Text>
      )}
      <Title id={`instances-${itemId}`} order={4} size="sm">{productName} instances</Title>
      {loading && <Loader aria-label="Loading item instances" />}
      {error !== '' && (
        <Alert color="red" py="xs">
          {error}
        </Alert>
      )}
      {!loading && error === '' && instances.length === 0 && (
        <Text c="dimmed">No item instances.</Text>
      )}
      {!loading && error === '' && instances.length > 0 && (
        <Stack component="ol" gap="xs">
          {instances.map((instance) => (
            <Paper component="li" key={instance.id} withBorder p="xs">
              <Group justify="flex-start" align="center" wrap="wrap">
                <Group gap="xs">
                  <Text size="sm">Stocked in {formatDate(instance.stockInAt)}</Text>
                  <Text size="sm" c="dimmed">
                    {instance.expiresAt === null
                      ? 'No expiration date'
                      : `Expires ${formatDate(instance.expiresAt)}`}
                  </Text>
                </Group>
                {expiryBadge(instanceStatuses.get(instance.id) ?? 'ok')}
              </Group>
            </Paper>
          ))}
        </Stack>
      )}
    </Stack>
  );
};
