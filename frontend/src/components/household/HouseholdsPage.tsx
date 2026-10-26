import { useState, type FormEvent } from 'react';
import { Alert, Badge, Button, Group, Paper, Radio, Stack, Text, TextInput, Title } from '@mantine/core';
import { useNavigate } from 'react-router-dom';
import { hostedErrorSentence, type RestockMode } from '@stockbot/shared/contract';
import {
  ApiError,
  createHousehold,
  createOperatorHouseholdInvite,
  currentAuthSession,
  getSession,
  switchHousehold,
} from '../../api/client';
import { useHouseholdSession } from '../auth/householdSession';
import { announceHouseholdSwitch } from './liveHints';

const RESTOCK: { value: RestockMode; label: string }[] = [
  { value: 'auto', label: 'Restock new items automatically' },
  { value: 'ask_restock', label: 'Ask me before restocking a new item' },
  { value: 'ask_hold', label: 'Ask me, and hold the item until I answer' },
];

function hosted(error: unknown, fallback: string): string {
  if (error instanceof ApiError && error.code !== '') return hostedErrorSentence(error.code);
  return error instanceof Error ? error.message : fallback;
}

export function extractInviteCode(input: string): string {
  const trimmed = input.trim();
  const match = /\/invite\/([^/?#]+)/.exec(trimmed);
  if (match) {
    return decodeURIComponent(match[1]);
  }
  return trimmed;
}

function copyLink(url: string) {
  void navigator.clipboard?.writeText(url);
}

export function HouseholdsPage() {
  const navigate = useNavigate();
  const { auth, replaceAuth } = useHouseholdSession();
  const households = auth?.households ?? [];

  const [switchingId, setSwitchingId] = useState('');
  const [switchError, setSwitchError] = useState('');
  const [switchNotice, setSwitchNotice] = useState('');

  const [startName, setStartName] = useState('');
  const [startCode, setStartCode] = useState('');
  const [restockMode, setRestockMode] = useState<RestockMode>('auto');
  const [startLoading, setStartLoading] = useState(false);
  const [startError, setStartError] = useState('');
  const [startNotice, setStartNotice] = useState('');

  const [operatorInviteLoading, setOperatorInviteLoading] = useState(false);
  const [operatorInviteUrl, setOperatorInviteUrl] = useState('');
  const [operatorInviteError, setOperatorInviteError] = useState('');

  const switchTo = async (householdId: string, householdName: string) => {
    setSwitchError('');
    setSwitchNotice('');
    setSwitchingId(householdId);
    try {
      const session = await switchHousehold(householdId);
      replaceAuth(session);
      announceHouseholdSwitch();
      setSwitchNotice(`Switched to ${householdName}.`);
    } catch (requestError) {
      setSwitchError(hosted(requestError, 'Could not switch households.'));
    } finally {
      setSwitchingId('');
    }
  };

  const startHousehold = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    setStartError('');
    setStartNotice('');
    setStartLoading(true);
    try {
      let code = extractInviteCode(startCode);
      if (code === '' && auth?.operator === true) {
        const invite = await createOperatorHouseholdInvite();
        code = invite.code;
      }
      const created = await createHousehold({ name: startName.trim(), restockMode, inviteCode: code });
      const switched = await switchHousehold(created.id).catch(() => null);
      if (switched) {
        replaceAuth(switched);
        announceHouseholdSwitch();
      } else {
        await getSession();
        const session = currentAuthSession();
        if (session) replaceAuth(session);
      }
      setStartNotice(`${created.name || startName.trim()} was created.`);
      setStartName('');
      setStartCode('');
    } catch (requestError) {
      if (requestError instanceof ApiError && requestError.code === 'not_found') {
        setStartError("That invite code wasn't found. Check the code or ask for a new invite.");
      } else {
        setStartError(hosted(requestError, hostedErrorSentence('invite_required')));
      }
    } finally {
      setStartLoading(false);
    }
  };

  const createOperatorInvite = async () => {
    setOperatorInviteError('');
    setOperatorInviteUrl('');
    setOperatorInviteLoading(true);
    try {
      const invite = await createOperatorHouseholdInvite();
      setOperatorInviteUrl(invite.url);
    } catch (requestError) {
      setOperatorInviteError(hosted(requestError, 'Could not create a start-household invite.'));
    } finally {
      setOperatorInviteLoading(false);
    }
  };

  const operator = auth?.operator === true;
  const inviteRequired = !operator;

  return (
    <Stack className="household-page" gap="lg">
      <Title order={2}>Households</Title>

      <Paper withBorder p="md" radius="md" className="household-card" component="section" aria-labelledby="your-households-heading">
        <Title id="your-households-heading" order={3}>Your households</Title>
        {switchError !== '' && <Alert color="red" variant="light" role="alert" mt="sm">{switchError}</Alert>}
        {switchNotice !== '' && <Alert color="teal" variant="light" role="status" mt="sm">{switchNotice}</Alert>}
        <ul className="household-list">
          {households.map((household) => {
            const isCurrent = household.id === auth?.activeHouseholdId;
            const label = household.deletedAt ? `${household.name} (deleted)` : household.name;
            return (
              <li key={household.id}>
                <Group gap="xs" wrap="wrap">
                  <span>{label}</span>
                  <span>{household.role === 'owner' ? 'Owner' : 'Member'}</span>
                  {isCurrent && <Badge variant="light">Current</Badge>}
                  {isCurrent ? (
                    <Button size="compact-sm" variant="default" onClick={() => navigate('/household')}>
                      Manage household
                    </Button>
                  ) : (
                    <Button
                      size="compact-sm"
                      variant="default"
                      loading={switchingId === household.id}
                      onClick={() => { void switchTo(household.id, household.name); }}
                    >
                      Switch
                    </Button>
                  )}
                </Group>
              </li>
            );
          })}
        </ul>
      </Paper>

      <Paper withBorder p="md" radius="md" className="household-card" component="section" aria-labelledby="start-household-heading">
        <Title id="start-household-heading" order={3}>Start a household</Title>
        <form aria-label="Start a household" onSubmit={(event) => { void startHousehold(event); }}>
          <Stack gap="sm" mt="sm">
            <TextInput
              label="Household name"
              value={startName}
              onChange={(event) => setStartName(event.currentTarget.value)}
              required
            />
            <Radio.Group label="When a new item shows up" value={restockMode} onChange={(value) => setRestockMode(value as RestockMode)}>
              <Stack gap="xs" mt="xs">
                {RESTOCK.map((choice) => <Radio key={choice.value} value={choice.value} label={choice.label} />)}
              </Stack>
            </Radio.Group>
            <TextInput
              label="Invite code"
              description={operator
                ? 'Optional for operators — leave blank to create a household directly, or paste an invite code or link.'
                : 'Paste an invite code or invite link.'}
              value={startCode}
              onChange={(event) => setStartCode(event.currentTarget.value)}
              required={inviteRequired}
            />
            {startError !== '' && <Alert color="red" variant="light" role="alert">{startError}</Alert>}
            {startNotice !== '' && <Alert color="teal" variant="light" role="status">{startNotice}</Alert>}
            <Button type="submit" loading={startLoading}>Create household</Button>
          </Stack>
        </form>
      </Paper>

      {operator && (
        <Paper withBorder p="md" radius="md" className="household-card" component="section" aria-labelledby="operator-invite-heading">
          <Title id="operator-invite-heading" order={3}>Invite someone else to start a household</Title>
          <Text mt="sm">Create a one-time invite link to send to someone else so they can start their own household.</Text>
          <Button
            type="button"
            variant="default"
            mt="sm"
            loading={operatorInviteLoading}
            onClick={() => { void createOperatorInvite(); }}
          >
            Create a start-household invite
          </Button>
          {operatorInviteError !== '' && <Alert color="red" variant="light" role="alert" mt="sm">{operatorInviteError}</Alert>}
          {operatorInviteUrl !== '' && (
            <Group gap="xs" mt="sm" wrap="wrap">
              <Text role="status">Invite link: {operatorInviteUrl}</Text>
              <Button size="compact-sm" variant="default" onClick={() => copyLink(operatorInviteUrl)}>
                Copy link
              </Button>
            </Group>
          )}
        </Paper>
      )}
    </Stack>
  );
}
