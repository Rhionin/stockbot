import { useState, type FormEvent } from 'react';
import { Alert, Button, PasswordInput, Stack, Text, TextInput, Title } from '@mantine/core';
import { PASSWORD_TOO_SHORT, hostedErrorSentence, inlineFailure } from '@stockbot/shared/contract';
import { ApiError, getLiveConfig, submitSetup } from '../../api/client';

export const SetupPage = () => {
  const [setupCode, setSetupCode] = useState('');
  const [name, setName] = useState('');
  const [email, setEmail] = useState('');
  const [password, setPassword] = useState('');
  const [recover, setRecover] = useState(false);
  const [error, setError] = useState('');
  const [ready, setReady] = useState('');
  const [pending, setPending] = useState(false);

  const submit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    if (password.length < 10) {
      setError(PASSWORD_TOO_SHORT);
      return;
    }
    setPending(true);
    setError('');
    try {
      await submitSetup(recover
        ? { setupCode: setupCode.trim(), email: email.trim(), newPassword: password }
        : { setupCode: setupCode.trim(), name: name.trim(), email: email.trim(), password });
      let next = '';
      try {
        const config = await getLiveConfig();
        next = config.appBaseUrl ?? '';
      } catch {
        next = '';
      }
      setReady(next === ''
        ? 'The account is ready. Sign-in is not set up on this server yet.'
        : `The account is ready. Sign in at ${next}.`);
    } catch (requestError) {
      const code = requestError instanceof ApiError ? requestError.code : '';
      setError(code !== '' ? hostedErrorSentence(code) : (inlineFailure(requestError, hostedErrorSentence('not_found')) ?? ''));
    } finally {
      setPending(false);
    }
  };

  return (
    <main className="login-screen">
      <section className="login-card" aria-labelledby="setup-title">
        <Title id="setup-title" order={1} size="h2">Set up Stockbot</Title>
        <Text className="login-lede">
          {recover
            ? 'Enter the recovery code from the server, then choose a new password.'
            : 'Enter the setup code from the server, then create the operator account.'}
        </Text>
        {ready !== '' ? <Text role="status">{ready}</Text> : (
          <form aria-label="Setup" onSubmit={(event) => { void submit(event); }}>
            <Stack gap="sm">
              {error !== '' && <Alert color="red" variant="light" role="alert">{error}</Alert>}
              <TextInput label="Setup code" value={setupCode} onChange={(event) => setSetupCode(event.currentTarget.value)} required disabled={pending} />
              {!recover && (
                <TextInput label="Name" value={name} onChange={(event) => setName(event.currentTarget.value)} required disabled={pending} />
              )}
              <TextInput label="Email" type="email" value={email} onChange={(event) => setEmail(event.currentTarget.value)} required disabled={pending} autoComplete="username" />
              <PasswordInput label={recover ? 'New password' : 'Password'} value={password} onChange={(event) => setPassword(event.currentTarget.value)} required disabled={pending} />
              <Button type="submit" loading={pending}>Save account</Button>
              <Button type="button" variant="subtle" onClick={() => setRecover((current) => !current)}>
                {recover ? 'This is a first account' : 'I already have an account'}
              </Button>
            </Stack>
          </form>
        )}
      </section>
    </main>
  );
};
