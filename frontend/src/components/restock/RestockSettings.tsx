import { useCallback, useEffect, useState } from 'react';
import { Link } from 'react-router-dom';
import { ActionIcon, Button, Group, Menu, Modal, Stack, Text, TextInput, Title } from '@mantine/core';
import { createCategory, deleteCategory, getRestock, setHouseholdTarget } from '../../api/client';
import type { RestockSnapshot, RestockTarget } from '../../restock/types';
import { restockCopy } from '../../restock/copy';
import { TargetEditor } from './TargetEditor';
import './restock.css';

function Kebab() {
  return (
    <svg viewBox="0 0 16 16" width="16" height="16" aria-hidden="true">
      <circle cx="8" cy="3.1" r="1.35" fill="currentColor" />
      <circle cx="8" cy="8" r="1.35" fill="currentColor" />
      <circle cx="8" cy="12.9" r="1.35" fill="currentColor" />
    </svg>
  );
}

export function RestockSettings() {
  const [snap, setSnap] = useState<RestockSnapshot | null>(null);
  const [open, setOpen] = useState(false);
  const [more, setMore] = useState(false);
  const [creating, setCreating] = useState(false);
  const [name, setName] = useState('');

  const load = useCallback(async () => {
    try {
      setSnap(await getRestock());
    } catch {
      setSnap(null);
    }
  }, []);

  useEffect(() => {
    void Promise.resolve().then(load);
  }, [load]);

  if (!snap) return null;

  const shown = more ? snap.categories : snap.categories.slice(0, 6);
  const saveHousehold = async (target: RestockTarget) => {
    setSnap(await setHouseholdTarget(target));
  };

  return (
    <div className="restock-settings">
      <Title order={2} size="h4">{restockCopy.section}</Title>
      <Text size="sm" c="dimmed">{restockCopy.sectionBody}</Text>
      <div className="restock-order">
        <span>Item</span>›<span>Group</span>›<span>Category</span>›<span className="is-here">Household default</span>
      </div>
      <Group align="end" mt="sm" wrap="wrap">
        <div>
          <Text fw={600} size="sm">Household default</Text>
          <Text size="sm">{snap.householdLabel}</Text>
          <Text size="xs" c="dimmed">{restockCopy.applies(snap.appliesTo, snap.itemCount)}</Text>
        </div>
        <Button size="xs" variant="light" onClick={() => setOpen(true)}>Change</Button>
      </Group>
      <Group justify="space-between" mt="lg">
        <Title order={2} size="h4">Categories</Title>
        <Button size="xs" variant="subtle" onClick={() => { setName(''); setCreating(true); }}>+ New category</Button>
      </Group>
      <Text size="sm" c="dimmed">Shared by everyone in the household.</Text>
      {snap.suggestionCount > 0 && (
        <div className="restock-suggest">
          <Text size="sm" style={{ flex: 1 }}><b>{restockCopy.suggestions(snap.suggestionCount)}</b></Text>
          <Button size="xs" component={Link} to="/settings/categories">Review</Button>
        </div>
      )}
      {shown.map((category) => (
        <div className="restock-row" key={category.id}>
          <div className="name">
            <b><Link to={`/inventory/categories/${category.id}`}>{category.name}</Link></b>
            <span>
              {`${category.items} items`}
              {category.ownTargets > 0 ? ` · ${category.ownTargets} have their own target` : ''}
            </span>
          </div>
          <div className="value">
            {category.valueLabel}
            <span>{category.sourceLabel}</span>
          </div>
          <Menu position="bottom-end">
            <Menu.Target>
              <ActionIcon variant="subtle" aria-label={`Actions for ${category.name}`}><Kebab /></ActionIcon>
            </Menu.Target>
            <Menu.Dropdown>
              <Menu.Item component={Link} to={`/inventory/categories/${category.id}`}>Restock target…</Menu.Item>
              <Menu.Item color="red" onClick={() => {
                if (window.confirm(`Delete ${category.name}? Items stay in inventory.`)) {
                  void deleteCategory(category.id).then(() => load());
                }
              }}
              >
                Delete category…
              </Menu.Item>
            </Menu.Dropdown>
          </Menu>
        </div>
      ))}
      {snap.categories.length > 6 && (
        <Button variant="subtle" size="xs" mt="xs" onClick={() => setMore((current) => !current)}>
          {more ? 'Show fewer' : `Show ${snap.categories.length - 6} more`}
        </Button>
      )}
      <TargetEditor opened={open} title="Household default" initial={snap.household} onClose={() => setOpen(false)} onSave={saveHousehold} />
      <Modal opened={creating} onClose={() => setCreating(false)} title="New category">
        <Stack>
          <TextInput label="Name" value={name} onChange={(event) => setName(event.currentTarget.value)} />
          <Button onClick={() => void createCategory(name).then(() => { setCreating(false); void load(); })}>Create</Button>
        </Stack>
      </Modal>
    </div>
  );
}
