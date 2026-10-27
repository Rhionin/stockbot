import { useCallback, useEffect, useState, type FormEvent } from 'react';
import { Alert, Button, Group, Radio, Stack, Text, TextInput, Title } from '@mantine/core';
import { useNavigate } from 'react-router-dom';
import {
  HOUSEHOLD_DELETE_SENTENCE,
  classifyImportDocument,
  hostedErrorSentence,
  type Household,
  type HouseholdSettings,
  type InviteSummary,
  type Member,
} from '@stockbot/shared/contract';
import {
  ApiError,
  changeMemberRole,
  createInvite,
  createResetLink,
  deleteAccount,
  deleteHousehold,
  getExportJob,
  getHousehold,
  getHouseholdSettings,
  getImportJob,
  currentAuthSession,
  getSession,
  listInvites,
  listMembers,
  removeMember,
  renameHousehold,
  restoreHousehold,
  revokeInvite,
  startExport,
  startImport,
  createImportUpload,
} from '../../api/client';
import { useHouseholdSession } from '../auth/householdSession';

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

function copyLink(url: string) {
  void navigator.clipboard?.writeText(url);
}

type MemberBusy = { id: string; action: 'role' | 'remove' | 'reset' } | null;

function memberActionError(error: unknown): string {
  if (error instanceof ApiError && error.code === 'last_owner') {
    return hostedErrorSentence('last_owner');
  }
  return error instanceof Error ? error.message : 'Something went wrong.';
}

export function HouseholdPage() {
  const navigate = useNavigate();
  const { auth, logout, replaceAuth } = useHouseholdSession();
  const active = auth?.households.find((row) => row.id === auth.activeHouseholdId) ?? null;
  const isOwner = active?.role === 'owner';
  const owner = isOwner;
  const householdId = auth?.activeHouseholdId ?? '';
  const [members, setMembers] = useState<Member[]>([]);
  const [invites, setInvites] = useState<InviteSummary[]>([]);
  const [settings, setSettings] = useState<HouseholdSettings | null>(null);
  const [household, setHousehold] = useState<Household | null>(null);
  const [loadError, setLoadError] = useState('');
  const [membersError, setMembersError] = useState('');
  const [memberResetUrls, setMemberResetUrls] = useState<Record<string, string>>({});
  const [memberBusy, setMemberBusy] = useState<MemberBusy>(null);
  const [inviteRole, setInviteRole] = useState<'member' | 'owner'>('member');
  const [createdInviteUrl, setCreatedInviteUrl] = useState('');
  const [inviteError, setInviteError] = useState('');
  const [inviteLoading, setInviteLoading] = useState(false);
  const [revokingInviteId, setRevokingInviteId] = useState('');
  const [exportLinks, setExportLinks] = useState<{ jsonUrl: string | null; csvUrl: string | null } | null>(null);
  const [exporting, setExporting] = useState(false);
  const [exportError, setExportError] = useState('');
  const [exportNotice, setExportNotice] = useState('');
  const [importing, setImporting] = useState(false);
  const [deleteName, setDeleteName] = useState('');
  const [accountEmail, setAccountEmail] = useState('');
  const [accountError, setAccountError] = useState('');
  const [householdName, setHouseholdName] = useState('');
  const [renameLoading, setRenameLoading] = useState(false);
  const [renameError, setRenameError] = useState('');
  const [renameNotice, setRenameNotice] = useState('');

  useEffect(() => {
    const next = household?.name ?? active?.name ?? '';
    setHouseholdName(next);
  }, [household?.name, active?.name]);

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
      setLoadError('');
    } catch (requestError) {
      setLoadError(hosted(requestError, 'Stockbot could not load this household.'));
    }
  }, [householdId]);

  useEffect(() => {
    void Promise.resolve().then(load);
  }, [load]);

  const create = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    setInviteError('');
    setInviteLoading(true);
    try {
      const created = await createInvite(owner && inviteRole === 'owner' ? { role: 'owner' } : {});
      setCreatedInviteUrl(created.url);
      await load();
    } catch (requestError) {
      setInviteError(hosted(requestError, hostedErrorSentence('forbidden_role')));
    } finally {
      setInviteLoading(false);
    }
  };

  const exportHousehold = async () => {
    setExporting(true);
    setExportError('');
    setExportNotice('');
    try {
      const created = await startExport();
      let job = await getExportJob(created.jobId);
      for (let attempt = 0; job.status === 'pending' && attempt < 10; attempt += 1) {
        await new Promise((resolve) => window.setTimeout(resolve, 200));
        job = await getExportJob(created.jobId);
      }
      if (job.status === 'failed') {
        setExportError('The export failed. Try again.');
      } else {
        setExportLinks({ jsonUrl: job.jsonUrl, csvUrl: job.csvUrl });
        setExportNotice(job.status === 'ready' ? 'Export ready.' : 'Export in progress.');
      }
    } catch (requestError) {
      setExportError(hosted(requestError, hostedErrorSentence('forbidden_role')));
    } finally {
      setExporting(false);
    }
  };

  const importFile = async (file: File) => {
    setImporting(true);
    setExportError('');
    setExportNotice('');
    try {
      const text = await file.text();
      const parsed = JSON.parse(text) as unknown;
      const classification = classifyImportDocument(parsed);
      if (!classification.ok) {
        setExportError(hostedErrorSentence('validation'));
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
      setExportNotice(job.status === 'ready' ? 'Import finished.' : 'Import in progress.');
    } catch (requestError) {
      setExportError(hosted(requestError, hostedErrorSentence('household_not_empty')));
    } finally {
      setImporting(false);
    }
  };

  const removeHousehold = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    if (household === null) return;
    setExportError('');
    setExportNotice('');
    try {
      const deleted = await deleteHousehold(household.id, deleteName);
      setHousehold(deleted);
      setExportNotice(deleted.message);
    } catch (requestError) {
      setExportError(hosted(requestError, hostedErrorSentence('confirmation_mismatch')));
    }
  };

  const removeAccount = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    setAccountError('');
    try {
      await deleteAccount(accountEmail);
      logout();
    } catch (requestError) {
      setAccountError(hosted(requestError, hostedErrorSentence('last_owner')));
    }
  };

  const runMemberAction = async (userId: string, action: 'role' | 'remove', fn: () => Promise<void>) => {
    setMembersError('');
    setMemberBusy({ id: userId, action });
    try {
      await fn();
      await load();
    } catch (requestError) {
      setMembersError(memberActionError(requestError));
    } finally {
      setMemberBusy(null);
    }
  };

  const resetMemberLink = async (member: Member) => {
    setMembersError('');
    setMemberBusy({ id: member.userId, action: 'reset' });
    try {
      const link = await createResetLink(householdId, member.userId);
      setMemberResetUrls((current) => ({ ...current, [member.userId]: link.url }));
    } catch (requestError) {
      setMembersError(memberActionError(requestError));
    } finally {
      setMemberBusy(null);
    }
  };

  const memberRowBusy = (userId: string) => memberBusy?.id === userId;
  const memberActionLoading = (userId: string, action: 'role' | 'remove' | 'reset') =>
    memberBusy?.id === userId && memberBusy.action === action;

  const saveHouseholdName = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    if (!isOwner || householdId === '') return;
    setRenameError('');
    setRenameNotice('');
    setRenameLoading(true);
    try {
      const trimmed = householdName.trim();
      const updated = await renameHousehold(householdId, trimmed);
      setHousehold(updated);
      await getSession();
      const session = currentAuthSession();
      if (session) replaceAuth(session);
      setHouseholdName(updated.name);
      setRenameNotice('Household name saved.');
    } catch (requestError) {
      setRenameError(hosted(requestError, hostedErrorSentence('invalid_input')));
    } finally {
      setRenameLoading(false);
    }
  };

  const revoke = async (inviteId: string) => {
    setInviteError('');
    setRevokingInviteId(inviteId);
    try {
      await revokeInvite(inviteId);
      await load();
    } catch (requestError) {
      setInviteError(hosted(requestError, hostedErrorSentence('forbidden_role')));
    } finally {
      setRevokingInviteId('');
    }
  };

  return (
    <Stack className="household-page" gap="lg">
      <Title order={2}>{household?.name ?? active?.name ?? 'Household'}</Title>
      <section aria-labelledby="household-name-heading">
        <Title id="household-name-heading" order={3}>Household name</Title>
        {isOwner ? (
          <form aria-label="Rename household" onSubmit={(event) => { void saveHouseholdName(event); }}>
            <Group align="flex-end" wrap="wrap" mt="sm">
              <TextInput
                label="Household name"
                aria-label="Household name"
                value={householdName}
                onChange={(event) => setHouseholdName(event.currentTarget.value)}
                disabled={renameLoading}
                style={{ flex: 1, minWidth: '12rem' }}
              />
              <Button type="submit" loading={renameLoading}>Save name</Button>
              {renameNotice !== '' && <Text role="status">{renameNotice}</Text>}
              {renameError !== '' && <Text role="alert" c="red">{renameError}</Text>}
            </Group>
          </form>
        ) : (
          <Text mt="sm">{household?.name ?? active?.name ?? 'Household'}</Text>
        )}
      </section>
      {settings && <Text>Restock mode: {settings.restockMode.replaceAll('_', ' ')}</Text>}

      <section aria-labelledby="members-heading">
        <Title id="members-heading" order={3}>Members</Title>
        {loadError !== '' && <Alert color="red" variant="light" role="alert">{loadError}</Alert>}
        {membersError !== '' && <Alert color="red" variant="light" role="alert">{membersError}</Alert>}
        <ul className="household-list">
          {members.map((member) => (
            <li key={member.userId}>
              <span>{member.email}</span>
              <span>{member.role === 'owner' ? 'Owner' : 'Member'}</span>
              {memberResetUrls[member.userId] && (
                <Group gap="xs" wrap="wrap">
                  <Text role="status">Reset link for {member.email}: {memberResetUrls[member.userId]}</Text>
                  <Button size="compact-sm" variant="default" onClick={() => copyLink(memberResetUrls[member.userId] ?? '')}>
                    Copy link
                  </Button>
                </Group>
              )}
              {owner && (
                <Group gap="xs">
                  <Button
                    size="compact-sm"
                    variant="default"
                    disabled={memberRowBusy(member.userId)}
                    loading={memberActionLoading(member.userId, 'role')}
                    onClick={() => {
                      void runMemberAction(member.userId, 'role', () =>
                        changeMemberRole(householdId, member.userId, member.role === 'owner' ? 'member' : 'owner').then(() => undefined),
                      );
                    }}
                  >
                    {member.role === 'owner' ? 'Make member' : 'Make owner'}
                  </Button>
                  <Button
                    size="compact-sm"
                    variant="default"
                    disabled={memberRowBusy(member.userId)}
                    loading={memberActionLoading(member.userId, 'remove')}
                    onClick={() => {
                      void runMemberAction(member.userId, 'remove', () => removeMember(householdId, member.userId).then(() => undefined));
                    }}
                  >
                    Remove
                  </Button>
                  <Button
                    size="compact-sm"
                    variant="subtle"
                    disabled={memberRowBusy(member.userId)}
                    loading={memberActionLoading(member.userId, 'reset')}
                    onClick={() => { void resetMemberLink(member); }}
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
        {inviteError !== '' && <Alert color="red" variant="light" role="alert">{inviteError}</Alert>}
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
            <Button type="submit" loading={inviteLoading}>Create invite</Button>
          </Group>
        </form>
        {createdInviteUrl !== '' && (
          <Group gap="xs" mt="sm" wrap="wrap">
            <Text role="status">Invite link: {createdInviteUrl}</Text>
            <Button size="compact-sm" variant="default" onClick={() => copyLink(createdInviteUrl)}>
              Copy link
            </Button>
          </Group>
        )}
        <ul className="household-list">
          {invites.filter((invite) => invite.status === 'open' || invite.status === 'revoked').map((invite) => (
            <li key={invite.id}>
              <span>{invite.email ?? 'Anyone with the link'}</span>
              <span>{invite.role === 'owner' ? 'Owner' : 'Member'}</span>
              <span>{inviteLabel(invite)}</span>
              {invite.status === 'open' && (
                <Button
                  size="compact-sm"
                  variant="default"
                  loading={revokingInviteId === invite.id}
                  onClick={() => { void revoke(invite.id); }}
                >
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
          {exportError !== '' && <Alert color="red" variant="light" role="alert">{exportError}</Alert>}
          {exportNotice !== '' && <Alert color="teal" variant="light" role="status">{exportNotice}</Alert>}
          <Group>
            <Button loading={exporting} onClick={() => { void exportHousehold(); }}>Export household</Button>
          </Group>
          {(exportLinks?.jsonUrl || exportLinks?.csvUrl) && (
            <Group gap="md">
              {exportLinks?.jsonUrl && <a href={exportLinks.jsonUrl}>Download JSON</a>}
              {exportLinks?.csvUrl && <a href={exportLinks.csvUrl}>Download CSV</a>}
            </Group>
          )}
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
            <Button onClick={() => {
              void restoreHousehold(household.id)
                .then((restored) => {
                  setHousehold(restored);
                  setExportNotice('Household restored.');
                })
                .catch((requestError: unknown) => {
                  setExportError(hosted(requestError, 'Could not restore this household.'));
                });
            }}>
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
        {accountError !== '' && <Alert color="red" variant="light" role="alert">{accountError}</Alert>}
        <form aria-label="Delete account" onSubmit={(event) => { void removeAccount(event); }}>
          <TextInput
            label={`Type ${auth?.user?.email ?? 'your email'} to delete the account`}
            value={accountEmail}
            onChange={(event) => setAccountEmail(event.currentTarget.value)}
          />
          <Button type="submit" color="red" mt="sm">Delete account</Button>
        </form>
      </section>

      <section aria-labelledby="other-households-heading">
        <Title id="other-households-heading" order={3}>Other households</Title>
        <Text>Switch between households or start a new one.</Text>
        <Button variant="default" mt="sm" onClick={() => navigate('/households')}>
          Switch or add a household
        </Button>
      </section>
    </Stack>
  );
}
