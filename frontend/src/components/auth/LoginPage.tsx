import { useState, type FormEvent } from 'react';
import { Alert, Button, PasswordInput, Stack, Text, TextInput, Title } from '@mantine/core';
import { login } from '../../api/client';
import { inlineFailure } from '../../api/contract';
import './LoginPage.css';

export interface LoginPageProps {
  onSignedIn: () => void;
  initialError?: string;
}

export const LoginPage = ({ onSignedIn, initialError = '' }: LoginPageProps) => {
  const [username, setUsername] = useState('');
  const [password, setPassword] = useState('');
  const [error, setError] = useState(initialError);
  const [pending, setPending] = useState(false);

  const submit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    setPending(true);
    setError('');
    try {
      await login(username, password);
      onSignedIn();
    } catch (requestError) {
      setError(inlineFailure(requestError, 'The username or password is incorrect.') ?? '');
    } finally {
      setPending(false);
    }
  };

  return (
    <main className="login-screen">
      <section className="login-card" aria-labelledby="login-title">
        <img className="login-logo" src="/brand/logo.png" alt="" />
        <Title id="login-title" className="login-brand" order={1} size="h2">Stockbot</Title>
        <Text className="login-lede">Sign in with the household username and password.</Text>
        <form className="login-form" aria-label="Sign in" onSubmit={(event) => { void submit(event); }}>
          <Stack gap="sm">
            {error !== '' && <Alert color="red" variant="light" role="alert">{error}</Alert>}
            <TextInput
              label="Username"
              name="username"
              autoComplete="username"
              autoCapitalize="none"
              autoCorrect="off"
              spellCheck={false}
              size="lg"
              value={username}
              onChange={(event) => setUsername(event.currentTarget.value)}
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
            <Button className="login-submit" type="submit" size="lg" fullWidth loading={pending}>
              Sign in
            </Button>
          </Stack>
        </form>
      </section>
    </main>
  );
};
