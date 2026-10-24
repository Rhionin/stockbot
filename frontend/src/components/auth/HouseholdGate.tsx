import { useEffect, useRef, useState, type ReactNode } from 'react';
import { AUTH_NOT_CONFIGURED_SENTENCE, AUTH_SETUP_SENTENCE, SESSION_EXPIRED_SENTENCE, serverNoticeText, UnreachableError, type AuthSession } from '@stockbot/shared/contract';
import { currentAuthSession, getLiveConfig, getSession, logout, setUnauthorizedHandler, stage1AuthActive } from '../../api/client';
import { ServerNoticeBanner } from '../shell/ServerNoticeBanner';
import { AccountLoginPage } from './AccountLoginPage';
import { HouseholdSessionContext } from './householdSession';
import { LoginPage } from './LoginPage';
import './LoginPage.css';

const RETRY_START_MS = 1000;
const RETRY_CAP_MS = 30_000;

export const HouseholdGate = ({ children }: { children: ReactNode }) => {
  const [phase, setPhase] = useState<'checking' | 'offline' | 'login' | 'account' | 'setup' | 'app'>('checking');
  const [required, setRequired] = useState(false);
  const [error, setError] = useState('');
  const [auth, setAuth] = useState<AuthSession | null>(null);
  const recheckRef = useRef<(() => void) | null>(null);

  useEffect(() => {
    let cancelled = false;
    let timer = 0;
    let delay = RETRY_START_MS;

    const applySession = (session: Awaited<ReturnType<typeof getSession>>) => {
      const next = currentAuthSession();
      setAuth(next);
      setRequired(session.required);
      if (serverNoticeText() === AUTH_NOT_CONFIGURED_SENTENCE && session.username === '') {
        setError('');
        setPhase('setup');
        return;
      }
      if (next?.kind === 'signed_out' || (stage1AuthActive() && next === null && session.required && session.username === '')) {
        setError(session.error);
        setPhase('account');
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
    setUnauthorizedHandler(() => {
      setError(SESSION_EXPIRED_SENTENCE);
      setAuth(null);
      setPhase(stage1AuthActive() ? 'account' : 'login');
    });
    return () => setUnauthorizedHandler(null);
  }, [phase, required]);

  const signOut = () => {
    const account = stage1AuthActive();
    void logout().finally(() => {
      setAuth(null);
      setPhase(account ? 'account' : 'login');
    });
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
        <ServerNoticeBanner inset />
        <LoginPage
          initialError={error}
          onSignedIn={() => {
            setRequired(true);
            setError('');
            setAuth(currentAuthSession());
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

  if (phase === 'account') {
    return (
      <AccountEntry
        initialError={error}
        onSignedIn={() => {
          setRequired(true);
          setError('');
          setAuth(currentAuthSession());
          setPhase('app');
        }}
      />
    );
  }

  return (
    <HouseholdSessionContext.Provider value={{
      required,
      logout: signOut,
      auth,
      replaceAuth: (session) => setAuth(session),
    }}>
      {children}
    </HouseholdSessionContext.Provider>
  );
};

function AccountEntry({ initialError, onSignedIn }: { initialError: string; onSignedIn: () => void }) {
  const [householdPasswordSignIn, setHouseholdPasswordSignIn] = useState<boolean | null>(null);
  const [entry, setEntry] = useState<'lan' | 'public' | ''>('');
  const [appBaseUrl, setAppBaseUrl] = useState('');

  useEffect(() => {
    let cancelled = false;
    void getLiveConfig().then((config) => {
      if (cancelled) return;
      setHouseholdPasswordSignIn(Boolean(config.householdPasswordSignIn));
      setEntry(config.entry ?? '');
      setAppBaseUrl(config.appBaseUrl ?? '');
    }).catch(() => {
      if (!cancelled) setHouseholdPasswordSignIn(false);
    });
    return () => {
      cancelled = true;
    };
  }, []);

  if (householdPasswordSignIn === null) {
    return (
      <main className="login-screen" aria-busy="true">
        <p className="login-brand">Stockbot</p>
      </main>
    );
  }

  if (entry === 'lan') {
    return (
      <main className="login-screen">
        <section className="login-card">
          <p className="login-lede">
            {appBaseUrl === ''
              ? 'Sign-in is not used on this address.'
              : `Sign in at ${appBaseUrl}.`}
          </p>
          {appBaseUrl !== '' && <a href={appBaseUrl}>Open {appBaseUrl}</a>}
        </section>
      </main>
    );
  }

  return (
    <>
      <ServerNoticeBanner inset />
      <AccountLoginPage
        householdPasswordSignIn={householdPasswordSignIn}
        initialError={initialError}
        onSignedIn={onSignedIn}
      />
    </>
  );
}
