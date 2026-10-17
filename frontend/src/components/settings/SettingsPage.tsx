import { useCallback, useEffect, useRef, useState } from 'react';
import { Alert, NumberInput, Select, Stack, Text, Title } from '@mantine/core';
import { getDefaultGroupRule, getSupplySettings, setDefaultGroupRule, setSupplyMonths } from '../../api/client';
import { ruleLabels } from '../groups/copy';
import { WipeInventoryDialog } from '../inventory/WipeInventoryDialog';
import { RestockSettings } from '../restock/RestockSettings';
import { ScanAlertSetting } from './ScanAlertSetting';

export const SettingsPage = () => {
  const [months, setMonths] = useState<number | string>(3);
  const monthsRef = useRef(months);
  const [rule, setRule] = useState('same_as_ran_out');
  const [phrase, setPhrase] = useState('WIPE INVENTORY');
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');

  const load = useCallback(async () => {
    setLoading(true);
    setError('');
    try {
      const [settings, defaultRule] = await Promise.all([
        getSupplySettings(),
        getDefaultGroupRule().catch(() => ({ rule: 'same_as_ran_out' })),
      ]);
      monthsRef.current = settings.months;
      setMonths(settings.months);
      setPhrase(settings.wipePhrase);
      setRule(defaultRule.rule);
    } catch (requestError) {
      setError(requestError instanceof Error ? requestError.message : 'Unable to load settings.');
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    void Promise.resolve().then(load);
  }, [load]);

  const commitMonths = async () => {
    const value = monthsRef.current;
    const next = typeof value === 'number' ? value : Number(value);
    if (!Number.isInteger(next) || next < 1 || next > 12) return;
    setError('');
    try {
      const settings = await setSupplyMonths(next);
      monthsRef.current = settings.months;
      setMonths(settings.months);
    } catch (requestError) {
      setError(requestError instanceof Error ? requestError.message : 'Unable to save the supply length.');
    }
  };

  return (
    <Stack gap="lg">
      <Title order={1} size="h3">Settings</Title>
      <NumberInput
        label="Months of supply"
        description="How far ahead the shopping list looks once a steady rate exists. 1 to 12. Unset means 3."
        min={1}
        max={12}
        allowDecimal={false}
        value={months}
        disabled={loading}
        onChange={(value) => {
          monthsRef.current = value;
          setMonths(value);
        }}
        onBlur={() => void commitMonths()}
        w={180}
      />
      <Select
        label="Default group rule"
        description="A new group starts here. It still asks you to pick a rule, including Same product."
        data={Object.entries(ruleLabels).map(([value, label]) => ({ value, label }))}
        value={rule}
        disabled={loading}
        allowDeselect={false}
        onChange={(value) => {
          if (!value) return;
          setRule(value);
          void setDefaultGroupRule(value).catch((requestError: unknown) => {
            setError(requestError instanceof Error ? requestError.message : 'Unable to save the default rule.');
          });
        }}
        w={280}
      />
      <ScanAlertSetting />
      <RestockSettings />
      {error !== '' && <Alert color="red" py="xs">{error}</Alert>}
      <Stack gap="xs" mt="xl">
        <Text size="sm" c="dimmed">
          Wiping inventory clears the shelves and the opening date. The next bulk scan is a new snapshot.
        </Text>
        <WipeInventoryDialog phrase={phrase} onWiped={() => void load()} />
      </Stack>
    </Stack>
  );
};
