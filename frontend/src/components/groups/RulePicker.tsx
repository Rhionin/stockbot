import { useState } from 'react';
import { Button, Drawer, Radio, Select, Stack, Text } from '@mantine/core';
import { setGroupRule } from '../../api/client';
import { ruleChoices } from './copy';
import { inlineFailure } from '../../api/contract';

export interface RulePickerGroup {
  id: string;
  rule: string;
  pinnedProductId?: string;
  members: { productId: string; name: string }[];
}

export const RulePicker = ({
  group,
  opened,
  onClose,
  onSaved,
}: {
  group: RulePickerGroup;
  opened: boolean;
  onClose: () => void;
  onSaved: () => void;
}) => {
  const [rule, setRule] = useState(group.rule);
  const [pin, setPin] = useState(group.pinnedProductId ?? '');
  const [error, setError] = useState('');
  const [saving, setSaving] = useState(false);
  const memberOptions = group.members.map((member) => ({ value: member.productId, label: member.name }));
  const productLabel = rule === 'favorite' ? 'Product' : 'If no price is known';
  const namesAProduct = rule !== 'same_as_ran_out' && rule !== 'favor_variety';

  const save = async () => {
    setSaving(true);
    setError('');
    try {
      const pinnedProductId = namesAProduct ? pin : '';
      await setGroupRule(group.id, rule, pinnedProductId, true);
      onSaved();
      onClose();
    } catch (requestError) {
      setError(inlineFailure(requestError, 'Unable to save the rule.') ?? '');
    } finally {
      setSaving(false);
    }
  };

  return (
    <Drawer.Root opened={opened} onClose={onClose} position="bottom" size="auto" transitionProps={{ duration: 0 }}>
      <Drawer.Overlay />
      <Drawer.Content className="rule-sheet">
        {/* A header element here is a second banner landmark beside the app bar. */}
        <Drawer.Header component="div">
          <Drawer.Title>What to buy next</Drawer.Title>
          <Drawer.CloseButton aria-label="Close" />
        </Drawer.Header>
        <Drawer.Body>
          <Stack gap="md" pb="md">
            <Radio.Group value={rule} onChange={setRule}>
              <Stack gap="sm">
                {ruleChoices.map((choice) => (
                  <div key={choice.id}>
                    <Radio value={choice.id} label={choice.label} description={choice.hint} />
                    {rule === choice.id && choice.id !== 'same_as_ran_out' && choice.id !== 'favor_variety' && (
                      <Select
                        className="rule-choice-extra"
                        aria-label={productLabel}
                        placeholder={productLabel}
                        data={memberOptions}
                        value={pin === '' ? null : pin}
                        onChange={(value) => setPin(value ?? '')}
                        allowDeselect={choice.id === 'best_deal'}
                        comboboxProps={{ withinPortal: true }}
                      />
                    )}
                  </div>
                ))}
              </Stack>
            </Radio.Group>
            <p className="rule-skip-note">Products marked "Don't restock" are skipped.</p>
            {error !== '' && <Text size="sm" c="red">{error}</Text>}
            <Button color="dark" onClick={() => void save()} loading={saving} disabled={rule === 'favorite' && pin === ''}>
              Save
            </Button>
          </Stack>
        </Drawer.Body>
      </Drawer.Content>
    </Drawer.Root>
  );
};
