import type { ReactElement } from 'react';
import { StyleSheet } from 'react-native';
import { SafeAreaProvider } from 'react-native-safe-area-context';
import { act, render } from '@testing-library/react-native';
import { BUSY_SENTENCE, OFFLINE_SENTENCE, applyHttpResponse, notifyApiSuccess, raiseAuthNotConfiguredBanner, raiseOfflineBanner } from '../../../frontend/src/api/contract';
import { createStockbotClient } from '../api/client';
import { PASSWORD_KEY, SERVER_URL_KEY } from '../auth/credentials';
import HouseholdRoute from '../app/(app)/household';
import SignInRoute from '../app/sign-in';
import { SessionProvider } from '../session';
import { QueueScreen } from '../screens/QueueScreen';
import { ScanScreen } from '../screens/ScanScreen';
import { ServerNoticeBanner, SERVER_NOTICE_INSET, SERVER_NOTICE_RED } from './ServerNoticeBanner';

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

jest.mock('expo-router', () => ({
  router: { replace: jest.fn() },
}));

jest.mock('expo-camera', () => {
  // eslint-disable-next-line @typescript-eslint/no-require-imports
  const React = require('react');
  // eslint-disable-next-line @typescript-eslint/no-require-imports
  const { View: CameraHole } = require('react-native');
  return {
    CameraView: () => React.createElement(CameraHole, null),
    useCameraPermissions: () => [{ granted: false, canAskAgain: true }, jest.fn()],
  };
});

jest.mock('expo-haptics', () => ({
  notificationAsync: jest.fn(),
  NotificationFeedbackType: { Success: 'success', Error: 'error' },
}));

const TOP = 47;

function framed(ui: ReactElement) {
  return (
    <SafeAreaProvider
      initialMetrics={{
        insets: { top: TOP, right: 0, bottom: 0, left: 0 },
        frame: { x: 0, y: 0, width: 390, height: 844 },
      }}
    >
      {ui}
    </SafeAreaProvider>
  );
}

function collect(node: unknown, acc: string[] = []): string[] {
  if (typeof node === 'string') {
    acc.push(node);
    return acc;
  }
  if (Array.isArray(node)) {
    for (const child of node) collect(child, acc);
    return acc;
  }
  if (node !== null && typeof node === 'object') {
    const record = node as { props?: { testID?: string; accessibilityRole?: string }; children?: unknown };
    if (record.props?.testID) acc.push(record.props.testID);
    if (typeof record.children === 'string') acc.push(record.children);
    collect(record.children, acc);
  }
  return acc;
}

function offlineClient() {
  const fetchImpl = jest.fn(async () => {
    throw new TypeError('Network request failed');
  });
  return createStockbotClient(
    { serverUrl: 'https://stockbot.example', password: 'correct-horse' },
    { fetchImpl },
  );
}

function jsonResponse(body: unknown): Response {
  return {
    ok: true,
    status: 200,
    text: async () => JSON.stringify(body),
    json: async () => body,
  } as Response;
}

describe('mobile server notice banner', () => {
  afterEach(async () => {
    await act(async () => {
      notifyApiSuccess();
    });
    mockMemory.clear();
    jest.restoreAllMocks();
  });

  it.each([
    {
      name: 'queue',
      heading: 'To confirm',
      draw: () => render(framed(<QueueScreen client={offlineClient()} warning="" />)),
    },
    {
      name: 'scan',
      heading: 'Putting away',
      draw: () => render(framed(<ScanScreen client={offlineClient()} warning="" />)),
    },
    {
      name: 'household',
      heading: 'Household',
      draw: async () => {
        mockMemory.set(SERVER_URL_KEY, 'https://stockbot.example');
        mockMemory.set(PASSWORD_KEY, 'correct-horse');
        jest.spyOn(globalThis, 'fetch').mockResolvedValue(jsonResponse({ required: true, username: 'pantry' }));
        const view = await render(framed(
          <SessionProvider>
            <HouseholdRoute />
          </SessionProvider>,
        ));
        await view.findByText('Household');
        await act(async () => {
          raiseOfflineBanner();
        });
        return view;
      },
    },
    {
      name: 'sign-in',
      heading: 'Stockbot',
      draw: async () => {
        const view = await render(framed(
          <SessionProvider>
            <SignInRoute />
          </SessionProvider>,
        ));
        await view.findByText('Stockbot');
        await act(async () => {
          raiseOfflineBanner();
        });
        return view;
      },
    },
  ])('pins a boxed red banner above the $name heading', async ({ heading, draw }) => {
    const view = await draw();
    const banner = await view.findByTestId('server-notice-banner');
    expect(view.getByText(OFFLINE_SENTENCE)).toBeTruthy();
    const style = StyleSheet.flatten(banner.props.style);
    expect(style.backgroundColor).toBe(SERVER_NOTICE_RED);
    expect(style.backgroundColor).toBe('#EF4444');
    expect(style.borderRadius).toBe(4);
    expect(style.marginTop).toBe(TOP);
    expect(style.marginHorizontal).toBe(SERVER_NOTICE_INSET);
    expect(style.marginHorizontal).toBe(20);
    const order = collect(view.toJSON());
    const bannerAt = order.indexOf('server-notice-banner');
    const headingAt = order.indexOf(heading);
    expect(bannerAt).toBeGreaterThanOrEqual(0);
    expect(headingAt).toBeGreaterThan(bannerAt);
  });

  it('replaces the auth sentence with the queue sentence', async () => {
    const view = await render(<ServerNoticeBanner text={BUSY_SENTENCE} />);
    await act(async () => {
      raiseAuthNotConfiguredBanner();
    });
    expect(view.getByText("Sign-in isn't set up yet.")).toBeTruthy();
    await act(async () => {
      applyHttpResponse(503, 'busy');
    });
    expect(view.queryByText("Sign-in isn't set up yet.")).toBeNull();
    expect(view.getByText(BUSY_SENTENCE)).toBeTruthy();
  });
});
