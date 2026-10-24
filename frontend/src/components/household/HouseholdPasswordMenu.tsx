import { useEffect, useState, type ReactNode } from 'react';
import { Button, Group, List, Modal, Text } from '@mantine/core';
import { Menu } from '@mantine/core';
import {
  olderAppDevicesSentence,
  RESTORE_HOUSEHOLD_PASSWORD,
  RETIRE_HOUSEHOLD_PASSWORD,
  type HouseholdSettings,
  type Member,
} from '@stockbot/shared/contract';
import {
  getChangeoverTelemetry,
  getHouseholdSettings,
  listMembers,
  restoreHouseholdPassword,
  retireHouseholdPassword,
} from '../../api/client';
import { useHouseholdSession } from '../auth/householdSession';

export function useHouseholdPassword(opened: boolean): { items: ReactNode; dialogs: ReactNode } {
  const { auth } = useHouseholdSession();
  const active = auth?.households.find((row) => row.id === auth.activeHouseholdId) ?? null;
  const owner = auth?.kind === 'account' && active?.role === 'owner';
  const [settings, setSettings] = useState<HouseholdSettings | null>(null);
  const [members, setMembers] = useState<Member[]>([]);
  const [olderDevices, setOlderDevices] = useState(0);
  const [mode, setMode] = useState<'retire' | 'restore' | null>(null);

  useEffect(() => {
    if (!opened || !owner || !auth?.activeHouseholdId) return undefined;
    const householdId = auth.activeHouseholdId;
    let cancelled = false;
    void (async () => {
      try {
        const next = await getHouseholdSettings();
        if (cancelled) return;
        setSettings(next);
        if (!next.householdPasswordEnabled && !next.householdPasswordRestoreUntil) return;
        setMembers(await listMembers(householdId));
        try {
          const telemetry = await getChangeoverTelemetry();
          if (!cancelled) setOlderDevices(telemetry.olderAppDevices ?? 0);
        } catch {
          if (!cancelled) setOlderDevices(0);
        }
      } catch {
        if (!cancelled) setSettings(null);
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [opened, owner, auth?.activeHouseholdId]);

  const warning = settings ? olderAppDevicesSentence(olderDevices) : null;
  const canRetire = Boolean(settings?.householdPasswordEnabled);
  const canRestore = Boolean(settings && !settings.householdPasswordEnabled && settings.householdPasswordRestoreUntil);
  const items = owner && settings && (canRetire || canRestore) ? (
    <>
      {canRetire && (
        <Menu.Item onClick={() => setMode('retire')}>
          {RETIRE_HOUSEHOLD_PASSWORD}
        </Menu.Item>
      )}
      {canRestore && (
        <Menu.Item onClick={() => setMode('restore')}>
          {RESTORE_HOUSEHOLD_PASSWORD}
        </Menu.Item>
      )}
    </>
  ) : null;

  const dialogs = (
    <>
      <Modal opened={mode === 'retire'} onClose={() => setMode(null)} title={RETIRE_HOUSEHOLD_PASSWORD}>
        <Text>People with their own accounts stay signed in. The shared password stops working.</Text>
        <List mt="sm">
          {members.map((member) => <List.Item key={member.userId}>{member.email}</List.Item>)}
        </List>
        {warning && <Text mt="sm">{warning}</Text>}
        <Group justify="flex-end" mt="md">
          <Button variant="default" onClick={() => setMode(null)}>Cancel</Button>
          <Button
            className="household-danger"
            color="red"
            onClick={() => {
              void retireHouseholdPassword().then((next) => {
                setSettings(next);
                setMode(null);
              });
            }}
          >
            Turn off
          </Button>
        </Group>
      </Modal>
      <Modal opened={mode === 'restore'} onClose={() => setMode(null)} title={RESTORE_HOUSEHOLD_PASSWORD}>
        <Text>
          {settings?.householdPasswordRestoreUntil
            ? `You can turn it back on until ${new Date(settings.householdPasswordRestoreUntil).toLocaleString()}.`
            : 'You can turn the shared password back on.'}
        </Text>
        <Group justify="flex-end" mt="md">
          <Button variant="default" onClick={() => setMode(null)}>Cancel</Button>
          <Button
            onClick={() => {
              void restoreHouseholdPassword().then((next) => {
                setSettings(next);
                setMode(null);
              });
            }}
          >
            Turn back on
          </Button>
        </Group>
      </Modal>
    </>
  );

  return { items, dialogs };
}
