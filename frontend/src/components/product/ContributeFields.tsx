import { useEffect, useState } from 'react';
import { Checkbox, NativeSelect, Stack, Switch, Text } from '@mantine/core';
import { getContributionSettings, updateContributionSettings } from '../../api/client';
import type { ExternalSource } from '@stockbot/shared/types';
import { DATABASE_NAMES } from '@stockbot/shared/databaseNames';
import { inlineFailure } from '@stockbot/shared/contract';
import {
  CONTRIBUTE_CHECKBOX_LABEL,
  CONTRIBUTE_CONFIGURED_NOTE,
  CONTRIBUTE_DATABASE_LABEL,
  CONTRIBUTE_OFF_NOTE,
  CONTRIBUTE_SAVE_FAILURE,
  CONTRIBUTE_SWITCH_LABEL,
  CONTRIBUTE_UNCONFIGURED_NOTE,
  CONTRIBUTE_UPSTREAM_NOTE,
  type ContributeChoice,
} from '@stockbot/shared/productForm';

export type { ContributeChoice };

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
      setError(inlineFailure(requestError, CONTRIBUTE_SAVE_FAILURE) ?? '');
    }
  };

  return (
    <Stack gap="xs">
      <Switch
        label={CONTRIBUTE_SWITCH_LABEL}
        checked={enabled}
        onChange={(event) => void toggleEnabled(event.currentTarget.checked)}
      />
      <Text size="xs" c="dimmed">
        {CONTRIBUTE_OFF_NOTE}
      </Text>
      {enabled && allowProductOptIn && (
        <>
          <Text size="xs" c="dimmed">
            {configured ? CONTRIBUTE_CONFIGURED_NOTE : CONTRIBUTE_UNCONFIGURED_NOTE}
          </Text>
          <Checkbox
            label={CONTRIBUTE_CHECKBOX_LABEL}
            checked={checked}
            onChange={(event) => setChecked(event.currentTarget.checked)}
          />
          {checked && (
            <NativeSelect
              label={CONTRIBUTE_DATABASE_LABEL}
              data={DATABASE_OPTIONS}
              value={database}
              onChange={(event) => setDatabase(event.currentTarget.value as ExternalSource)}
            />
          )}
        </>
      )}
      {enabled && !allowProductOptIn && (
        <Text size="xs" c="dimmed">
          {CONTRIBUTE_UPSTREAM_NOTE}
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
