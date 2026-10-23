import { createContext, use, useCallback, useEffect, useMemo, useState, type ReactNode } from 'react';
import { CODE_AUTH_NOT_CONFIGURED, inlineFailure, raiseOfflineBanner, ServerNoticeError, subscribeApiSuccess, UnreachableError } from '../../frontend/src/api/contract';
import { ApiError, createStockbotClient, type StockbotClient } from './api/client';
import { clearCredentials, loadCredentials, normalizeServerUrl, saveCredentials } from './auth/credentials';

type SessionState =
  | { status: 'loading' }
  | { status: 'signedOut'; error: string }
  | { status: 'setup'; serverUrl: string; password: string; saved: boolean }
  | { status: 'ready'; serverUrl: string; password: string; client: StockbotClient; warning: string };

type SessionContextValue = SessionState & {
  signIn: (serverUrl: string, password: string) => Promise<void>;
  signOut: (error?: string) => Promise<void>;
  recheck: () => Promise<void>;
};

const SessionContext = createContext<SessionContextValue | null>(null);

export function SessionProvider({ children }: { children: ReactNode }) {
  const [state, setState] = useState<SessionState>({ status: 'loading' });

  useEffect(() => subscribeApiSuccess(() => {
    setState((current) => {
      if (current.status !== 'ready' || current.warning === '') return current;
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
    try {
      const session = await client.getSession();
      if (session.error !== '') {
        throw new Error(session.error);
      }
      await saveCredentials({ serverUrl: normalized, password });
      setState({ status: 'ready', serverUrl: normalized, password, client, warning: '' });
    } catch (error) {
      if (error instanceof ServerNoticeError && error.code === CODE_AUTH_NOT_CONFIGURED) {
        setState({ status: 'setup', serverUrl: normalized, password, saved: false });
      }
      throw error;
    }
  }, [signOut]);

  const recheck = useCallback(async () => {
    if (state.status !== 'setup') return;
    const { serverUrl, password, saved } = state;
    const client = createStockbotClient(
      { serverUrl, password },
      { onUnauthorized: () => { void signOut('That password was rejected.'); } },
    );
    try {
      const session = await client.getSession();
      if (session.error !== '') {
        setState({ status: 'signedOut', error: session.error });
        return;
      }
      await saveCredentials({ serverUrl, password });
      setState({ status: 'ready', serverUrl, password, client, warning: '' });
    } catch (error) {
      if (error instanceof ServerNoticeError && error.code === CODE_AUTH_NOT_CONFIGURED) return;
      if (error instanceof UnreachableError) {
        if (saved) {
          setState({ status: 'ready', serverUrl, password, client, warning: '' });
        } else {
          setState({ status: 'signedOut', error: '' });
        }
        return;
      }
      if (error instanceof ApiError && (error.status === 401 || error.status === 403)) return;
      setState({ status: 'signedOut', error: inlineFailure(error, 'Sign-in is unavailable right now.') ?? '' });
    }
  }, [state, signOut]);

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
          if (error instanceof ServerNoticeError && error.code === CODE_AUTH_NOT_CONFIGURED) {
            setState({ status: 'setup', serverUrl: saved.serverUrl, password: saved.password, saved: true });
            return;
          }
          if (error instanceof ApiError && (error.status === 401 || error.status === 403)) {
            await clearCredentials();
            if (!active) return;
            setState({ status: 'signedOut', error: inlineFailure(error, 'That password was rejected.') ?? '' });
            return;
          }
          if (!(error instanceof Error)) raiseOfflineBanner();
          setState({
            status: 'ready',
            serverUrl: saved.serverUrl,
            password: saved.password,
            client,
            warning: inlineFailure(error, '') ?? '',
          });
        }
      } catch (error) {
        if (!active) return;
        setState({
          status: 'signedOut',
          error: inlineFailure(error, 'Saved sign-in could not be read.') ?? '',
        });
      }
    })();
    return () => {
      active = false;
    };
  }, [signOut]);

  const value = useMemo(() => ({ ...state, signIn, signOut, recheck }), [state, signIn, signOut, recheck]);
  return <SessionContext.Provider value={value}>{children}</SessionContext.Provider>;
}

export function useSession(): SessionContextValue {
  const value = use(SessionContext);
  if (value === null) {
    throw new Error('useSession must be used inside SessionProvider');
  }
  return value;
}
