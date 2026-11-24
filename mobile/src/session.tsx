import { createContext, use, useCallback, useEffect, useMemo, useRef, useState, type ReactNode } from 'react';
import {
  CODE_AUTH_NOT_CONFIGURED,
  INVALID_CREDENTIALS_SENTENCE,
  PASSWORD_TOO_SHORT,
  SESSION_EXPIRED_SENTENCE,
  ServerNoticeError,
  UnreachableError,
  inlineFailure,
  raiseOfflineBanner,
  subscribeApiSuccess,
  type AuthSession,
} from '@stockbot/shared/contract';
import { APP_VERSION, isOlderVersion } from './appVersion';
import { ApiError, CODE_UPGRADE_REQUIRED, createStockbotClient, type StockbotClient } from './api/client';
import { DEFAULT_SERVER_URL, clearToken, loadSavedSession, normalizeServerUrl, saveCredentials } from './auth/credentials';

type SessionState =
  | { status: 'loading' }
  | { status: 'signedOut'; serverUrl: string; error: string }
  | { status: 'upgradeRequired' }
  | { status: 'setup'; serverUrl: string; token: string | null }
  | {
      status: 'ready';
      serverUrl: string;
      client: StockbotClient;
      account: AuthSession | null;
      scope: string;
      warning: string;
    };

type SessionContextValue = SessionState & {
  signIn: (serverUrl: string, email: string, password: string) => Promise<void>;
  signOut: () => Promise<void>;
  switchHousehold: (householdId: string) => Promise<void>;
  refreshAccount: () => Promise<void>;
  recheck: () => Promise<void>;
};

const SessionContext = createContext<SessionContextValue | null>(null);

// Queue and scan screens key on this so a new account or household never
// shows the previous one's rows.
function scopeOf(token: string, account: AuthSession | null): string {
  return `${token}|${account?.activeHouseholdId ?? ''}`;
}

function rejectedSignIn(error: ApiError): ApiError {
  if (error.status !== 401) return error;
  return new ApiError(error.status, INVALID_CREDENTIALS_SENTENCE, error.code);
}

function isAuthNotConfigured(error: unknown): boolean {
  return error instanceof ServerNoticeError && error.code === CODE_AUTH_NOT_CONFIGURED;
}

export function SessionProvider({ children }: { children: ReactNode }) {
  const [state, setState] = useState<SessionState>({ status: 'loading' });
  const serverRef = useRef(DEFAULT_SERVER_URL);
  const tokenRef = useRef('');

  useEffect(() => subscribeApiSuccess(() => {
    setState((current) => {
      if (current.status !== 'ready' || current.warning === '') return current;
      return { ...current, warning: '' };
    });
  }), []);

  const onUnauthorized = useCallback(() => {
    tokenRef.current = '';
    void clearToken();
    setState({ status: 'signedOut', serverUrl: serverRef.current, error: SESSION_EXPIRED_SENTENCE });
  }, []);

  const onUpgradeRequired = useCallback(() => {
    setState({ status: 'upgradeRequired' });
  }, []);

  const clientFor = useCallback((serverUrl: string, token: string | null) => {
    serverRef.current = serverUrl;
    tokenRef.current = token ?? '';
    return createStockbotClient({ serverUrl, token }, { onUnauthorized, onUpgradeRequired });
  }, [onUnauthorized, onUpgradeRequired]);

  const signOut = useCallback(async () => {
    const serverUrl = serverRef.current;
    const hadToken = tokenRef.current !== '';
    const client = hadToken ? clientFor(serverUrl, tokenRef.current) : null;
    tokenRef.current = '';
    setState({ status: 'signedOut', serverUrl, error: '' });
    await clearToken();
    if (client !== null) {
      try {
        await client.logout();
      } catch {
        // The token is already gone from this phone.
      }
    }
  }, [clientFor]);

  const signIn = useCallback(async (serverUrl: string, email: string, password: string) => {
    const normalized = normalizeServerUrl(serverUrl);
    if (password.length < 10) throw new Error(PASSWORD_TOO_SHORT);
    try {
      const config = await clientFor(normalized, null).getConfig();
      if (isOlderVersion(APP_VERSION, config.minClientVersion.expo)) {
        setState({ status: 'upgradeRequired' });
        return;
      }
      const origin = config.entry === 'lan' && config.appBaseUrl ? normalizeServerUrl(config.appBaseUrl) : normalized;
      const { token, ...account } = await clientFor(origin, null).login(email.trim(), password);
      if (token === undefined || token === '') throw new Error('Sign-in is unavailable right now.');
      await saveCredentials({ serverUrl: origin, token });
      setState({
        status: 'ready',
        serverUrl: origin,
        client: clientFor(origin, token),
        account,
        scope: scopeOf(token, account),
        warning: '',
      });
    } catch (error) {
      if (isAuthNotConfigured(error)) setState({ status: 'setup', serverUrl: normalized, token: null });
      if (error instanceof ApiError) throw rejectedSignIn(error);
      throw error;
    }
  }, [clientFor]);

  const switchHousehold = useCallback(async (householdId: string) => {
    if (state.status !== 'ready') return;
    const account = await state.client.switchHousehold(householdId);
    setState((current) => {
      if (current.status !== 'ready') return current;
      return { ...current, account, scope: scopeOf(tokenRef.current, account) };
    });
  }, [state]);

  const refreshAccount = useCallback(async () => {
    if (state.status !== 'ready') return;
    const account = await state.client.getAuthSession();
    setState((current) => {
      if (current.status !== 'ready') return current;
      return { ...current, account, scope: scopeOf(tokenRef.current, account) };
    });
  }, [state]);

  const recheck = useCallback(async () => {
    if (state.status !== 'setup') return;
    const { serverUrl, token } = state;
    const client = clientFor(serverUrl, token);
    try {
      if (token === null) {
        await client.getConfig();
        setState({ status: 'signedOut', serverUrl, error: '' });
        return;
      }
      const account = await client.getAuthSession();
      setState({ status: 'ready', serverUrl, client, account, scope: scopeOf(token, account), warning: '' });
    } catch (error) {
      if (isAuthNotConfigured(error)) return;
      if (error instanceof UnreachableError) {
        if (token === null) {
          setState({ status: 'signedOut', serverUrl, error: '' });
        } else {
          setState({ status: 'ready', serverUrl, client, account: null, scope: scopeOf(token, null), warning: '' });
        }
        return;
      }
      if (error instanceof ApiError && (error.status === 401 || error.status === 426)) return;
      setState({ status: 'signedOut', serverUrl, error: inlineFailure(error, 'Sign-in is unavailable right now.') ?? '' });
    }
  }, [state, clientFor]);

  useEffect(() => {
    let active = true;
    void (async () => {
      try {
        const saved = await loadSavedSession();
        if (!active) return;
        const serverUrl = saved.serverUrl ?? DEFAULT_SERVER_URL;
        if (saved.token === null || saved.serverUrl === null) {
          serverRef.current = serverUrl;
          setState({ status: 'signedOut', serverUrl, error: '' });
          return;
        }
        const { token } = saved;
        const client = clientFor(serverUrl, token);
        try {
          const config = await client.getConfig();
          if (!active) return;
          if (isOlderVersion(APP_VERSION, config.minClientVersion.expo)) {
            setState({ status: 'upgradeRequired' });
            return;
          }
          const account = await client.getAuthSession();
          if (!active) return;
          setState({ status: 'ready', serverUrl, client, account, scope: scopeOf(token, account), warning: '' });
        } catch (error) {
          if (!active) return;
          if (isAuthNotConfigured(error)) {
            setState({ status: 'setup', serverUrl, token });
            return;
          }
          if (error instanceof ApiError && (error.status === 401 || error.code === CODE_UPGRADE_REQUIRED)) return;
          if (!(error instanceof Error)) raiseOfflineBanner();
          setState({
            status: 'ready',
            serverUrl,
            client,
            account: null,
            scope: scopeOf(token, null),
            warning: inlineFailure(error, '') ?? '',
          });
        }
      } catch (error) {
        if (!active) return;
        setState({
          status: 'signedOut',
          serverUrl: DEFAULT_SERVER_URL,
          error: inlineFailure(error, 'Saved sign-in could not be read.') ?? '',
        });
      }
    })();
    return () => {
      active = false;
    };
  }, [clientFor]);

  const value = useMemo(
    () => ({ ...state, signIn, signOut, switchHousehold, refreshAccount, recheck }),
    [state, signIn, signOut, switchHousehold, refreshAccount, recheck],
  );
  return <SessionContext.Provider value={value}>{children}</SessionContext.Provider>;
}

export function useSession(): SessionContextValue {
  const value = use(SessionContext);
  if (value === null) {
    throw new Error('useSession must be used inside SessionProvider');
  }
  return value;
}
