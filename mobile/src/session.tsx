import { createContext, use, useCallback, useEffect, useMemo, useState, type ReactNode } from 'react';
import { OFFLINE_SENTENCE, raiseOfflineBanner, subscribeApiSuccess } from '../../frontend/src/api/contract';
import { ApiError, createStockbotClient, type StockbotClient } from './api/client';
import { clearCredentials, loadCredentials, normalizeServerUrl, saveCredentials } from './auth/credentials';

type SessionState =
  | { status: 'loading' }
  | { status: 'signedOut'; error: string }
  | { status: 'ready'; serverUrl: string; password: string; client: StockbotClient; warning: string };

type SessionContextValue = SessionState & {
  signIn: (serverUrl: string, password: string) => Promise<void>;
  signOut: (error?: string) => Promise<void>;
};

const SessionContext = createContext<SessionContextValue | null>(null);

export function SessionProvider({ children }: { children: ReactNode }) {
  const [state, setState] = useState<SessionState>({ status: 'loading' });

  useEffect(() => subscribeApiSuccess(() => {
    setState((current) => {
      if (current.status !== 'ready' || current.warning !== OFFLINE_SENTENCE) return current;
      return { ...current, warning: '' };
    });
  }), []);

  const signOut = useCallback(async (error = '') => {
    await clearCredentials();
    setState({ status: 'signedOut', error });
  }, []);

  const signIn = useCallback(async (serverUrl: string, password: string) => {
    const normalized = normalizeServerUrl(serverUrl);
    const client = createStockbotClient(
      { serverUrl: normalized, password },
      { onUnauthorized: () => { void signOut('That password was rejected.'); } },
    );
    const session = await client.getSession();
    if (session.error !== '') {
      throw new Error(session.error);
    }
    await saveCredentials({ serverUrl: normalized, password });
    setState({ status: 'ready', serverUrl: normalized, password, client, warning: '' });
  }, [signOut]);

  useEffect(() => {
    let active = true;
    void (async () => {
      try {
        const saved = await loadCredentials();
        if (!active) return;
        if (saved === null) {
          setState({ status: 'signedOut', error: '' });
          return;
        }
        const client = createStockbotClient(saved, {
          onUnauthorized: () => { void signOut('That password was rejected.'); },
        });
        try {
          const session = await client.getSession();
          if (!active) return;
          if (session.error !== '') {
            await clearCredentials();
            if (!active) return;
            setState({ status: 'signedOut', error: session.error });
            return;
          }
          setState({ status: 'ready', serverUrl: saved.serverUrl, password: saved.password, client, warning: '' });
        } catch (error) {
          if (!active) return;
          if (error instanceof ApiError && (error.status === 401 || error.status === 403)) {
            await clearCredentials();
            if (!active) return;
            setState({ status: 'signedOut', error: error.message });
            return;
          }
          if (!(error instanceof Error) || error.message === OFFLINE_SENTENCE) {
            if (!(error instanceof Error)) raiseOfflineBanner();
            setState({
              status: 'ready',
              serverUrl: saved.serverUrl,
              password: saved.password,
              client,
              warning: '',
            });
            return;
          }
          setState({
            status: 'ready',
            serverUrl: saved.serverUrl,
            password: saved.password,
            client,
            warning: error.message,
          });
        }
      } catch (error) {
        if (!active) return;
        setState({
          status: 'signedOut',
          error: error instanceof Error ? error.message : 'Saved sign-in could not be read.',
        });
      }
    })();
    return () => {
      active = false;
    };
  }, [signOut]);

  const value = useMemo(() => ({ ...state, signIn, signOut }), [state, signIn, signOut]);
  return <SessionContext.Provider value={value}>{children}</SessionContext.Provider>;
}

export function useSession(): SessionContextValue {
  const value = use(SessionContext);
  if (value === null) {
    throw new Error('useSession must be used inside SessionProvider');
  }
  return value;
}
