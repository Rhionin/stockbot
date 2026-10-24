import { useEffect, useState } from 'react';
import { Checkbox, NativeSelect, Stack, Switch, Text } from '@mantine/core';
import { getContributionSettings, updateContributionSettings } from '../../api/client';
import type { ExternalSource } from '@stockbot/shared/types';
import { DATABASE_NAMES } from './databaseNames';
import { inlineFailure } from '@stockbot/shared/contract';

export interface ContributeChoice {
  contribute: boolean;
  contributeTo: ExternalSource;
}

const DATABASE_OPTIONS = (Object.keys(DATABASE_NAMES) as ExternalSource[]).map((value) => ({
  value,
  label: DATABASE_NAMES[value],
}));

export interface ContributeFieldsProps {
  onChange: (choice: ContributeChoice) => void;
  // Upstream products stay in the pantry. The household switch is still shown
  // so the setting is findable, but the per-product checkbox is not.
  allowProductOptIn?: boolean;
}

export const ContributeFields = ({ onChange, allowProductOptIn = true }: ContributeFieldsProps) => {
  const [enabled, setEnabled] = useState(false);
  const [configured, setConfigured] = useState(false);
  const [checked, setChecked] = useState(false);
  const [database, setDatabase] = useState<ExternalSource>('openfoodfacts');
  const [error, setError] = useState('');

  useEffect(() => {
    let active = true;
    void getContributionSettings()
      .then((settings) => {
        if (!active) return;
        setEnabled(settings.enabled === true);
        setConfigured(settings.configured === true);
      })
      .catch(() => {
        // A failed read leaves sharing off, which is the safe default.
        if (!active) return;
        setEnabled(false);
      });
    return () => {
      active = false;
    };
  }, []);

  useEffect(() => {
    onChange({
      contribute: enabled && allowProductOptIn && checked,
      contributeTo: database,
    });
  }, [allowProductOptIn, checked, database, enabled, onChange]);

  const toggleEnabled = async (next: boolean) => {
    setError('');
    setEnabled(next);
    if (!next) setChecked(false);
    try {
      const saved = await updateContributionSettings(next);
      setEnabled(saved.enabled);
      setConfigured(saved.configured);
      if (!saved.enabled) setChecked(false);
    } catch (requestError) {
      setEnabled(!next);
      setError(inlineFailure(requestError, 'Unable to save sharing setting.') ?? '');
    }
  };

  return (
    <Stack gap="xs">
      <Switch
        label="Let me contribute products I type in"
        checked={enabled}
        onChange={(event) => void toggleEnabled(event.currentTarget.checked)}
      />
      <Text size="xs" c="dimmed">
        Off by default. Turning this on does not send anything until you also check Contribute on a product.
      </Text>
      {enabled && allowProductOptIn && (
        <>
          <Text size="xs" c="dimmed">
            {configured
              ? 'A product is sent only when you check Contribute on that product.'
              : 'This Stockbot is not signed in to the open databases. A product you choose to contribute is saved here and is not sent.'}
          </Text>
          <Checkbox
            label="Contribute this product"
            checked={checked}
            onChange={(event) => setChecked(event.currentTarget.checked)}
          />
          {checked && (
            <NativeSelect
              label="Open database"
              data={DATABASE_OPTIONS}
              value={database}
              onChange={(event) => setDatabase(event.currentTarget.value as ExternalSource)}
            />
          )}
        </>
      )}
      {enabled && !allowProductOptIn && (
        <Text size="xs" c="dimmed">
          This product already comes from an open database, so edits stay in your pantry.
        </Text>
      )}
      {error !== '' && (
        <Text size="xs" c="red">
          {error}
        </Text>
      )}
    </Stack>
  );
};
