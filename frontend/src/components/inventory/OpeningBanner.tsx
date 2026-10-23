import { useState } from 'react';
import { Alert, Button, Group, Text } from '@mantine/core';
import { completeOnboarding } from '../../api/client';
import { inlineFailure } from '../../api/contract';

export interface OpeningBannerProps {
  onComplete: () => void;
}

export const OpeningBanner = ({ onComplete }: OpeningBannerProps) => {
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState('');

  const complete = async () => {
    setSaving(true);
    setError('');
    try {
      await completeOnboarding();
      onComplete();
    } catch (requestError) {
      setError(inlineFailure(requestError, 'Unable to finish the opening scan.') ?? '');
    } finally {
      setSaving(false);
    }
  };

  return (
    <Alert color="blue" title="Opening inventory">
      <Text size="sm">
        This first scan is a snapshot of what is already here. It is not use, and it does not start a shopping rate.
      </Text>
      {error !== '' && <Text size="sm" c="red" mt="xs">{error}</Text>}
      <Group mt="sm">
        <Button size="xs" loading={saving} onClick={() => void complete()}>
          This scan is complete
        </Button>
      </Group>
    </Alert>
  );
};
