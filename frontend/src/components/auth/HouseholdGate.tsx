import { useEffect, useState, type ReactNode } from 'react';
import { OFFLINE_SENTENCE, UnreachableError } from '../../api/contract';
import { getSession, logout, setUnauthorizedHandler } from '../../api/client';
import { HouseholdSessionContext } from './householdSession';
import { LoginPage } from './LoginPage';
import './LoginPage.css';

export const HouseholdGate = ({ children }: { children: ReactNode }) => {
  const [phase, setPhase] = useState<'checking' | 'login' | 'app'>('checking');
  const [required, setRequired] = useState(false);
  const [error, setError] = useState('');

  useEffect(() => {
    let cancelled = false;
    void getSession().then((session) => {
      if (cancelled) return;
      setRequired(session.required);
      if (!session.required || session.username !== '') {
        setPhase('app');
        return;
      }
      setError(session.error);
      setPhase('login');
    }).catch((error: unknown) => {
      if (cancelled) return;
      setRequired(true);
      setError(error instanceof UnreachableError ? OFFLINE_SENTENCE : 'Stockbot could not be reached.');
      setPhase('login');
    });
    return () => {
      cancelled = true;
    };
  }, []);

  useEffect(() => {
    if (phase !== 'app' || !required) {
      setUnauthorizedHandler(null);
      return undefined;
    }
    setUnauthorizedHandler(() => setPhase('login'));
    return () => setUnauthorizedHandler(null);
  }, [phase, required]);

  const signOut = () => {
    void logout().finally(() => setPhase('login'));
  };

  if (phase === 'checking') {
    return (
      <main className="login-screen" aria-busy="true">
        <p className="login-brand">Stockbot</p>
      </main>
    );
  }

  if (phase === 'login') {
    return (
      <LoginPage
        initialError={error}
        onSignedIn={() => {
          setRequired(true);
          setError('');
          setPhase('app');
        }}
      />
    );
  }

  return (
    <HouseholdSessionContext.Provider value={{ required, logout: signOut }}>
      {children}
    </HouseholdSessionContext.Provider>
  );
};
