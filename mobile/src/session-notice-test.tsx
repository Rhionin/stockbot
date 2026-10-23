import { Text } from 'react-native';
import { OFFLINE_SENTENCE, notifyApiSuccess } from '../../frontend/src/api/contract';
import { createStockbotClient } from './api/client';
import { PASSWORD_KEY, SERVER_URL_KEY } from './auth/credentials';
import { SessionProvider, useSession } from './session';
import { ServerNoticeBanner } from './ui/ServerNoticeBanner';
import { renderScreen } from './test/renderScreen';

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

jest.mock('./api/client', () => {
  const actual = jest.requireActual('./api/client') as typeof import('./api/client');
  return {
    ...actual,
    createStockbotClient: jest.fn(),
  };
});

function Probe() {
  const session = useSession();
  if (session.status === 'loading') return <Text>Loading</Text>;
  if (session.status !== 'ready') return <Text>Signed out</Text>;
  return (
    <>
      <ServerNoticeBanner />
      <Text>{session.warning === '' ? 'no-warning' : session.warning}</Text>
    </>
  );
}

describe('session non-Error startup', () => {
  afterEach(() => {
    notifyApiSuccess();
    mockMemory.clear();
  });

  it('raises the offline banner when startup rejects with a non-Error', async () => {
    mockMemory.set(SERVER_URL_KEY, 'https://stockbot.example');
    mockMemory.set(PASSWORD_KEY, 'correct-horse');
    jest.mocked(createStockbotClient).mockReturnValue({
      getSession: () => Promise.reject('down'),
    } as ReturnType<typeof createStockbotClient>);

    const screen = await renderScreen(
      <SessionProvider>
        <Probe />
      </SessionProvider>,
    );

    expect(await screen.findByText(OFFLINE_SENTENCE)).toBeTruthy();
    expect(screen.getByText('no-warning')).toBeTruthy();
    expect(screen.queryByText('down')).toBeNull();
    expect(screen.getAllByText(OFFLINE_SENTENCE)).toHaveLength(1);
  });
});
