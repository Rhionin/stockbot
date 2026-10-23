import { useEffect, useRef, useState, type ReactNode } from 'react';
import { AUTH_NOT_CONFIGURED_SENTENCE, AUTH_SETUP_SENTENCE, serverNoticeText, UnreachableError } from '../../api/contract';
import { getSession, logout, setUnauthorizedHandler } from '../../api/client';
import { ServerNoticeBanner } from '../shell/ServerNoticeBanner';
import { HouseholdSessionContext } from './householdSession';
import { LoginPage } from './LoginPage';
import './LoginPage.css';

const RETRY_START_MS = 1000;
const RETRY_CAP_MS = 30_000;

export const HouseholdGate = ({ children }: { children: ReactNode }) => {
  const [phase, setPhase] = useState<'checking' | 'offline' | 'login' | 'setup' | 'app'>('checking');
  const [required, setRequired] = useState(false);
  const [error, setError] = useState('');
  const recheckRef = useRef<(() => void) | null>(null);

  useEffect(() => {
    let cancelled = false;
    let timer = 0;
    let delay = RETRY_START_MS;

    const applySession = (session: Awaited<ReturnType<typeof getSession>>) => {
      setRequired(session.required);
      if (serverNoticeText() === AUTH_NOT_CONFIGURED_SENTENCE && session.username === '') {
        setError('');
        setPhase('setup');
        return;
      }
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
        delay = RETRY_START_MS;
        applySession(session);
      }).catch((error: unknown) => {
        if (cancelled) return;
        if (error instanceof UnreachableError) {
          setError('');
          setPhase('offline');
          const wait = delay;
          delay = Math.min(delay * 2, RETRY_CAP_MS);
          timer = window.setTimeout(attempt, wait);
          return;
        }
        setRequired(true);
        setError('Stockbot could not be reached.');
        setPhase('login');
      });
    };

    recheckRef.current = () => {
      window.clearTimeout(timer);
      attempt();
    };
    attempt();
    return () => {
      cancelled = true;
      window.clearTimeout(timer);
      recheckRef.current = null;
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
      <main className={phase === 'offline' ? 'login-screen login-screen--offline' : 'login-screen'} aria-busy="true">
        {phase === 'offline' && <ServerNoticeBanner />}
        <p className="login-brand">Stockbot</p>
      </main>
    );
  }

  if (phase === 'setup') {
    return (
      <main className="login-screen login-screen--offline">
        <ServerNoticeBanner />
        <p className="login-setup-sentence">{AUTH_SETUP_SENTENCE}</p>
        <button type="button" className="login-setup-retry" onClick={() => { recheckRef.current?.(); }}>
          Try again
        </button>
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
          onNotConfigured={() => {
            setError('');
            setPhase('setup');
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
