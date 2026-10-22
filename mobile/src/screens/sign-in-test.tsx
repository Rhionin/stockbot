import { fireEvent, waitFor } from '@testing-library/react-native';
import * as SecureStore from 'expo-secure-store';
import { useState } from 'react';
import { Pressable, Text } from 'react-native';
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

function HttpProbe() {
  const session = useSession();
  const [error, setError] = useState('');
  if (session.status === 'loading') return <Text>Loading</Text>;
  return (
    <Pressable
      accessibilityLabel="Connect insecure"
      onPress={() => {
        void session.signIn('http://192.168.1.20:8080', 'correct-horse').catch((reason: unknown) => {
          setError(reason instanceof Error ? reason.message : 'failed');
        });
      }}
    >
      <Text>{error || (session.status === 'ready' ? 'Ready' : 'Signed out')}</Text>
    </Pressable>
  );
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

    await fireEvent.changeText(screen.getByLabelText('Server'), 'https://pantry.example.com/');
    await fireEvent.changeText(screen.getByLabelText('Household password'), 'correct-horse');
    await fireEvent.press(screen.getByLabelText('Connect'));

    await waitFor(() => expect(onSubmit).toHaveBeenCalledWith('https://pantry.example.com/', 'correct-horse'));
    expect(screen.getByLabelText(/Version .+\. Update .+\. Runtime /)).toBeTruthy();
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

  it('rejects an http address before calling the server', async () => {
    const screen = await renderScreen(
      <SessionProvider>
        <HttpProbe />
      </SessionProvider>,
    );

    await fireEvent.press(await screen.findByLabelText('Connect insecure'));

    expect(await screen.findByText('The address must start with https://.')).toBeTruthy();
    expect(screen.queryByText('Ready')).toBeNull();
    expect(SecureStore.setItemAsync).not.toHaveBeenCalled();
    expect(globalThis.fetch).not.toHaveBeenCalled();
  });

  it('rejects cleartext and forgets a saved http address', async () => {
    mockMemory.set(SERVER_URL_KEY, 'http://192.168.1.20:8080');
    mockMemory.set(PASSWORD_KEY, 'correct-horse');
    const screen = await renderScreen(
      <SessionProvider>
        <ConnectProbe />
      </SessionProvider>,
    );

    expect(await screen.findByText('The address must start with https://.')).toBeTruthy();
    expect(screen.queryByText('Ready')).toBeNull();
    expect(SecureStore.deleteItemAsync).toHaveBeenCalledWith(SERVER_URL_KEY);
    expect(SecureStore.deleteItemAsync).toHaveBeenCalledWith(PASSWORD_KEY);
    expect(globalThis.fetch).not.toHaveBeenCalled();
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
});
