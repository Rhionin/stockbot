import { useState, type FormEvent } from 'react';
import { Alert, Button, PasswordInput, Stack, Text, TextInput, Title } from '@mantine/core';
import {
  HOSTED_ERROR_SENTENCES,
  PASSWORD_TOO_SHORT,
  hostedErrorSentence,
  inlineFailure,
  type HostedErrorCode,
} from '@stockbot/shared/contract';
import { ApiError, loginAccount } from '../../api/client';

export interface AccountLoginPageProps {
  initialError?: string;
  onSignedIn: () => void;
}

function failureText(error: unknown): string {
  if (error instanceof ApiError && error.code in HOSTED_ERROR_SENTENCES) {
    return hostedErrorSentence(error.code as HostedErrorCode);
  }
  return inlineFailure(error, hostedErrorSentence('unauthorized')) ?? '';
}

export const AccountLoginPage = ({
  initialError = '',
  onSignedIn,
}: AccountLoginPageProps) => {
  const [email, setEmail] = useState('');
  const [password, setPassword] = useState('');
  const [error, setError] = useState(initialError);
  const [pending, setPending] = useState(false);

  const submitAccount = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    if (password.length < 10) {
      setError(PASSWORD_TOO_SHORT);
      return;
    }
    setPending(true);
    setError('');
    try {
      await loginAccount({ email: email.trim(), password });
      onSignedIn();
    } catch (requestError) {
      setError(failureText(requestError));
    } finally {
      setPending(false);
    }
  };

  return (
    <main className="login-screen">
      <section className="login-card" aria-labelledby="login-title">
        <img className="login-logo" src="/brand/logo.png" alt="" />
        <Title id="login-title" className="login-brand" order={1} size="h2">Stockbot</Title>
        <Text className="login-lede">Sign in with your email and password.</Text>
        <form className="login-form" aria-label="Sign in" onSubmit={(event) => { void submitAccount(event); }}>
          <Stack gap="sm">
            {error !== '' && <Alert color="red" variant="light" role="alert">{error}</Alert>}
            <TextInput
              label="Email"
              name="email"
              type="email"
              autoComplete="username"
              autoCapitalize="none"
              autoCorrect="off"
              spellCheck={false}
              size="lg"
              value={email}
              onChange={(event) => setEmail(event.currentTarget.value)}
              disabled={pending}
              required
            />
            <PasswordInput
              label="Password"
              name="password"
              autoComplete="current-password"
              size="lg"
              value={password}
              onChange={(event) => setPassword(event.currentTarget.value)}
              disabled={pending}
              required
            />
            <Button className="login-submit" type="submit" size="lg" fullWidth loading={pending}>Sign in</Button>
          </Stack>
        </form>
      </section>
    </main>
  );
};
