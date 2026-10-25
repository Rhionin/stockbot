import { useEffect, useState } from 'react';
import { Button, Drawer, Stack, Text, TextInput } from '@mantine/core';
import { createProduct } from '../../api/client';
import type { ProductWriteResult } from '@stockbot/shared/types';
import { ADD_IT_AND_TAKE_IT_OUT, inlineFailure } from '@stockbot/shared/contract';
import {
  ADD_AND_STOCK_IN,
  ADD_ITEM_SUBMIT,
  ADD_ITEM_TITLE,
  ENTER_NAME,
  SAVE_FAILURE,
  SIZE_PLACEHOLDER,
} from './noBarcodeCopy';

export type AddItemSheetMode = 'inventory' | 'stock_in' | 'stock_out';

export interface AddItemSheetProps {
  opened: boolean;
  mode: AddItemSheetMode;
  initialName?: string;
  onClose: () => void;
  onCreated: (result: ProductWriteResult, mode: AddItemSheetMode) => void | Promise<void>;
}

const submitLabel: Record<AddItemSheetMode, string> = {
  inventory: ADD_ITEM_SUBMIT,
  stock_in: ADD_AND_STOCK_IN,
  stock_out: ADD_IT_AND_TAKE_IT_OUT,
};

export function AddItemSheet({
  opened,
  mode,
  initialName = '',
  onClose,
  onCreated,
}: AddItemSheetProps) {
  const [name, setName] = useState(initialName);
  const [brand, setBrand] = useState('');
  const [size, setSize] = useState('');
  const [category, setCategory] = useState('');
  const [nameError, setNameError] = useState('');
  const [error, setError] = useState('');
  const [saving, setSaving] = useState(false);

  useEffect(() => {
    if (!opened) return;
    /* eslint-disable react-hooks/set-state-in-effect -- reset sheet when opened */
    setName(initialName);
    setBrand('');
    setSize('');
    setCategory('');
    setNameError('');
    setError('');
    setSaving(false);
    /* eslint-enable react-hooks/set-state-in-effect */
  }, [opened, initialName]);

  const close = () => {
    if (saving) return;
    onClose();
  };

  const save = async () => {
    const trimmed = name.trim();
    if (trimmed === '') {
      setNameError(ENTER_NAME);
      return;
    }
    if (saving) return;
    setSaving(true);
    setNameError('');
    setError('');
    try {
      const result = await createProduct({
        name: trimmed,
        brand: brand.trim(),
        size: size.trim(),
        category: category.trim(),
        unitOfMeasure: '',
      });
      await onCreated(result, mode);
      onClose();
    } catch (requestError) {
      setError(inlineFailure(requestError, SAVE_FAILURE) ?? '');
    } finally {
      setSaving(false);
    }
  };

  return (
    <Drawer.Root opened={opened} onClose={close} position="bottom" transitionProps={{ duration: 0 }}>
      <Drawer.Overlay />
      <Drawer.Content className="bin-page shelf-sheet">
        <Drawer.Header component="div">
          <Drawer.Title>{ADD_ITEM_TITLE}</Drawer.Title>
          <Drawer.CloseButton aria-label="Close" />
        </Drawer.Header>
        <Drawer.Body className="shelf-sheet-body">
          <Stack gap="sm" component="form" onSubmit={(event) => {
            event.preventDefault();
            void save();
          }}
          >
            <TextInput
              label="Name"
              required
              value={name}
              error={nameError !== '' ? nameError : undefined}
              onChange={(event) => {
                setName(event.currentTarget.value);
                if (nameError !== '') setNameError('');
              }}
            />
            <TextInput
              label="Brand"
              value={brand}
              onChange={(event) => setBrand(event.currentTarget.value)}
            />
            <TextInput
              label="Size"
              placeholder={SIZE_PLACEHOLDER}
              value={size}
              onChange={(event) => setSize(event.currentTarget.value)}
            />
            <TextInput
              label="Category"
              value={category}
              onChange={(event) => setCategory(event.currentTarget.value)}
            />
            {error !== '' && <Text size="sm" c="red">{error}</Text>}
            <Button type="submit" loading={saving}>
              {submitLabel[mode]}
            </Button>
            <Button variant="default" disabled={saving} onClick={close}>
              Cancel
            </Button>
          </Stack>
        </Drawer.Body>
      </Drawer.Content>
    </Drawer.Root>
  );
}
