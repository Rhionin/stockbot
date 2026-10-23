import { useEffect, useState, type ReactNode } from 'react';
import { UnreachableError } from '../../api/contract';
import { getSession, logout, setUnauthorizedHandler } from '../../api/client';
import { ServerNoticeBanner } from '../shell/ServerNoticeBanner';
import { HouseholdSessionContext } from './householdSession';
import { LoginPage } from './LoginPage';
import './LoginPage.css';

const RETRY_MS = 1000;

export const HouseholdGate = ({ children }: { children: ReactNode }) => {
  const [phase, setPhase] = useState<'checking' | 'offline' | 'login' | 'app'>('checking');
  const [required, setRequired] = useState(false);
  const [error, setError] = useState('');

  useEffect(() => {
    let cancelled = false;
    let timer = 0;

    const applySession = (session: Awaited<ReturnType<typeof getSession>>) => {
      setRequired(session.required);
      if (!session.required || session.username !== '') {
        setError('');
        setPhase('app');
        return;
      }
      setError(session.error);
      setPhase('login');
    };

    const attempt = () => {
      void getSession().then((session) => {
        if (cancelled) return;
        applySession(session);
      }).catch((error: unknown) => {
        if (cancelled) return;
        if (error instanceof UnreachableError) {
          setError('');
          setPhase('offline');
          timer = window.setTimeout(attempt, RETRY_MS);
          return;
        }
        setRequired(true);
        setError('Stockbot could not be reached.');
        setPhase('login');
      });
    };

    attempt();
    return () => {
      cancelled = true;
      window.clearTimeout(timer);
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

  if (phase === 'checking' || phase === 'offline') {
    return (
      <main className="login-screen" aria-busy="true">
        {phase === 'offline' && <ServerNoticeBanner />}
        <p className="login-brand">Stockbot</p>
      </main>
    );
  }

  if (phase === 'login') {
    return (
      <>
        <ServerNoticeBanner />
        <LoginPage
          initialError={error}
          onSignedIn={() => {
            setRequired(true);
            setError('');
            setPhase('app');
          }}
        />
      </>
    );
  }

  return (
    <HouseholdSessionContext.Provider value={{ required, logout: signOut }}>
      {children}
    </HouseholdSessionContext.Provider>
  );
};
