import { useEffect, useRef, useState } from 'react';
import { Text, View } from 'react-native';
import { inlineFailure } from '@stockbot/shared/contract';
import { addItemSubmitLabel, type AddItemMode } from '@stockbot/shared/findItem';
import {
  ENTER_NAME,
  SAVE_FAILURE,
  SIZE_PLACEHOLDER,
} from '@stockbot/shared/noBarcodeCopy';
import type { ProductWriteResult } from '@stockbot/shared/types';
import { ActionButton, TextField, type TextFieldHandle } from '@stockbot/ui';
import type { StockbotClient } from '../api/client';
import { colors, fonts } from '../ui/theme';

export type AddItemSheetMode = AddItemMode;

export interface AddItemFormProps {
  client: StockbotClient;
  mode: AddItemSheetMode;
  initialName?: string;
  // Back to where the form was opened from, after a save or Cancel.
  onClose: () => void;
  onBusyChange: (busy: boolean) => void;
  // The form leaves once this settles, so it owns the failure it reports.
  onCreated: (result: ProductWriteResult, mode: AddItemSheetMode) => void | Promise<void>;
}

export function AddItemForm({ client, mode, initialName = '', onClose, onBusyChange, onCreated }: AddItemFormProps) {
  const nameRef = useRef<TextFieldHandle>(null);
  const alive = useRef(true);
  const [name, setName] = useState(initialName);
  const [brand, setBrand] = useState('');
  const [size, setSize] = useState('');
  const [category, setCategory] = useState('');
  const [nameError, setNameError] = useState('');
  const [error, setError] = useState('');
  const [saving, setSaving] = useState(false);

  useEffect(() => {
    alive.current = true;
    return () => {
      alive.current = false;
    };
  }, []);

  useEffect(() => {
    onBusyChange(saving);
  }, [onBusyChange, saving]);

  function close() {
    if (saving) return;
    onClose();
  }

  async function save() {
    const trimmed = name.trim();
    if (trimmed === '') {
      setNameError(ENTER_NAME);
      setTimeout(() => nameRef.current?.focus(), 0);
      return;
    }
    if (saving) return;
    setSaving(true);
    setNameError('');
    setError('');
    try {
      const result = await client.createProduct({
        name: trimmed,
        brand: brand.trim(),
        size: size.trim(),
        category: category.trim(),
        unitOfMeasure: '',
      });
      await onCreated(result, mode);
      onClose();
    } catch (requestError) {
      if (alive.current) setError(inlineFailure(requestError, SAVE_FAILURE) ?? '');
    } finally {
      if (alive.current) setSaving(false);
    }
  }

  const common = { disabled: saving, fontRegular: fonts.regular, fontBold: fonts.bold, onSubmitEditing: () => { void save(); } };

  return (
    <View style={{ gap: 16 }}>
      <TextField
        ref={nameRef}
        fieldId="add-item-name"
        label="Name"
        required
        value={name}
        error={nameError}
        returnKeyType="next"
        onChangeText={(next) => {
          setName(next);
          if (nameError !== '') setNameError('');
        }}
        {...common}
      />
      <TextField fieldId="add-item-brand" label="Brand" value={brand} onChangeText={setBrand} {...common} />
      <TextField
        fieldId="add-item-size"
        label="Size"
        placeholder={SIZE_PLACEHOLDER}
        value={size}
        onChangeText={setSize}
        {...common}
      />
      <TextField fieldId="add-item-category" label="Category" value={category} onChangeText={setCategory} {...common} />
      {error !== '' ? (
        <Text role="alert" accessibilityRole="alert" style={{ fontFamily: fonts.bold, fontSize: 16, lineHeight: 22, color: colors.danger }}>
          {error}
        </Text>
      ) : null}
      <ActionButton label={addItemSubmitLabel[mode]} busy={saving} fontBold={fonts.bold} onPress={() => { void save(); }} />
      <ActionButton label="Cancel" variant="secondary" disabled={saving} fontBold={fonts.bold} onPress={close} />
    </View>
  );
}
