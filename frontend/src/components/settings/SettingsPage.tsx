import { useCallback, useEffect, useState } from 'react';
import { Alert, Select, Stack, Text, Title } from '@mantine/core';
import { getDefaultGroupRule, getSupplySettings, setDefaultGroupRule } from '../../api/client';
import { ruleLabels } from '../groups/copy';
import { WipeInventoryDialog } from '../inventory/WipeInventoryDialog';
import { RestockSettings } from '../restock/RestockSettings';
import { ScanAlertSetting } from './ScanAlertSetting';

export const SettingsPage = () => {
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

  return (
    <Stack gap="lg">
      <Title order={1} size="h3">Settings</Title>
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
