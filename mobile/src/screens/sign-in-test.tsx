import { fireEvent, waitFor, within } from '@testing-library/react-native';
import * as SecureStore from 'expo-secure-store';
import { useState } from 'react';
import { Pressable, Text } from 'react-native';
import { OFFLINE_SENTENCE, UnreachableError, raiseOfflineBanner } from '../../../frontend/src/api/contract';
import { QueueScreen } from './QueueScreen';
import { basicAuthorization, HOUSEHOLD_USERNAME } from '../api/client';
import { PASSWORD_KEY, SERVER_URL_KEY } from '../auth/credentials';
import { ConnectionForm } from './ConnectionForm';
import { SessionProvider, useSession } from '../session';
import { renderScreen } from '../test/renderScreen';

const mockMemory = new Map<string, string>();

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

function ConnectProbe() {
  const session = useSession();
  const [error, setError] = useState('');
  if (session.status === 'loading') return <Text>Loading</Text>;
  if (session.status === 'ready') return <Text>Ready</Text>;
  return (
    <Pressable
      accessibilityLabel="Connect now"
      onPress={() => {
        void session.signIn('https://stockbot.rhionin.com', 'correct-horse').catch((reason: unknown) => {
          setError(reason instanceof Error ? reason.message : 'failed');
        });
      }}
    >
      <Text>{error || session.error || 'Signed out'}</Text>
    </Pressable>
  );
}

describe('ConnectionForm', () => {
  it('sends the server and household password', async () => {
    const onSubmit = jest.fn(async () => undefined);
    const screen = await renderScreen(
      <ConnectionForm
        heading="Stockbot"
        lede="Scan the pantry from the phone."
        submitLabel="Connect"
        onSubmit={onSubmit}
      />,
    );

    await fireEvent.changeText(screen.getByLabelText('Server'), 'http://192.168.1.20:8080/');
    await fireEvent.changeText(screen.getByLabelText('Household password'), 'correct-horse');
    await fireEvent.press(screen.getByLabelText('Connect'));

    await waitFor(() => expect(onSubmit).toHaveBeenCalledWith('http://192.168.1.20:8080/', 'correct-horse'));
  });

  it('shows the message when connect fails', async () => {
    const screen = await renderScreen(
      <ConnectionForm
        heading="Stockbot"
        lede="Scan the pantry from the phone."
        submitLabel="Connect"
        onSubmit={async () => {
          throw new Error('That password was rejected.');
        }}
      />,
    );

    await fireEvent.press(screen.getByLabelText('Connect'));
    expect(await screen.findByText('That password was rejected.')).toBeTruthy();
  });
});

describe('session sign-in', () => {
  beforeEach(() => {
    mockMemory.clear();
    jest.clearAllMocks();
    jest.spyOn(globalThis, 'fetch').mockResolvedValue(jsonResponse(200, { required: true, username: 'pantry' }));
  });

  afterEach(() => {
    jest.restoreAllMocks();
  });

  it('checks GET /api/session and stores the address and password', async () => {
    const screen = await renderScreen(
      <SessionProvider>
        <ConnectProbe />
      </SessionProvider>,
    );

    await fireEvent.press(await screen.findByLabelText('Connect now'));

    await waitFor(() => expect(screen.getByText('Ready')).toBeTruthy());
    expect(SecureStore.setItemAsync).toHaveBeenCalledWith(SERVER_URL_KEY, 'https://stockbot.rhionin.com');
    expect(SecureStore.setItemAsync).toHaveBeenCalledWith(PASSWORD_KEY, 'correct-horse');
    expect(globalThis.fetch).toHaveBeenCalledWith(
      'https://stockbot.rhionin.com/api/session',
      expect.objectContaining({ headers: expect.any(Headers) }),
    );
    const init = jest.mocked(globalThis.fetch).mock.calls[0]?.[1];
    expect(new Headers(init?.headers).get('Authorization')).toBe(basicAuthorization(HOUSEHOLD_USERNAME, 'correct-horse'));
  });

  it('stays signed out when the password is rejected', async () => {
    jest.mocked(globalThis.fetch).mockResolvedValue(jsonResponse(401, { error: 'Sign in required.' }));
    const screen = await renderScreen(
      <SessionProvider>
        <ConnectProbe />
      </SessionProvider>,
    );

    await fireEvent.press(await screen.findByLabelText('Connect now'));

    expect(await screen.findByText('That password was rejected.')).toBeTruthy();
    expect(screen.queryByText('Ready')).toBeNull();
    expect(SecureStore.setItemAsync).not.toHaveBeenCalled();
  });

  it('shows the offline sentence when connect cannot reach the server', async () => {
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

  it('keeps the previous sentence when connect fails before the network', async () => {
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
    mockMemory.set(SERVER_URL_KEY, 'https://stockbot.example');
    mockMemory.set(PASSWORD_KEY, 'correct-horse');
    let online = false;
    jest.spyOn(globalThis, 'fetch').mockImplementation(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url.endsWith('/api/session') || !online) throw new TypeError('Network request failed');
      return jsonResponse(200, []);
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

    online = true;
    await fireEvent.press(screen.getByLabelText('Reload'));
    await waitFor(() => expect(screen.queryByText(OFFLINE_SENTENCE)).toBeNull());
    expect(screen.getByText('no-warning')).toBeTruthy();
  });

  it('shows the connection banner for an unreachable submit', async () => {
    const screen = await renderScreen(
      <ConnectionForm
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

  it('shows the raw message when connect fails for another reason', async () => {
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
    mockMemory.set(SERVER_URL_KEY, 'https://stockbot.example');
    mockMemory.set(PASSWORD_KEY, 'correct-horse');
    let sessionCalls = 0;
    jest.mocked(globalThis.fetch).mockImplementation(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url.endsWith('/api/session') && sessionCalls === 0) {
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
