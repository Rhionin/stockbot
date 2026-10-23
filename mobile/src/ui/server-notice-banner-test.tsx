import type { ReactElement } from 'react';
import { StyleSheet, View } from 'react-native';
import { SafeAreaProvider } from 'react-native-safe-area-context';
import { act, fireEvent, render } from '@testing-library/react-native';
import { AUTH_SETUP_SENTENCE, BUSY_SENTENCE, OFFLINE_SENTENCE, applyHttpResponse, notifyApiSuccess, raiseAuthNotConfiguredBanner, raiseOfflineBanner } from '../../../frontend/src/api/contract';
import type { ScanEntry } from '../../../frontend/src/types';
import authNotConfigured from '../../../testdata/contract/auth_not_configured.json';
import { createStockbotClient } from '../api/client';
import { PASSWORD_KEY, SERVER_URL_KEY } from '../auth/credentials';
import HouseholdRoute from '../app/(app)/household';
import SignInRoute from '../app/sign-in';
import { SessionProvider } from '../session';
import { QueueScreen } from '../screens/QueueScreen';
import { SCAN_BAND_PADDING_TOP, ScanScreen } from '../screens/ScanScreen';
import { ServerNoticeBanner, SERVER_NOTICE_INSET } from './ServerNoticeBanner';

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

type LaidOut = {
  parent: LaidOut | null;
  props: { style?: unknown };
  children: readonly (LaidOut | string)[];
};

const HORIZONTAL_KEYS = [
  'padding',
  'paddingHorizontal',
  'paddingLeft',
  'paddingRight',
  'paddingStart',
  'paddingEnd',
  'paddingInline',
  'paddingInlineStart',
  'paddingInlineEnd',
  'margin',
  'marginHorizontal',
  'marginLeft',
  'marginRight',
  'marginStart',
  'marginEnd',
  'marginInline',
  'marginInlineStart',
  'marginInlineEnd',
] as const;

const TOP_KEYS = [
  'padding',
  'paddingVertical',
  'paddingTop',
  'paddingBlock',
  'paddingBlockStart',
  'margin',
  'marginVertical',
  'marginTop',
  'marginBlock',
  'marginBlockStart',
] as const;

function padding(style: unknown) {
  // Flatten keeps shorthand and logical keys as written, so each one counts.
  const flat = (StyleSheet.flatten(style as never) ?? {}) as Record<string, unknown>;
  const amount = (key: string) => (typeof flat[key] === 'number' ? flat[key] as number : 0);
  const sum = (keys: readonly string[]) => keys.reduce((total, key) => total + amount(key), 0);
  return {
    top: sum(TOP_KEYS),
    horizontal: sum(HORIZONTAL_KEYS),
  };
}

function ancestors(node: LaidOut): LaidOut[] {
  const chain: LaidOut[] = [];
  let current = node.parent;
  while (current) {
    chain.push(current);
    current = current.parent;
  }
  return chain;
}

function expectBannerClearOfPadding(banner: LaidOut) {
  for (const ancestor of ancestors(banner)) {
    const flat = (StyleSheet.flatten(ancestor.props.style as never) ?? {}) as Record<string, unknown>;
    for (const [key, value] of Object.entries(flat)) {
      if (!key.startsWith('padding') && !key.startsWith('margin')) continue;
      // A percent string is an inset, and +4 with -4 does not cancel.
      expect({ key, value }).toEqual({ key, value: 0 });
    }
  }
}

function paddedAncestor(node: LaidOut): LaidOut | undefined {
  return ancestors(node).find((ancestor) => padding(ancestor.props.style).horizontal > 0);
}

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

function jsonResponse(body: unknown, status = 200): Response {
  return {
    ok: status >= 200 && status < 300,
    status,
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
    {
      name: 'auth setup',
      heading: AUTH_SETUP_SENTENCE,
      draw: async () => {
        mockMemory.set(SERVER_URL_KEY, 'https://stockbot.example');
        mockMemory.set(PASSWORD_KEY, 'correct-horse');
        jest.spyOn(globalThis, 'fetch').mockResolvedValue(jsonResponse({
          error: "Sign-in isn't set up yet.",
          code: authNotConfigured.code,
        }, 503));
        const view = await render(framed(
          <SessionProvider>
            <SignInRoute />
          </SessionProvider>,
        ));
        await view.findByText(AUTH_SETUP_SENTENCE);
        await act(async () => {
          raiseOfflineBanner();
        });
        return view;
      },
    },
  ])('pins a boxed red banner above the $name heading', async ({ name, heading, draw }) => {
    const view = await draw();
    const banner = await view.findByTestId('server-notice-banner');
    expect(view.getByText(OFFLINE_SENTENCE)).toBeTruthy();
    const style = StyleSheet.flatten(banner.props.style);
    expect(style.backgroundColor).toBe('#ffeeee');
    expect(style.borderColor).toBe('#EF4444');
    expect(style.borderWidth).toBe(1);
    const sentence = StyleSheet.flatten(view.getByText(OFFLINE_SENTENCE).props.style);
    expect(sentence.color).toBe('#000000');
    expect(style.borderRadius).toBe(4);
    expect(style.marginTop).toBe(TOP);
    expect(style.marginHorizontal).toBe(SERVER_NOTICE_INSET);
    expect(style.marginHorizontal).toBe(20);
    expectBannerClearOfPadding(banner);
    if (name === 'scan') {
      const band = paddedAncestor(view.getByText('Putting away'));
      expect(band).toBeTruthy();
      expect(padding(band?.props.style).top).toBe(SCAN_BAND_PADDING_TOP);
      expect(ancestors(banner)).not.toContain(band);
    }
    if (name === 'queue') {
      const body = paddedAncestor(view.getByText('To confirm'));
      expect(body).toBeTruthy();
      expect(padding(body?.props.style).top).toBe(0);
    }
    if (name === 'household' || name === 'sign-in') {
      const host = banner.parent;
      const slot = host?.children.find((child) => typeof child !== 'string' && child !== banner);
      expect(slot).toBeTruthy();
      expect(padding(slot && typeof slot !== 'string' ? slot.props.style : undefined).top).toBe(0);
      expect(ancestors(banner)).not.toContain(slot);
    }
    const order = collect(view.toJSON());
    const bannerAt = order.indexOf('server-notice-banner');
    const headingAt = order.indexOf(heading);
    expect(bannerAt).toBeGreaterThanOrEqual(0);
    expect(headingAt).toBeGreaterThan(bannerAt);
  });

  it('pins the queue when the busy sentence is up and the connection is fine', async () => {
    const waiting: ScanEntry = {
      id: 'scan-1',
      userId: 'user-1',
      barcode: '111222333444',
      scannedAt: '2026-10-16T12:00:00Z',
      direction: 'stock_in',
      unitCount: 1,
      expiresAt: null,
      status: 'pending',
      productId: 'prod-1',
      product: { id: 'prod-1', name: 'Oat milk', category: 'Dairy', unitOfMeasure: 'carton' },
      committedAt: null,
      createdAt: '2026-10-16T12:00:00Z',
    };
    const fetchImpl = jest.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      const method = init?.method ?? 'GET';
      if (url.includes('/commit') && method === 'POST') {
        return jsonResponse({ error: BUSY_SENTENCE, code: 'busy' }, 503);
      }
      if (url.includes('status=pending')) return jsonResponse([waiting]);
      if (url.includes('/api/inventory')) return jsonResponse([]);
      return jsonResponse({ error: 'unexpected' });
    });
    const view = await render(framed(
      <QueueScreen client={createStockbotClient(
        { serverUrl: 'https://stockbot.example', password: 'correct-horse' },
        { fetchImpl },
      )} warning="" />,
    ));
    await fireEvent.press(await view.findByLabelText('Confirm Oat milk'));
    const banner = await view.findByTestId('server-notice-banner');
    expect(view.getByText(BUSY_SENTENCE)).toBeTruthy();
    expect(view.queryByText(OFFLINE_SENTENCE)).toBeNull();
    expectBannerClearOfPadding(banner);
    const body = paddedAncestor(view.getByText('To confirm'));
    expect(padding(body?.props.style).top).toBe(0);
  });

  it('treats a percent padding as an inset', async () => {
    const view = await render(
      <View style={{ padding: '4%' }}>
        <ServerNoticeBanner text={OFFLINE_SENTENCE} />
      </View>,
    );
    const banner = view.getByTestId('server-notice-banner');
    expect(() => expectBannerClearOfPadding(banner)).toThrow();
  });

  it('rejects offsetting margins', async () => {
    const view = await render(
      <View style={{ marginLeft: 4, marginRight: -4 }}>
        <ServerNoticeBanner text={OFFLINE_SENTENCE} />
      </View>,
    );
    const banner = view.getByTestId('server-notice-banner');
    expect(() => expectBannerClearOfPadding(banner)).toThrow();
  });

  it('hides the exclamation icon from TalkBack', async () => {
    const view = await render(<ServerNoticeBanner text={OFFLINE_SENTENCE} />);
    const mark = view.getByText('!', { includeHiddenElements: true });
    expect(mark.parent?.props.accessibilityElementsHidden).toBe(true);
    expect(mark.parent?.props.importantForAccessibility).toBe('no-hide-descendants');
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
