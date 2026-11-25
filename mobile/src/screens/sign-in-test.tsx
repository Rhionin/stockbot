import { act, fireEvent, waitFor, within } from '@testing-library/react-native';
import * as SecureStore from 'expo-secure-store';
import { useEffect, useState } from 'react';
import { Pressable, Text } from 'react-native';
import {
  AUTH_LOGIN_PATH,
  AUTH_LOGOUT_PATH,
  AUTH_SESSION_PATH,
  CLIENT_HEADER,
  CONFIG_PATH,
  INVALID_CREDENTIALS_SENTENCE,
  OFFLINE_SENTENCE,
  PASSWORD_TOO_SHORT,
  SESSION_EXPIRED_SENTENCE,
  UPGRADE_REQUIRED_SENTENCE,
  UnreachableError,
  raiseOfflineBanner,
  serverNoticeText,
  type AuthSession,
} from '@stockbot/shared/contract';
import authNotConfigured from '../../../testdata/contract/auth_not_configured.json';
import SignInRoute from '../app/sign-in';
import { NoticeBanner } from '@stockbot/ui';
import { fonts } from '../ui/theme';
import { QueueScreen } from './QueueScreen';
import { SERVER_URL_KEY, SESSION_TOKEN_KEY } from '../auth/credentials';
import { ConnectionForm, ConnectionScreen } from './ConnectionForm';
import { SessionProvider, useSession } from '../session';
import { renderScreen } from '../test/renderScreen';

const mockMemory = new Map<string, string>();

jest.mock('expo-constants', () => ({
  __esModule: true,
  default: { expoConfig: { version: '1.0.0' } },
}));

jest.mock('expo-router', () => ({
  router: { replace: jest.fn() },
}));

jest.mock('expo-secure-store', () => ({
  getItemAsync: jest.fn(async (key: string) => mockMemory.get(key) ?? null),
  setItemAsync: jest.fn(async (key: string, value: string) => {
    mockMemory.set(key, value);
  }),
  deleteItemAsync: jest.fn(async (key: string) => {
    mockMemory.delete(key);
  }),
}));

function jsonResponse(status: number, body: unknown): Response {
  return {
    ok: status >= 200 && status < 300,
    status,
    statusText: 'Error',
    text: async () => JSON.stringify(body),
    json: async () => body,
  } as Response;
}

const CONFIG = { live: 'sse', minClientVersion: { expo: '0.0.0' }, entry: 'public' };
const ACCOUNT: AuthSession = {
  user: { id: 'u1', email: 'ada@example.test', name: 'Ada' },
  kind: 'account',
  memberships: [{ householdId: 'hh-1', name: 'Home', role: 'owner', deletedAt: null }],
  households: [{ id: 'hh-1', name: 'Home', role: 'owner', deletedAt: null }],
  activeHouseholdId: 'hh-1',
  operator: false,
};
const PASSWORD = 'correct horse battery';

function pathOf(input: RequestInfo | URL): string {
  return new URL(String(input)).pathname;
}

function callsTo(path: string) {
  return jest.mocked(globalThis.fetch).mock.calls.filter(([input]) => pathOf(input) === path);
}

function seedSession(serverUrl = 'https://stockbot.example', token = 'sbs_saved') {
  mockMemory.set(SERVER_URL_KEY, serverUrl);
  mockMemory.set(SESSION_TOKEN_KEY, token);
}

function stockbotServer(overrides: Partial<Record<string, () => Response | Promise<Response>>> = {}) {
  return jest.spyOn(globalThis, 'fetch').mockImplementation(async (input: RequestInfo | URL) => {
    const path = pathOf(input);
    const handler = overrides[path];
    if (handler) return handler();
    if (path === CONFIG_PATH) return jsonResponse(200, CONFIG);
    if (path === AUTH_LOGIN_PATH) return jsonResponse(200, { ...ACCOUNT, token: 'sbs_fresh' });
    if (path === AUTH_SESSION_PATH) return jsonResponse(200, ACCOUNT);
    if (path === AUTH_LOGOUT_PATH) return jsonResponse(200, {});
    return jsonResponse(200, []);
  });
}

function ConnectProbe({ server = 'https://stockbot.rhionin.com', password = PASSWORD }: { server?: string; password?: string }) {
  const session = useSession();
  const [error, setError] = useState('');
  if (session.status === 'loading') return <Text>Loading</Text>;
  if (session.status === 'ready') return <Text>Ready</Text>;
  if (session.status === 'upgradeRequired') return <Text>{UPGRADE_REQUIRED_SENTENCE}</Text>;
  const signedOutError = session.status === 'signedOut' ? session.error : '';
  return (
    <Pressable
      accessibilityLabel="Connect now"
      onPress={() => {
        void session.signIn(server, 'ada@example.test', password).catch((reason: unknown) => {
          setError(reason instanceof Error ? reason.message : 'failed');
        });
      }}
    >
      <Text>{error || signedOutError || 'Signed out'}</Text>
    </Pressable>
  );
}

describe('ConnectionForm', () => {
  it('sends the server, email and password', async () => {
    const onSubmit = jest.fn(async () => undefined);
    const screen = await renderScreen(
      <ConnectionForm
        heading="Stockbot"
        lede="Scan the pantry from the phone."
        submitLabel="Sign in"
        onSubmit={onSubmit}
      />,
    );

    await fireEvent.changeText(screen.getByLabelText('Server'), 'http://192.168.1.20:8080/');
    await fireEvent.changeText(screen.getByLabelText('Email'), 'ada@example.test');
    await fireEvent.changeText(screen.getByLabelText('Password'), PASSWORD);
    await fireEvent.press(screen.getByLabelText('Sign in'));

    await waitFor(() => expect(onSubmit).toHaveBeenCalledWith('http://192.168.1.20:8080/', 'ada@example.test', PASSWORD));
  });

  it('has no household password field', async () => {
    const screen = await renderScreen(
      <ConnectionForm heading="Stockbot" lede="Scan." submitLabel="Sign in" onSubmit={async () => undefined} />,
    );
    expect(screen.queryByLabelText('Household password')).toBeNull();
  });

  it('shows the message when sign-in fails', async () => {
    const screen = await renderScreen(
      <ConnectionForm
        heading="Stockbot"
        lede="Scan the pantry from the phone."
        submitLabel="Sign in"
        onSubmit={async () => {
          throw new Error(INVALID_CREDENTIALS_SENTENCE);
        }}
      />,
    );

    await fireEvent.press(screen.getByLabelText('Sign in'));
    expect(await screen.findByText(INVALID_CREDENTIALS_SENTENCE)).toBeTruthy();
  });
});

describe('session sign-in', () => {
  beforeEach(() => {
    mockMemory.clear();
    jest.clearAllMocks();
    stockbotServer();
  });

  afterEach(() => {
    jest.restoreAllMocks();
  });

  it('logs in with email and password and stores the server and Bearer token only', async () => {
    const screen = await renderScreen(
      <SessionProvider>
        <ConnectProbe />
      </SessionProvider>,
    );

    await fireEvent.press(await screen.findByLabelText('Connect now'));

    await waitFor(() => expect(screen.getByText('Ready')).toBeTruthy());
    const [login] = callsTo(AUTH_LOGIN_PATH);
    expect(login?.[0]).toBe(`https://stockbot.rhionin.com${AUTH_LOGIN_PATH}`);
    expect(JSON.parse(String(login?.[1]?.body))).toEqual({ email: 'ada@example.test', password: PASSWORD });
    const headers = new Headers(login?.[1]?.headers);
    expect(headers.get(CLIENT_HEADER)).toBe('expo/1.0.0');
    expect(headers.has('Authorization')).toBe(false);
    expect(mockMemory.get(SERVER_URL_KEY)).toBe('https://stockbot.rhionin.com');
    expect(mockMemory.get(SESSION_TOKEN_KEY)).toBe('sbs_fresh');
    expect([...mockMemory.values()]).not.toContain(PASSWORD);
  });

  it('asks for a longer password before calling the server', async () => {
    const screen = await renderScreen(
      <SessionProvider>
        <ConnectProbe password="short" />
      </SessionProvider>,
    );

    await fireEvent.press(await screen.findByLabelText('Connect now'));

    expect(await screen.findByText(PASSWORD_TOO_SHORT)).toBeTruthy();
    expect(callsTo(AUTH_LOGIN_PATH)).toHaveLength(0);
  });

  it('shows a failed-sign-in sentence on a login 401 and leaves a stored token alone', async () => {
    stockbotServer({
      [AUTH_LOGIN_PATH]: () => jsonResponse(401, { error: SESSION_EXPIRED_SENTENCE, code: 'unauthorized' }),
    });
    const screen = await renderScreen(
      <SessionProvider>
        <ConnectProbe />
      </SessionProvider>,
    );
    const button = await screen.findByLabelText('Connect now');
    mockMemory.set(SESSION_TOKEN_KEY, 'sbs_other');
    jest.mocked(SecureStore.deleteItemAsync).mockClear();

    await fireEvent.press(button);

    expect(await screen.findByText(INVALID_CREDENTIALS_SENTENCE)).toBeTruthy();
    expect(screen.queryByText('Ready')).toBeNull();
    expect(SecureStore.deleteItemAsync).not.toHaveBeenCalled();
    expect(mockMemory.get(SESSION_TOKEN_KEY)).toBe('sbs_other');
    expect(SecureStore.setItemAsync).not.toHaveBeenCalled();
  });

  it('signs in against appBaseUrl when the server reports a LAN entry', async () => {
    stockbotServer({
      [CONFIG_PATH]: () => jsonResponse(200, { ...CONFIG, entry: 'lan', appBaseUrl: 'https://stockbot.example/' }),
    });
    const screen = await renderScreen(
      <SessionProvider>
        <ConnectProbe server="http://192.168.1.20:8080" />
      </SessionProvider>,
    );

    await fireEvent.press(await screen.findByLabelText('Connect now'));

    await waitFor(() => expect(screen.getByText('Ready')).toBeTruthy());
    expect(callsTo(CONFIG_PATH)[0]?.[0]).toBe(`http://192.168.1.20:8080${CONFIG_PATH}`);
    expect(callsTo(AUTH_LOGIN_PATH)[0]?.[0]).toBe(`https://stockbot.example${AUTH_LOGIN_PATH}`);
    expect(mockMemory.get(SERVER_URL_KEY)).toBe('https://stockbot.example');
  });

  it('keeps the typed address when the entry is public', async () => {
    stockbotServer({
      [CONFIG_PATH]: () => jsonResponse(200, { ...CONFIG, appBaseUrl: 'https://elsewhere.example' }),
    });
    const screen = await renderScreen(
      <SessionProvider>
        <ConnectProbe server="https://stockbot.example" />
      </SessionProvider>,
    );

    await fireEvent.press(await screen.findByLabelText('Connect now'));

    await waitFor(() => expect(screen.getByText('Ready')).toBeTruthy());
    expect(callsTo(AUTH_LOGIN_PATH)[0]?.[0]).toBe(`https://stockbot.example${AUTH_LOGIN_PATH}`);
  });

  it('shows the update sentence and never logs in when the server needs a newer app', async () => {
    stockbotServer({
      [CONFIG_PATH]: () => jsonResponse(200, { ...CONFIG, minClientVersion: { expo: '99.0.0' } }),
    });
    const screen = await renderScreen(
      <SessionProvider>
        <ConnectProbe />
      </SessionProvider>,
    );

    await fireEvent.press(await screen.findByLabelText('Connect now'));

    expect(await screen.findByText(UPGRADE_REQUIRED_SENTENCE)).toBeTruthy();
    expect(callsTo(AUTH_LOGIN_PATH)).toHaveLength(0);
    expect(SecureStore.setItemAsync).not.toHaveBeenCalled();
  });

  it('shows the offline sentence when sign-in cannot reach the server', async () => {
    jest.spyOn(globalThis, 'fetch').mockRejectedValue(new TypeError('Network request failed'));
    const screen = await renderScreen(
      <SessionProvider>
        <ConnectProbe />
      </SessionProvider>,
    );

    await fireEvent.press(await screen.findByLabelText('Connect now'));
    expect(await screen.findByText(OFFLINE_SENTENCE)).toBeTruthy();
    expect(screen.queryByText('Network request failed')).toBeNull();
  });

  it('keeps the previous sentence when sign-in fails before the network', async () => {
    jest.spyOn(globalThis, 'fetch').mockRejectedValue(new Error('offline'));
    const screen = await renderScreen(
      <SessionProvider>
        <ConnectProbe />
      </SessionProvider>,
    );

    await fireEvent.press(await screen.findByLabelText('Connect now'));
    expect(await screen.findByText('Stockbot did not answer at that address.')).toBeTruthy();
    expect(screen.queryByText(OFFLINE_SENTENCE)).toBeNull();
    expect(screen.queryByText('offline')).toBeNull();
  });

  it('uses the queue banner for a saved-session offline startup and clears it after a load', async () => {
    seedSession();
    let online = false;
    jest.spyOn(globalThis, 'fetch').mockImplementation(async (input: RequestInfo | URL) => {
      if (!online) throw new TypeError('Network request failed');
      return jsonResponse(200, pathOf(input) === AUTH_SESSION_PATH ? ACCOUNT : []);
    });

    function Probe() {
      const session = useSession();
      if (session.status === 'loading') return <Text>Loading</Text>;
      if (session.status !== 'ready') return <Text>Signed out</Text>;
      return (
        <>
          <QueueScreen client={session.client} warning={session.warning} />
          <Text>{session.warning === '' ? 'no-warning' : session.warning}</Text>
          <Pressable accessibilityLabel="Reload" onPress={() => { void session.client.listPending(); }} />
        </>
      );
    }

    const screen = await renderScreen(
      <SessionProvider>
        <Probe />
      </SessionProvider>,
    );
    expect(within(await screen.findByTestId('server-notice-banner')).getByText(OFFLINE_SENTENCE)).toBeTruthy();
    expect(screen.getAllByText(OFFLINE_SENTENCE)).toHaveLength(1);
    expect(screen.getByText('no-warning')).toBeTruthy();
    expect(screen.queryByTestId('confirm-toast')).toBeNull();
    expect(screen.queryByText('Network request failed')).toBeNull();
    expect(mockMemory.get(SESSION_TOKEN_KEY)).toBe('sbs_saved');

    online = true;
    await fireEvent.press(screen.getByLabelText('Reload'));
    await waitFor(() => expect(screen.queryByText(OFFLINE_SENTENCE)).toBeNull());
    expect(screen.getByText('no-warning')).toBeTruthy();
  });

  it('shows the connection banner for an unreachable submit', async () => {
    const screen = await renderScreen(
      <ConnectionScreen
        topInset={0}
        heading="Connect"
        lede="The phone keeps this Stockbot."
        submitLabel="Connect now"
        onSubmit={async () => {
          raiseOfflineBanner();
          throw new UnreachableError();
        }}
      />,
    );
    await fireEvent.press(await screen.findByLabelText('Connect now'));
    expect(await screen.findByText(OFFLINE_SENTENCE)).toBeTruthy();
    expect(screen.getAllByText(OFFLINE_SENTENCE)).toHaveLength(1);
  });

  it('shows the raw message when sign-in fails for another reason', async () => {
    const screen = await renderScreen(
      <ConnectionForm
        heading="Connect"
        lede="The phone keeps this Stockbot."
        submitLabel="Connect now"
        onSubmit={async () => {
          throw new Error('Network error');
        }}
      />,
    );
    await fireEvent.press(screen.getByLabelText('Connect now'));
    expect(await screen.findByText('Network error')).toBeTruthy();
    expect(screen.queryByText(OFFLINE_SENTENCE)).toBeNull();
  });

  it('clears a startup warning that is not the offline sentence', async () => {
    seedSession();
    let sessionCalls = 0;
    jest.spyOn(globalThis, 'fetch').mockImplementation(async (input: RequestInfo | URL) => {
      if (pathOf(input) === CONFIG_PATH && sessionCalls === 0) {
        sessionCalls += 1;
        throw new Error('offline');
      }
      return jsonResponse(200, []);
    });

    function Probe() {
      const session = useSession();
      if (session.status === 'loading') return <Text>Loading</Text>;
      if (session.status !== 'ready') return <Text>Signed out</Text>;
      return (
        <>
          <Text>{session.warning === '' ? 'no-warning' : session.warning}</Text>
          <Pressable accessibilityLabel="Reload" onPress={() => { void session.client.listPending(); }} />
        </>
      );
    }

    const screen = await renderScreen(
      <SessionProvider>
        <Probe />
      </SessionProvider>,
    );
    expect(await screen.findByText('Stockbot did not answer at that address.')).toBeTruthy();
    await fireEvent.press(screen.getByLabelText('Reload'));
    await waitFor(() => expect(screen.getByText('no-warning')).toBeTruthy());
    expect(screen.queryByText('Stockbot did not answer at that address.')).toBeNull();
  });
});

function AuthGate() {
  const session = useSession();
  const [reloads, setReloads] = useState(0);
  useEffect(() => {
    if (session.status === 'ready' && reloads > 0) void session.client.listPending().catch(() => undefined);
  }, [session, reloads]);
  if (session.status === 'loading') return null;
  if (session.status === 'ready') {
    return (
      <>
        <Text>In the app</Text>
        <Pressable accessibilityLabel="Reload" onPress={() => setReloads((n) => n + 1)} />
        <Pressable accessibilityLabel="Sign out now" onPress={() => { void session.signOut(); }} />
        <NoticeBanner fontRegular={fonts.regular} fontBold={fonts.bold} />
      </>
    );
  }
  return <SignInRoute />;
}

describe('saved session', () => {
  beforeEach(() => {
    mockMemory.clear();
    jest.clearAllMocks();
  });

  afterEach(() => {
    jest.restoreAllMocks();
  });

  it('opens the app with the saved token and sends it on every request', async () => {
    seedSession();
    stockbotServer();
    const screen = await renderScreen(<SessionProvider><AuthGate /></SessionProvider>);

    expect(await screen.findByText('In the app')).toBeTruthy();
    const headers = new Headers(callsTo(AUTH_SESSION_PATH)[0]?.[1]?.headers);
    expect(headers.get('Authorization')).toBe('Bearer sbs_saved');
    expect(headers.get(CLIENT_HEADER)).toBe('expo/1.0.0');
  });

  it('clears the token and shows sign-in when a later request gets a 401', async () => {
    seedSession();
    let expired = false;
    stockbotServer({
      '/api/scans': () => (expired
        ? jsonResponse(401, { error: SESSION_EXPIRED_SENTENCE, code: 'unauthorized' })
        : jsonResponse(200, [])),
    });
    const screen = await renderScreen(<SessionProvider><AuthGate /></SessionProvider>);
    await screen.findByText('In the app');
    expired = true;

    await fireEvent.press(screen.getByLabelText('Reload'));

    expect(await screen.findByLabelText('Email')).toBeTruthy();
    expect(screen.getByText(SESSION_EXPIRED_SENTENCE)).toBeTruthy();
    expect(mockMemory.has(SESSION_TOKEN_KEY)).toBe(false);
    expect(mockMemory.get(SERVER_URL_KEY)).toBe('https://stockbot.example');
    expect(screen.getByLabelText('Server').props.value).toBe('https://stockbot.example');
  });

  it('clears the token when the saved session is already rejected at launch', async () => {
    seedSession();
    stockbotServer({
      [AUTH_SESSION_PATH]: () => jsonResponse(401, { error: SESSION_EXPIRED_SENTENCE, code: 'unauthorized' }),
    });
    const screen = await renderScreen(<SessionProvider><AuthGate /></SessionProvider>);

    expect(await screen.findByLabelText('Email')).toBeTruthy();
    expect(mockMemory.has(SESSION_TOKEN_KEY)).toBe(false);
    expect(screen.getByText(SESSION_EXPIRED_SENTENCE)).toBeTruthy();
  });

  it('shows the update sentence for a 426 and does not fall back to sign-in', async () => {
    seedSession();
    stockbotServer({
      [CONFIG_PATH]: () => jsonResponse(426, { error: UPGRADE_REQUIRED_SENTENCE, code: 'upgrade_required' }),
    });
    const screen = await renderScreen(<SessionProvider><AuthGate /></SessionProvider>);

    expect(await screen.findByText(UPGRADE_REQUIRED_SENTENCE)).toBeTruthy();
    expect(screen.queryByLabelText('Email')).toBeNull();
    expect(mockMemory.get(SESSION_TOKEN_KEY)).toBe('sbs_saved');
    expect(callsTo(AUTH_LOGIN_PATH)).toHaveLength(0);
  });

  it('shows the update sentence when the minimum version is above this app, without asking for the session', async () => {
    seedSession();
    stockbotServer({
      [CONFIG_PATH]: () => jsonResponse(200, { ...CONFIG, minClientVersion: { expo: '99.0.0' } }),
    });
    const screen = await renderScreen(<SessionProvider><AuthGate /></SessionProvider>);

    expect(await screen.findByText(UPGRADE_REQUIRED_SENTENCE)).toBeTruthy();
    expect(screen.queryByLabelText('Email')).toBeNull();
    expect(callsTo(AUTH_SESSION_PATH)).toHaveLength(0);
    expect(mockMemory.get(SESSION_TOKEN_KEY)).toBe('sbs_saved');
  });

  it('opens the app when the minimum version equals this app', async () => {
    seedSession();
    stockbotServer({
      [CONFIG_PATH]: () => jsonResponse(200, { ...CONFIG, minClientVersion: { expo: '1.0.0' } }),
    });
    const screen = await renderScreen(<SessionProvider><AuthGate /></SessionProvider>);

    expect(await screen.findByText('In the app')).toBeTruthy();
    expect(screen.queryByText(UPGRADE_REQUIRED_SENTENCE)).toBeNull();
  });

  it('signs out on the server, drops the token and returns to sign-in', async () => {
    seedSession();
    stockbotServer();
    const screen = await renderScreen(<SessionProvider><AuthGate /></SessionProvider>);
    await screen.findByText('In the app');

    await fireEvent.press(screen.getByLabelText('Sign out now'));

    expect(await screen.findByLabelText('Email')).toBeTruthy();
    await waitFor(() => expect(callsTo(AUTH_LOGOUT_PATH)).toHaveLength(1));
    expect(new Headers(callsTo(AUTH_LOGOUT_PATH)[0]?.[1]?.headers).get('Authorization')).toBe('Bearer sbs_saved');
    expect(mockMemory.has(SESSION_TOKEN_KEY)).toBe(false);
  });

  it('stays plainly signed out when the server answers the sign-out with 401', async () => {
    seedSession();
    stockbotServer({
      [AUTH_LOGOUT_PATH]: () => jsonResponse(401, { error: SESSION_EXPIRED_SENTENCE, code: 'unauthorized' }),
    });
    function Probe() {
      const session = useSession();
      if (session.status === 'ready') {
        return <Pressable accessibilityLabel="Sign out now" onPress={() => { void session.signOut(); }} />;
      }
      return <Text testID="state">{session.status === 'signedOut' ? `signedOut:${session.error}` : session.status}</Text>;
    }
    const screen = await renderScreen(<SessionProvider><Probe /></SessionProvider>);

    await fireEvent.press(await screen.findByLabelText('Sign out now'));

    await waitFor(() => expect(callsTo(AUTH_LOGOUT_PATH)).toHaveLength(1));
    await act(async () => { await new Promise((done) => { setTimeout(done, 0); }); });
    expect(screen.getByTestId('state').props.children).toBe('signedOut:');
    expect(mockMemory.has(SESSION_TOKEN_KEY)).toBe(false);
  });
});

const AUTH_BANNER = "Sign-in isn't set up yet.";
const SETUP_SENTENCE = "Whoever runs this server needs to finish setting it up.";

describe('auth is not configured', () => {
  afterEach(() => {
    mockMemory.clear();
    jest.restoreAllMocks();
  });

  it.each([
    { via: 'session', next: 'form' },
    { via: 'session', next: 'stay' },
    { via: 'session', next: 'offline' },
    { via: 'connect', next: 'form' },
  ])('hides the form when $via returns auth_not_configured and Try again handles $next', async ({ via, next }) => {
    let calls = 0;
    let mode: 'auth' | 'form' | 'offline' = 'auth';
    jest.spyOn(globalThis, 'fetch').mockImplementation(async (input: RequestInfo | URL) => {
      calls += 1;
      if (mode === 'offline') throw new TypeError('Network request failed');
      if (mode === 'form') return jsonResponse(200, pathOf(input) === CONFIG_PATH ? CONFIG : ACCOUNT);
      return jsonResponse(503, { error: AUTH_BANNER, code: authNotConfigured.code });
    });
    if (via === 'session') seedSession();
    const screen = await renderScreen(
      <SessionProvider>
        <AuthGate />
      </SessionProvider>,
    );
    if (via === 'connect') {
      await fireEvent.changeText(await screen.findByLabelText('Server'), 'https://stockbot.example');
      await fireEvent.changeText(screen.getByLabelText('Email'), 'ada@example.test');
      await fireEvent.changeText(screen.getByLabelText('Password'), PASSWORD);
      await fireEvent.press(screen.getByLabelText('Sign in'));
    }
    expect(await screen.findByText(SETUP_SENTENCE)).toBeTruthy();
    expect(screen.getByText(AUTH_BANNER)).toBeTruthy();
    expect(screen.getAllByText(AUTH_BANNER)).toHaveLength(1);
    expect(AUTH_BANNER.includes('’')).toBe(false);
    expect(screen.getAllByText(SETUP_SENTENCE)).toHaveLength(1);
    expect(screen.getByLabelText('Try again')).toBeTruthy();
    expect(screen.queryByLabelText('Password')).toBeNull();
    expect(screen.queryByLabelText('Server')).toBeNull();
    expect(JSON.stringify(screen.toJSON())).not.toContain('/setup');

    const before = calls;
    mode = next === 'form' ? 'form' : next === 'stay' ? 'auth' : 'offline';
    await fireEvent.press(screen.getByLabelText('Try again'));
    await waitFor(() => expect(calls).toBeGreaterThan(before));
    if (next === 'form' && via === 'session') {
      expect(await screen.findByText('In the app')).toBeTruthy();
      expect(screen.queryByText(AUTH_BANNER)).toBeNull();
    } else if (next === 'form') {
      expect(await screen.findByLabelText('Password')).toBeTruthy();
      expect(screen.queryByText(AUTH_BANNER)).toBeNull();
      expect(screen.queryByText(SETUP_SENTENCE)).toBeNull();
    } else if (next === 'offline') {
      expect(serverNoticeText()).toBe(OFFLINE_SENTENCE);
      expect(screen.queryByText(SETUP_SENTENCE)).toBeNull();
      expect(screen.queryByText(AUTH_BANNER)).toBeNull();
      expect(await screen.findByText('In the app')).toBeTruthy();
      expect(screen.getByText(OFFLINE_SENTENCE)).toBeTruthy();
    } else {
      expect(screen.getByText(SETUP_SENTENCE)).toBeTruthy();
      expect(screen.getByText(AUTH_BANNER)).toBeTruthy();
      expect(screen.queryByLabelText('Password')).toBeNull();
    }
    expect(JSON.stringify(screen.toJSON())).not.toContain('/setup');
  });

  it('keeps the saved token while setup is unfinished and opens the app once it is', async () => {
    seedSession();
    let ready = false;
    jest.spyOn(globalThis, 'fetch').mockImplementation(async (input: RequestInfo | URL) => {
      if (!ready) return jsonResponse(503, { error: AUTH_BANNER, code: authNotConfigured.code });
      return jsonResponse(200, pathOf(input) === CONFIG_PATH ? CONFIG : ACCOUNT);
    });
    const screen = await renderScreen(<SessionProvider><AuthGate /></SessionProvider>);
    expect(await screen.findByText(SETUP_SENTENCE)).toBeTruthy();
    expect(mockMemory.get(SESSION_TOKEN_KEY)).toBe('sbs_saved');

    ready = true;
    await fireEvent.press(screen.getByLabelText('Try again'));

    expect(await screen.findByText('In the app')).toBeTruthy();
  });
});
