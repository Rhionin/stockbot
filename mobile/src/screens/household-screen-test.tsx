import { fireEvent, waitFor } from '@testing-library/react-native';
import {
  AUTH_LOGOUT_PATH,
  AUTH_SESSION_PATH,
  CONFIG_PATH,
  SWITCH_HOUSEHOLD_PATH,
  type AuthSession,
} from '@stockbot/shared/contract';
import type { ScanEntry } from '@stockbot/shared/types';
import HouseholdRoute from '../app/(app)/household';
import QueueRoute from '../app/(app)/queue';
import { SERVER_URL_KEY, SESSION_TOKEN_KEY } from '../auth/credentials';
import { SessionProvider } from '../session';
import { renderScreen } from '../test/renderScreen';
import { HouseholdScreen } from './HouseholdScreen';

const mockMemory = new Map<string, string>();

jest.mock('expo-router', () => {
  const { useEffect } = jest.requireActual<typeof import('react')>('react');
  return {
    router: { replace: jest.fn() },
    useFocusEffect: (effect: () => void | (() => void)) => {
      useEffect(effect, [effect]);
    },
  };
});

jest.mock('expo-secure-store', () => ({
  getItemAsync: jest.fn(async (key: string) => mockMemory.get(key) ?? null),
  setItemAsync: jest.fn(async (key: string, value: string) => {
    mockMemory.set(key, value);
  }),
  deleteItemAsync: jest.fn(async (key: string) => {
    mockMemory.delete(key);
  }),
}));

const single: AuthSession = {
  user: { id: 'u1', email: 'ada@example.test', name: 'Ada' },
  kind: 'account',
  memberships: [{ householdId: 'hh-1', name: 'Home', role: 'owner', deletedAt: null }],
  households: [{ id: 'hh-1', name: 'Home', role: 'owner', deletedAt: null }],
  activeHouseholdId: 'hh-1',
  operator: false,
};

const two: AuthSession = {
  ...single,
  memberships: [
    { householdId: 'hh-1', name: 'Home', role: 'owner', deletedAt: null },
    { householdId: 'hh-2', name: 'Cabin', role: 'member', deletedAt: null },
  ],
  households: [
    { id: 'hh-1', name: 'Home', role: 'owner', deletedAt: null },
    { id: 'hh-2', name: 'Cabin', role: 'member', deletedAt: null },
  ],
};

describe('HouseholdScreen', () => {
  it('shows the account and household with no switcher for one membership', async () => {
    const screen = await renderScreen(
      <HouseholdScreen topInset={0} serverUrl="https://stockbot.example" account={single} onSwitch={jest.fn()} onSignOut={jest.fn()} />,
    );

    expect(screen.getByText('Ada')).toBeTruthy();
    expect(screen.getByText('ada@example.test')).toBeTruthy();
    expect(screen.getByText('Home')).toBeTruthy();
    expect(screen.getByText('https://stockbot.example')).toBeTruthy();
    expect(screen.queryByText('Switch household')).toBeNull();
  });

  it('switches to another household and signs out', async () => {
    const onSwitch = jest.fn(async () => undefined);
    const onSignOut = jest.fn();
    const screen = await renderScreen(
      <HouseholdScreen topInset={0} serverUrl="https://stockbot.example" account={two} onSwitch={onSwitch} onSignOut={onSignOut} />,
    );

    await fireEvent.press(screen.getByLabelText('Home'));
    expect(onSwitch).not.toHaveBeenCalled();
    await fireEvent.press(screen.getByLabelText('Cabin'));
    await waitFor(() => expect(onSwitch).toHaveBeenCalledWith('hh-2'));
    await fireEvent.press(screen.getByLabelText('Sign out'));
    expect(onSignOut).toHaveBeenCalledTimes(1);
  });

  it('shows why a switch failed', async () => {
    const screen = await renderScreen(
      <HouseholdScreen
        topInset={0}
        serverUrl="https://stockbot.example"
        account={two}
        onSwitch={async () => { throw new Error("Stockbot couldn't find that. It may have been removed."); }}
        onSignOut={jest.fn()}
      />,
    );

    await fireEvent.press(screen.getByLabelText('Cabin'));

    expect(await screen.findByText("Stockbot couldn't find that. It may have been removed.")).toBeTruthy();
  });
});

function pending(name: string, id: string): ScanEntry {
  return {
    id,
    userId: 'user-1',
    barcode: '111222333444',
    scannedAt: '2026-10-16T12:00:00Z',
    direction: 'stock_in',
    unitCount: 1,
    expiresAt: null,
    status: 'pending',
    productId: `prod-${id}`,
    product: { id: `prod-${id}`, name, category: 'Dairy', unitOfMeasure: 'carton' },
    committedAt: null,
    createdAt: '2026-10-16T12:00:00Z',
  };
}

function response(status: number, body: unknown): Response {
  return {
    ok: status >= 200 && status < 300,
    status,
    statusText: 'OK',
    text: async () => JSON.stringify(body),
    json: async () => body,
  } as Response;
}

describe('household and account changes clear the queue', () => {
  beforeEach(() => {
    mockMemory.clear();
    mockMemory.set(SERVER_URL_KEY, 'https://stockbot.example');
    mockMemory.set(SESSION_TOKEN_KEY, 'sbs_saved');
  });

  afterEach(() => {
    jest.restoreAllMocks();
  });

  function Tabs() {
    return (
      <>
        <QueueRoute />
        <HouseholdRoute />
      </>
    );
  }

  it('drops the previous household scans while the new household queue is still loading', async () => {
    let active = 'hh-1';
    jest.spyOn(globalThis, 'fetch').mockImplementation(async (input: RequestInfo | URL) => {
      const path = new URL(String(input)).pathname;
      if (path === CONFIG_PATH) return response(200, { live: 'sse', minClientVersion: { expo: '0.0.0' } });
      if (path === AUTH_SESSION_PATH) return response(200, { ...two, activeHouseholdId: active });
      if (path === SWITCH_HOUSEHOLD_PATH) {
        active = 'hh-2';
        return response(200, { ...two, activeHouseholdId: 'hh-2' });
      }
      if (path === '/api/scans') {
        if (active === 'hh-2') return new Promise<Response>(() => undefined);
        return response(200, String(input).includes('status=flagged') ? [] : [pending('Oat milk', 'a')]);
      }
      return response(200, []);
    });
    const screen = await renderScreen(<SessionProvider><Tabs /></SessionProvider>);
    expect(await screen.findByText('Oat milk')).toBeTruthy();

    await fireEvent.press(await screen.findByLabelText('Cabin'));

    await waitFor(() => expect(screen.queryByText('Oat milk')).toBeNull());
  });

  it('keeps the same queue when the household does not change', async () => {
    jest.spyOn(globalThis, 'fetch').mockImplementation(async (input: RequestInfo | URL) => {
      const path = new URL(String(input)).pathname;
      if (path === CONFIG_PATH) return response(200, { live: 'sse', minClientVersion: { expo: '0.0.0' } });
      if (path === AUTH_SESSION_PATH) return response(200, two);
      if (path === '/api/scans') return response(200, String(input).includes('status=flagged') ? [] : [pending('Oat milk', 'a')]);
      return response(200, []);
    });
    const screen = await renderScreen(<SessionProvider><Tabs /></SessionProvider>);
    await screen.findByText('Oat milk');

    await fireEvent.press(await screen.findByLabelText('Home'));

    expect(screen.getByText('Oat milk')).toBeTruthy();
  });

  it('clears the queue on sign out', async () => {
    jest.spyOn(globalThis, 'fetch').mockImplementation(async (input: RequestInfo | URL) => {
      const path = new URL(String(input)).pathname;
      if (path === CONFIG_PATH) return response(200, { live: 'sse', minClientVersion: { expo: '0.0.0' } });
      if (path === AUTH_SESSION_PATH) return response(200, two);
      if (path === AUTH_LOGOUT_PATH) return response(200, {});
      if (path === '/api/scans') return response(200, String(input).includes('status=flagged') ? [] : [pending('Oat milk', 'a')]);
      return response(200, []);
    });
    const screen = await renderScreen(<SessionProvider><Tabs /></SessionProvider>);
    await screen.findByText('Oat milk');

    await fireEvent.press(await screen.findByLabelText('Sign out'));

    await waitFor(() => expect(screen.queryByText('Oat milk')).toBeNull());
    expect(mockMemory.has(SESSION_TOKEN_KEY)).toBe(false);
  });
});
