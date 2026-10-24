import { useCallback, useEffect, useState, type FormEvent } from 'react';
import { Alert, Button, Group, Radio, Stack, Text, TextInput, Title } from '@mantine/core';
import {
  HOUSEHOLD_DELETE_SENTENCE,
  classifyImportDocument,
  hostedErrorSentence,
  type Household,
  type HouseholdSettings,
  type InviteSummary,
  type Member,
  type RestockMode,
} from '@stockbot/shared/contract';
import {
  ApiError,
  changeMemberRole,
  currentAuthSession,
  getSession,
  createHousehold,
  createInvite,
  createOperatorHouseholdInvite,
  createResetLink,
  deleteAccount,
  deleteHousehold,
  getExportJob,
  getHousehold,
  getHouseholdSettings,
  getImportJob,
  listInvites,
  listMembers,
  removeMember,
  restoreHousehold,
  revokeInvite,
  startExport,
  startImport,
  createImportUpload,
} from '../../api/client';
import { useHouseholdSession } from '../auth/householdSession';

const RESTOCK: { value: RestockMode; label: string }[] = [
  { value: 'auto', label: 'Restock new items automatically' },
  { value: 'ask_restock', label: 'Ask me before restocking a new item' },
  { value: 'ask_hold', label: 'Ask me, and hold the item until I answer' },
];

function hosted(error: unknown, fallback: string): string {
  if (error instanceof ApiError && error.code !== '') return hostedErrorSentence(error.code);
  return error instanceof Error ? error.message : fallback;
}

function inviteLabel(invite: InviteSummary): string {
  if (invite.status === 'revoked') return 'Cancelled';
  if (invite.status === 'used') return 'Used';
  if (invite.status === 'expired') return 'Expired';
  return `Expires ${new Date(invite.expiresAt).toLocaleString()}`;
}

export function HouseholdPage() {
  const { auth, replaceAuth, logout } = useHouseholdSession();
  const active = auth?.households.find((row) => row.id === auth.activeHouseholdId) ?? null;
  const owner = active?.role === 'owner';
  const householdId = auth?.activeHouseholdId ?? '';
  const [members, setMembers] = useState<Member[]>([]);
  const [invites, setInvites] = useState<InviteSummary[]>([]);
  const [settings, setSettings] = useState<HouseholdSettings | null>(null);
  const [household, setHousehold] = useState<Household | null>(null);
  const [error, setError] = useState('');
  const [notice, setNotice] = useState('');
  const [inviteRole, setInviteRole] = useState<'member' | 'owner'>('member');
  const [createdUrl, setCreatedUrl] = useState('');
  const [exportLinks, setExportLinks] = useState<{ jsonUrl: string | null; csvUrl: string | null } | null>(null);
  const [exporting, setExporting] = useState(false);
  const [importing, setImporting] = useState(false);
  const [deleteName, setDeleteName] = useState('');
  const [accountEmail, setAccountEmail] = useState('');
  const [startName, setStartName] = useState('');
  const [startCode, setStartCode] = useState('');
  const [restockMode, setRestockMode] = useState<RestockMode>('auto');

  const load = useCallback(async () => {
    if (householdId === '') return;
    try {
      const [people, pending, nextSettings, detail] = await Promise.all([
        listMembers(householdId),
        listInvites(),
        getHouseholdSettings(),
        getHousehold(householdId),
      ]);
      setMembers(people);
      setInvites(pending);
      setSettings(nextSettings);
      setHousehold(detail);
      setError('');
    } catch (requestError) {
      setError(hosted(requestError, 'Stockbot could not load this household.'));
    }
  }, [householdId]);

  useEffect(() => {
    void Promise.resolve().then(load);
  }, [load]);

  const create = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    setError('');
    try {
      const created = await createInvite(owner && inviteRole === 'owner' ? { role: 'owner' } : {});
      setCreatedUrl(created.url);
      await load();
    } catch (requestError) {
      setError(hosted(requestError, hostedErrorSentence('forbidden_role')));
    }
  };

  const exportHousehold = async () => {
    setExporting(true);
    setError('');
    setNotice('');
    try {
      const created = await startExport();
      let job = await getExportJob(created.jobId);
      for (let attempt = 0; job.status === 'pending' && attempt < 10; attempt += 1) {
        await new Promise((resolve) => window.setTimeout(resolve, 200));
        job = await getExportJob(created.jobId);
      }
      if (job.status === 'failed') {
        setError('The export failed. Try again.');
      } else {
        setExportLinks({ jsonUrl: job.jsonUrl, csvUrl: job.csvUrl });
        setNotice(job.status === 'ready' ? 'Export ready.' : 'Export in progress.');
      }
    } catch (requestError) {
      setError(hosted(requestError, hostedErrorSentence('forbidden_role')));
    } finally {
      setExporting(false);
    }
  };

  const importFile = async (file: File) => {
    setImporting(true);
    setError('');
    try {
      const text = await file.text();
      const parsed = JSON.parse(text) as unknown;
      const classification = classifyImportDocument(parsed);
      if (!classification.ok) {
        setError(hostedErrorSentence('validation'));
        return;
      }
      const upload = await createImportUpload(file.size);
      await fetch(upload.uploadUrl, { method: 'PUT', body: file });
      const importId = classification.kind === 'hosted' && parsed && typeof parsed === 'object' && 'importId' in parsed && typeof parsed.importId === 'string'
        ? parsed.importId
        : crypto.randomUUID();
      const started = await startImport(importId, upload.key);
      let job = await getImportJob(started.jobId);
      for (let attempt = 0; (job.status === 'pending' || job.status === 'running') && attempt < 10; attempt += 1) {
        await new Promise((resolve) => window.setTimeout(resolve, 200));
        job = await getImportJob(started.jobId);
      }
      setNotice(job.status === 'ready' ? 'Import finished.' : 'Import in progress.');
    } catch (requestError) {
      setError(hosted(requestError, hostedErrorSentence('household_not_empty')));
    } finally {
      setImporting(false);
    }
  };

  const removeHousehold = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    if (household === null) return;
    setError('');
    try {
      const deleted = await deleteHousehold(household.id, deleteName);
      setHousehold(deleted);
      setNotice(deleted.message);
    } catch (requestError) {
      setError(hosted(requestError, hostedErrorSentence('confirmation_mismatch')));
    }
  };

  const removeAccount = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    setError('');
    try {
      await deleteAccount(accountEmail);
      logout();
    } catch (requestError) {
      setError(hosted(requestError, hostedErrorSentence('last_owner')));
    }
  };

  const startAnother = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    setError('');
    try {
      await createHousehold({ name: startName.trim(), restockMode, inviteCode: startCode.trim() });
      await getSession();
      const session = currentAuthSession();
      if (session) replaceAuth(session);
      setNotice(`${startName.trim()} was created.`);
    } catch (requestError) {
      setError(hosted(requestError, hostedErrorSentence('invite_required')));
    }
  };

  return (
    <Stack className="household-page" gap="lg">
      <Title order={2}>{household?.name ?? active?.name ?? 'Household'}</Title>
      {settings && <Text>Restock mode: {settings.restockMode.replaceAll('_', ' ')}</Text>}
      {error !== '' && <Alert color="red" variant="light" role="alert">{error}</Alert>}
      {notice !== '' && <Alert color="teal" variant="light" role="status">{notice}</Alert>}

      <section aria-labelledby="members-heading">
        <Title id="members-heading" order={3}>Members</Title>
        <ul className="household-list">
          {members.map((member) => (
            <li key={member.userId}>
              <span>{member.email}</span>
              <span>{member.role === 'owner' ? 'Owner' : 'Member'}</span>
              {owner && (
                <Group gap="xs">
                  <Button
                    size="compact-sm"
                    variant="default"
                    onClick={() => {
                      void changeMemberRole(householdId, member.userId, member.role === 'owner' ? 'member' : 'owner')
                        .then(load)
                        .catch((requestError: unknown) => setError(hosted(requestError, hostedErrorSentence('last_owner'))));
                    }}
                  >
                    {member.role === 'owner' ? 'Make member' : 'Make owner'}
                  </Button>
                  <Button
                    size="compact-sm"
                    variant="default"
                    onClick={() => {
                      void removeMember(householdId, member.userId)
                        .then(load)
                        .catch((requestError: unknown) => setError(hosted(requestError, hostedErrorSentence('last_owner'))));
                    }}
                  >
                    Remove
                  </Button>
                  <Button
                    size="compact-sm"
                    variant="subtle"
                    onClick={() => {
                      void createResetLink(householdId, member.userId).then((link) => setCreatedUrl(link.url));
                    }}
                  >
                    Reset link
                  </Button>
                </Group>
              )}
            </li>
          ))}
        </ul>
      </section>

      <section aria-labelledby="invites-heading">
        <Title id="invites-heading" order={3}>Invites</Title>
        <form aria-label="Create invite" onSubmit={(event) => { void create(event); }}>
          <Group align="flex-end">
            {owner && (
              <Radio.Group label="Role" value={inviteRole} onChange={(value) => setInviteRole(value as 'member' | 'owner')}>
                <Group mt="xs">
                  <Radio value="member" label="Member" />
                  <Radio value="owner" label="Owner" />
                </Group>
              </Radio.Group>
            )}
            <Button type="submit">Create invite</Button>
          </Group>
        </form>
        {createdUrl !== '' && <Text role="status">Invite link: {createdUrl}</Text>}
        <ul className="household-list">
          {invites.filter((invite) => invite.status === 'open' || invite.status === 'revoked').map((invite) => (
            <li key={invite.id}>
              <span>{invite.email ?? 'Anyone with the link'}</span>
              <span>{invite.role === 'owner' ? 'Owner' : 'Member'}</span>
              <span>{inviteLabel(invite)}</span>
              {invite.status === 'open' && (
                <Button size="compact-sm" variant="default" onClick={() => { void revokeInvite(invite.id).then(load); }}>
                  Revoke invite
                </Button>
              )}
            </li>
          ))}
        </ul>
      </section>

      {owner && (
        <section aria-labelledby="export-heading">
          <Title id="export-heading" order={3}>Export and delete</Title>
          <Text>{HOUSEHOLD_DELETE_SENTENCE}</Text>
          <Group>
            <Button loading={exporting} onClick={() => { void exportHousehold(); }}>Export household</Button>
          </Group>
          {exportLinks?.jsonUrl && <a href={exportLinks.jsonUrl}>Download JSON</a>}
          {exportLinks?.csvUrl && <a href={exportLinks.csvUrl}>Download CSV</a>}
          <label className="household-import">
            Import into an empty household
            <input
              aria-label="Import file"
              type="file"
              accept="application/json"
              disabled={importing}
              onChange={(event) => {
                const file = event.currentTarget.files?.[0];
                if (file) void importFile(file);
              }}
            />
          </label>
          {household?.deletedAt ? (
            <Button onClick={() => { void restoreHousehold(household.id).then((restored) => { setHousehold(restored); setNotice('Household restored.'); }); }}>
              Restore household
            </Button>
          ) : (
            <form aria-label="Delete household" onSubmit={(event) => { void removeHousehold(event); }}>
              <TextInput
                label={`Type ${household?.name ?? 'the household name'} to delete`}
                value={deleteName}
                onChange={(event) => setDeleteName(event.currentTarget.value)}
              />
              <Button type="submit" color="red" mt="sm">Delete household</Button>
            </form>
          )}
        </section>
      )}

      <section aria-labelledby="account-heading">
        <Title id="account-heading" order={3}>Your account</Title>
        <form aria-label="Delete account" onSubmit={(event) => { void removeAccount(event); }}>
          <TextInput
            label={`Type ${auth?.user?.email ?? 'your email'} to delete the account`}
            value={accountEmail}
            onChange={(event) => setAccountEmail(event.currentTarget.value)}
          />
          <Button type="submit" color="red" mt="sm">Delete account</Button>
        </form>
      </section>

      <section aria-labelledby="another-heading">
        <Title id="another-heading" order={3}>Another household</Title>
        <form aria-label="Start a household" onSubmit={(event) => { void startAnother(event); }}>
          <Stack gap="sm">
            <TextInput label="Invite code" value={startCode} onChange={(event) => setStartCode(event.currentTarget.value)} required />
            <TextInput label="Household name" value={startName} onChange={(event) => setStartName(event.currentTarget.value)} required />
            <Radio.Group label="When a new item shows up" value={restockMode} onChange={(value) => setRestockMode(value as RestockMode)}>
              <Stack gap="xs" mt="xs">
                {RESTOCK.map((choice) => <Radio key={choice.value} value={choice.value} label={choice.label} />)}
              </Stack>
            </Radio.Group>
            <Button type="submit">Create household</Button>
            {auth?.operator && (
              <Button type="button" variant="default" onClick={() => { void createOperatorHouseholdInvite().then((invite) => setCreatedUrl(invite.url)); }}>
                Create a start-household invite
              </Button>
            )}
          </Stack>
        </form>
      </section>
    </Stack>
  );
}
