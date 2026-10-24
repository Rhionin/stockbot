import { createContext, use, useCallback, useEffect, useMemo, useRef, useState, type ReactNode } from 'react';
import { CODE_AUTH_NOT_CONFIGURED, CODE_UNAUTHORIZED, INVALID_CREDENTIALS_SENTENCE, SESSION_EXPIRED_SENTENCE, SIGN_IN_REQUIRED_SENTENCE, inlineFailure, raiseOfflineBanner, ServerNoticeError, subscribeApiSuccess, UnreachableError } from '@stockbot/shared/contract';
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

// A password the person just offered uses the wrong-credentials sentence.
// A 401 after the phone was already signed in is the session sentence.
function rejectedPassword(error: ApiError): ApiError {
  if (error.status !== 401) return error;
  if (error.code !== '' && error.code !== CODE_UNAUTHORIZED) return error;
  if (error.message === INVALID_CREDENTIALS_SENTENCE) return error;
  if (error.code !== CODE_UNAUTHORIZED && error.message !== SIGN_IN_REQUIRED_SENTENCE && error.message !== '') {
    return error;
  }
  return new ApiError(
    error.status,
    INVALID_CREDENTIALS_SENTENCE,
    error.code === '' ? CODE_UNAUTHORIZED : error.code,
    error.suggestions,
    error.itemId,
    error.onHand,
    error.provider,
    error.members,
  );
}

export function SessionProvider({ children }: { children: ReactNode }) {
  const [state, setState] = useState<SessionState>({ status: 'loading' });
  const readyRef = useRef(false);

  useEffect(() => subscribeApiSuccess(() => {
    setState((current) => {
      if (current.status !== 'ready' || current.warning === '') return current;
      return { ...current, warning: '' };
    });
  }), []);

  const signOut = useCallback(async (error = '') => {
    readyRef.current = false;
    await clearCredentials();
    setState({ status: 'signedOut', error });
  }, []);

  const onUnauthorized = useCallback(() => {
    void signOut(readyRef.current ? SESSION_EXPIRED_SENTENCE : INVALID_CREDENTIALS_SENTENCE);
  }, [signOut]);

  const signIn = useCallback(async (serverUrl: string, password: string) => {
    const normalized = normalizeServerUrl(serverUrl);
    const client = createStockbotClient(
      { serverUrl: normalized, password },
      { onUnauthorized },
    );
    try {
      const session = await client.getSession();
      if (session.error !== '') {
        throw new Error(session.error);
      }
      await saveCredentials({ serverUrl: normalized, password });
      readyRef.current = true;
      setState({ status: 'ready', serverUrl: normalized, password, client, warning: '' });
    } catch (error) {
      if (error instanceof ServerNoticeError && error.code === CODE_AUTH_NOT_CONFIGURED) {
        setState({ status: 'setup', serverUrl: normalized, password, saved: false });
      }
      if (error instanceof ApiError) throw rejectedPassword(error);
      throw error;
    }
  }, [onUnauthorized]);

  const recheck = useCallback(async () => {
    if (state.status !== 'setup') return;
    const { serverUrl, password, saved } = state;
    const client = createStockbotClient(
      { serverUrl, password },
      { onUnauthorized },
    );
    try {
      const session = await client.getSession();
      if (session.error !== '') {
        setState({ status: 'signedOut', error: session.error });
        return;
      }
      await saveCredentials({ serverUrl, password });
      readyRef.current = true;
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
  }, [state, onUnauthorized]);

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
        const client = createStockbotClient(saved, { onUnauthorized });
        try {
          const session = await client.getSession();
          if (!active) return;
          if (session.error !== '') {
            await clearCredentials();
            if (!active) return;
            setState({ status: 'signedOut', error: session.error });
            return;
          }
          readyRef.current = true;
          setState({ status: 'ready', serverUrl: saved.serverUrl, password: saved.password, client, warning: '' });
        } catch (error) {
          if (!active) return;
          if (error instanceof ServerNoticeError && error.code === CODE_AUTH_NOT_CONFIGURED) {
            setState({ status: 'setup', serverUrl: saved.serverUrl, password: saved.password, saved: true });
            return;
          }
          if (error instanceof ApiError && (error.status === 401 || error.status === 403)) {
            readyRef.current = false;
            await clearCredentials();
            if (!active) return;
            const rejected = error.status === 401 ? rejectedPassword(error) : error;
            setState({ status: 'signedOut', error: inlineFailure(rejected, INVALID_CREDENTIALS_SENTENCE) ?? '' });
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
  }, [onUnauthorized]);

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
