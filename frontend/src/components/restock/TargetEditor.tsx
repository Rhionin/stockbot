import { useState } from 'react';
import { Button, Group, Modal, NumberInput, SegmentedControl, Stack } from '@mantine/core';
import type { RestockTarget } from '../../restock/types';
import { inlineFailure, OFFLINE_SENTENCE, UnreachableError } from '@stockbot/shared/contract';

function TargetEditorForm({
  initial,
  onClose,
  onSave,
}: {
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
      const message = inlineFailure(requestError, 'Unable to save the target.');
      setError(message ?? (requestError instanceof UnreachableError ? OFFLINE_SENTENCE : ''));
    } finally {
      setBusy(false);
    }
  };

  return (
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
  );
}

/**
 * Remount key for one editing session. Product identity only.
 * Putting `initial` back in this key remounts the form when a poll updates
 * the saved target and wipes what the person has typed (#729).
 */
function targetEditorSessionKey(opened: boolean, identity: string): string {
  return opened ? identity : '';
}

export function TargetEditor({
  opened,
  title,
  initial,
  sessionId,
  onClose,
  onSave,
  zIndex = 400,
}: {
  opened: boolean;
  title: string;
  initial?: RestockTarget;
  /** Product (or category) identity. Defaults to the title when the title is already unique. */
  sessionId?: string;
  onClose: () => void;
  onSave: (target: RestockTarget) => Promise<void>;
  zIndex?: number;
}) {
  return (
    <Modal opened={opened} onClose={onClose} title={title} zIndex={zIndex}>
      {opened ? (
        <TargetEditorForm
          key={targetEditorSessionKey(opened, sessionId ?? title)}
          initial={initial}
          onClose={onClose}
          onSave={onSave}
        />
      ) : null}
    </Modal>
  );
}