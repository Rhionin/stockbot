import { useEffect, useState, type FormEvent } from 'react';
import { useNavigate, useParams } from 'react-router-dom';
import { Alert, Button, PasswordInput, Radio, Stack, Text, TextInput, Title } from '@mantine/core';
import {
  PASSWORD_TOO_SHORT,
  hostedErrorSentence,
  inlineFailure,
  type InvitePreview,
  type RestockMode,
} from '@stockbot/shared/contract';
import { ApiError, acceptInvite, currentAuthSession, getSession, previewInvite } from '../../api/client';

const RESTOCK: { value: RestockMode; label: string }[] = [
  { value: 'auto', label: 'Restock new items automatically' },
  { value: 'ask_restock', label: 'Ask me before restocking a new item' },
  { value: 'ask_hold', label: 'Ask me, and hold the item until I answer' },
];

export const InviteAcceptPage = ({ code: codeProp }: { code?: string }) => {
  const params = useParams();
  const code = codeProp ?? params.code ?? '';
  const navigate = useNavigate();
  const [preview, setPreview] = useState<InvitePreview | null>(null);
  const [error, setError] = useState('');
  const [signedIn, setSignedIn] = useState(false);
  const [name, setName] = useState('');
  const [email, setEmail] = useState('');
  const [password, setPassword] = useState('');
  const [householdName, setHouseholdName] = useState('');
  const [restockMode, setRestockMode] = useState<RestockMode>('auto');
  const [pending, setPending] = useState(false);

  useEffect(() => {
    let cancelled = false;
    void previewInvite(code).then((body) => {
      if (!cancelled) setPreview(body);
    }).catch((requestError: unknown) => {
      if (cancelled) return;
      const hosted = requestError instanceof ApiError ? requestError.code : '';
      setError(hosted !== '' ? hostedErrorSentence(hosted) : (inlineFailure(requestError, hostedErrorSentence('invite_expired')) ?? ''));
    });
    void getSession().then(() => {
      if (!cancelled) setSignedIn(currentAuthSession()?.kind === 'account');
    }).catch(() => undefined);
    return () => {
      cancelled = true;
    };
  }, [code]);

  const submit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    if (!signedIn && password.length < 10) {
      setError(PASSWORD_TOO_SHORT);
      return;
    }
    setPending(true);
    setError('');
    try {
      await acceptInvite(signedIn
        ? { code }
        : {
            code,
            account: { name: name.trim(), email: email.trim(), password },
            ...(preview?.kind === 'start_household'
              ? { household: { name: householdName.trim(), restockMode } }
              : {}),
          });
      navigate('/');
    } catch (requestError) {
      const hosted = requestError instanceof ApiError ? requestError.code : '';
      setError(hosted !== '' ? hostedErrorSentence(hosted) : (inlineFailure(requestError, hostedErrorSentence('invite_expired')) ?? ''));
    } finally {
      setPending(false);
    }
  };

  return (
    <main className="login-screen">
      <section className="login-card" aria-labelledby="invite-title">
        <Title id="invite-title" order={1} size="h2">
          {preview?.kind === 'start_household' ? 'Start a household' : 'Join a household'}
        </Title>
        {preview?.householdName && <Text className="login-lede">{preview.householdName}</Text>}
        {preview?.invitedBy && <Text>Invited by {preview.invitedBy}</Text>}
        {preview && <Text>Expires {new Date(preview.expiresAt).toLocaleString()}</Text>}
        {error !== '' && <Alert color="red" variant="light" role="alert">{error}</Alert>}
        {preview && (
          <form aria-label="Accept invite" onSubmit={(event) => { void submit(event); }}>
            <Stack gap="sm">
              {!signedIn && (
                <>
                  <TextInput label="Name" value={name} onChange={(event) => setName(event.currentTarget.value)} required disabled={pending} />
                  <TextInput label="Email" type="email" value={email} onChange={(event) => setEmail(event.currentTarget.value)} required disabled={pending} />
                  <PasswordInput label="Password" value={password} onChange={(event) => setPassword(event.currentTarget.value)} required disabled={pending} />
                </>
              )}
              {preview.kind === 'start_household' && !signedIn && (
                <>
                  <TextInput label="Household name" value={householdName} onChange={(event) => setHouseholdName(event.currentTarget.value)} required disabled={pending} />
                  <Radio.Group label="When a new item shows up" value={restockMode} onChange={(value) => setRestockMode(value as RestockMode)}>
                    <Stack gap="xs" mt="xs">
                      {RESTOCK.map((choice) => (
                        <Radio key={choice.value} value={choice.value} label={choice.label} />
                      ))}
                    </Stack>
                  </Radio.Group>
                </>
              )}
              <Button type="submit" loading={pending}>{preview.kind === 'start_household' ? 'Create household' : 'Join household'}</Button>
            </Stack>
          </form>
        )}
      </section>
    </main>
  );
};
