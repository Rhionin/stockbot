import { useState, type FormEvent } from 'react';
import { Alert, Button, Group, Modal, Stack, TextInput } from '@mantine/core';
import { addItemInstance } from '../../api/client';
import { inlineFailure } from '@stockbot/shared/contract';

export interface AddInstanceModalProps {
  itemId: string;
  opened: boolean;
  onClose: () => void;
  onAdded: () => void;
}

const expiryDateToISOString = (expiryDate: string): string =>
  new Date(`${expiryDate}T00:00:00.000Z`).toISOString();

export const AddInstanceModal = ({
  itemId,
  opened,
  onClose,
  onAdded,
}: AddInstanceModalProps) => {
  const [expiryDate, setExpiryDate] = useState('');
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState('');

  const closeModal = () => {
    setExpiryDate('');
    setError('');
    onClose();
  };

  const handleSubmit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    setSubmitting(true);
    setError('');
    try {
      await addItemInstance(itemId, expiryDateToISOString(expiryDate));
      closeModal();
      onAdded();
    } catch (requestError) {
      setError(inlineFailure(requestError, 'Unable to add instance.') ?? '');
    } finally {
      setSubmitting(false);
    }
  };

  return (
    <Modal opened={opened} onClose={closeModal} title="Add item instance">
      <form onSubmit={(event) => void handleSubmit(event)}>
        <Stack gap="sm">
          <TextInput
            label="Expiration date"
            type="date"
            required
            value={expiryDate}
            onChange={(event) => setExpiryDate(event.currentTarget.value)}
          />
          {error !== '' && (
            <Alert color="red" py="xs">
              {error}
            </Alert>
          )}
          <Group justify="flex-end">
            <Button size="xs" variant="default" onClick={closeModal}>Cancel</Button>
            <Button size="xs" type="submit" loading={submitting} disabled={expiryDate === ''}>
              Add instance
            </Button>
          </Group>
        </Stack>
      </form>
    </Modal>
  );
};
