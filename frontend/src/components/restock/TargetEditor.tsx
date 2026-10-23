import { useState } from 'react';
import { Button, Group, Modal, NumberInput, SegmentedControl, Stack } from '@mantine/core';
import type { RestockTarget } from '../../restock/types';
import { inlineFailure } from '../../api/contract';

export function TargetEditor({
  opened,
  title,
  initial,
  onClose,
  onSave,
}: {
  opened: boolean;
  title: string;
  initial?: RestockTarget;
  onClose: () => void;
  onSave: (target: RestockTarget) => Promise<void>;
}) {
  const [kind, setKind] = useState<'months' | 'amount'>(initial?.kind ?? 'months');
  const [value, setValue] = useState<number | string>(initial?.value ?? 3);
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);

  const save = async () => {
    const next = typeof value === 'number' ? value : Number(value);
    if (!Number.isFinite(next) || next < 0 || next > (kind === 'months' ? 12 : 999) || (kind === 'months' && !Number.isInteger(next))) {
      setError(kind === 'months' ? 'Enter a whole number from 0 to 12.' : 'Enter an amount from 0 to 999.');
      return;
    }
    setBusy(true);
    setError('');
    try {
      await onSave({ kind, value: next });
      onClose();
    } catch (requestError) {
      setError(inlineFailure(requestError, 'Unable to save the target.') ?? '');
    } finally {
      setBusy(false);
    }
  };

  return (
    <Modal opened={opened} onClose={onClose} title={title}>
      <Stack gap="sm">
        <SegmentedControl
          value={kind}
          onChange={(next) => setKind(next as 'months' | 'amount')}
          data={[
            { value: 'months', label: 'Months of supply' },
            { value: 'amount', label: 'Fixed amount' },
          ]}
        />
        <NumberInput
          label={kind === 'months' ? 'Months' : 'Amount'}
          description="0 means don't restock."
          min={0}
          max={kind === 'months' ? 12 : 999}
          allowDecimal={kind === 'amount'}
          value={value}
          onChange={setValue}
        />
        {error !== '' && <span className="restock-quiet">{error}</span>}
        <Group justify="flex-end">
          <Button variant="default" onClick={onClose}>Cancel</Button>
          <Button loading={busy} onClick={() => void save()}>Save</Button>
        </Group>
      </Stack>
    </Modal>
  );
}
