import { useState, type FormEvent } from 'react';
import { useNavigate, useParams } from 'react-router-dom';
import { Alert, Button, PasswordInput, Stack, Text, Title } from '@mantine/core';
import { PASSWORD_TOO_SHORT, hostedErrorSentence, inlineFailure } from '@stockbot/shared/contract';
import { ApiError, resetPassword } from '../../api/client';

export const ResetPasswordPage = ({ code: codeProp }: { code?: string }) => {
  const params = useParams();
  const code = codeProp ?? params.code ?? '';
  const navigate = useNavigate();
  const [password, setPassword] = useState('');
  const [error, setError] = useState('');
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
      await resetPassword({ code, password });
      navigate('/');
    } catch (requestError) {
      const hosted = requestError instanceof ApiError ? requestError.code : '';
      setError(hosted !== '' ? hostedErrorSentence(hosted) : (inlineFailure(requestError, hostedErrorSentence('reset_expired')) ?? ''));
    } finally {
      setPending(false);
    }
  };

  return (
    <main className="login-screen">
      <section className="login-card" aria-labelledby="reset-title">
        <Title id="reset-title" order={1} size="h2">Choose a new password</Title>
        <Text className="login-lede">This link works once.</Text>
        <form aria-label="Reset password" onSubmit={(event) => { void submit(event); }}>
          <Stack gap="sm">
            {error !== '' && <Alert color="red" variant="light" role="alert">{error}</Alert>}
            <PasswordInput label="New password" value={password} onChange={(event) => setPassword(event.currentTarget.value)} required disabled={pending} />
            <Button type="submit" loading={pending}>Save password</Button>
          </Stack>
        </form>
      </section>
    </main>
  );
};
