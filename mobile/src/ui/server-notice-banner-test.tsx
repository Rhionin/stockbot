import type { ReactElement } from 'react';
import { StyleSheet, View } from 'react-native';
import { SafeAreaProvider } from 'react-native-safe-area-context';
import { act, fireEvent, render } from '@testing-library/react-native';
import { AUTH_SETUP_SENTENCE, BUSY_SENTENCE, OFFLINE_SENTENCE, UPDATING_SENTENCE, applyHttpResponse, notifyApiSuccess, raiseAuthNotConfiguredBanner, raiseOfflineBanner } from '@stockbot/shared/contract';
import type { ScanEntry } from '@stockbot/shared/types';
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

const EDGE_KEYS = [
  'padding',
  'paddingTop',
  'paddingVertical',
  'paddingBlock',
  'paddingBlockStart',
  'paddingLeft',
  'paddingRight',
  'paddingStart',
  'paddingEnd',
  'paddingHorizontal',
  'paddingInline',
  'paddingInlineStart',
  'paddingInlineEnd',
  'margin',
  'marginTop',
  'marginVertical',
  'marginBlock',
  'marginBlockStart',
  'marginLeft',
  'marginRight',
  'marginStart',
  'marginEnd',
  'marginHorizontal',
  'marginInline',
  'marginInlineStart',
  'marginInlineEnd',
] as const;

function styleLayers(style: unknown): Record<string, unknown>[] {
  if (style == null || typeof style === 'boolean') return [];
  if (Array.isArray(style)) return style.flatMap((entry) => styleLayers(entry));
  if (typeof style === 'number') {
    return [(StyleSheet.flatten(style as never) ?? {}) as Record<string, unknown>];
  }
  if (typeof style === 'object') return [style as Record<string, unknown>];
  return [];
}

function insetAmount(value: unknown): number | string {
  if (value === undefined) return 0;
  if (typeof value === 'number') return value;
  return 'set';
}

// React Native lays a column out from the top. These main-axis values move
// the banner downward even when every padding edge is zero. On a row the
// same values only slide children sideways, so they stay legal.
const PUSHES_OFF_TOP = new Set([
  'flex-end',
  'center',
  'space-between',
  'space-around',
  'space-evenly',
]);

function pushesBannerOffTop(chain: readonly LaidOut[]): boolean {
  return chain.some((ancestor) => {
    const flat = (StyleSheet.flatten(ancestor.props.style as never) ?? {}) as Record<string, unknown>;
    const direction = flat.flexDirection;
    if (direction === 'column-reverse') return true;
    const vertical = direction == null || direction === 'column';
    return vertical && typeof flat.justifyContent === 'string' && PUSHES_OFF_TOP.has(flat.justifyContent);
  });
}

function expectBannerClearOfPadding(banner: LaidOut) {
  const chain = ancestors(banner);
  expect(pushesBannerOffTop(chain)).toBe(false);
  for (const ancestor of chain) {
    for (const layer of styleLayers(ancestor.props.style)) {
      for (const key of EDGE_KEYS) {
        expect(insetAmount(layer[key])).toBe(0);
      }
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

  it.each([
    { name: 'percent padding', style: { padding: '4%' } },
    { name: 'offsetting margins', style: { marginLeft: 4, marginRight: -4 } },
    { name: 'padding shorthand', style: { padding: 4 } },
    { name: 'paddingStart', style: { paddingStart: 4 } },
    { name: 'marginHorizontal', style: { marginHorizontal: 4 } },
    { name: 'paddingVertical', style: { paddingVertical: 4 } },
    { name: 'marginVertical', style: { marginVertical: 4 } },
    { name: 'flex-end paddingBottom', style: { justifyContent: 'flex-end', paddingBottom: 12 } },
    { name: 'flex-end marginBottom', style: { justifyContent: 'flex-end', marginBottom: 8 } },
    { name: 'flex-end paddingBlockEnd', style: { justifyContent: 'flex-end', paddingBlockEnd: 4 } },
    { name: 'column flex-end', style: { justifyContent: 'flex-end' } },
    { name: 'column center', style: { justifyContent: 'center' } },
    { name: 'column space-between', style: { justifyContent: 'space-between' } },
    { name: 'column space-around', style: { justifyContent: 'space-around' } },
    { name: 'column space-evenly', style: { justifyContent: 'space-evenly' } },
    { name: 'explicit column center', style: { flexDirection: 'column', justifyContent: 'center' } },
    { name: 'column-reverse flex-end', style: { flexDirection: 'column-reverse', justifyContent: 'flex-end' } },
    { name: 'v24', style: { flexDirection: 'column-reverse' } },
    { name: 'layered column center', style: [{ flexDirection: 'column' }, { justifyContent: 'center' }] },
  ])('rejects $name', async ({ style }) => {
    const view = await render(
      <View style={style as never}>
        <ServerNoticeBanner text={OFFLINE_SENTENCE} />
      </View>,
    );
    expect(() => expectBannerClearOfPadding(view.getByTestId('server-notice-banner'))).toThrow();
  });

  it('allows bottom padding and an unset top', async () => {
    const view = await render(
      <View style={{ paddingBottom: 12, paddingTop: undefined, marginBottom: 8, paddingBlockEnd: 4 }}>
        <ServerNoticeBanner text={OFFLINE_SENTENCE} />
      </View>,
    );
    expectBannerClearOfPadding(view.getByTestId('server-notice-banner'));
  });

  it.each([
    { name: 'row flex-end', style: { flexDirection: 'row', justifyContent: 'flex-end' } },
    { name: 'row center with bottom padding', style: { flexDirection: 'row', justifyContent: 'center', paddingBottom: 12 } },
    { name: 'row-reverse space-between', style: { flexDirection: 'row-reverse', justifyContent: 'space-between' } },
    { name: 'layered row flex-end', style: [{ flexDirection: 'row' }, { justifyContent: 'flex-end' }] },
    { name: 'column flex-start', style: { flexDirection: 'column', justifyContent: 'flex-start' } },
  ])('allows $name', async ({ style }) => {
    const view = await render(
      <View style={style as never}>
        <ServerNoticeBanner text={OFFLINE_SENTENCE} />
      </View>,
    );
    expectBannerClearOfPadding(view.getByTestId('server-notice-banner'));
  });

  it('hides the exclamation icon from TalkBack', async () => {
    const view = await render(<ServerNoticeBanner text={OFFLINE_SENTENCE} />);
    const mark = view.getByText('!', { includeHiddenElements: true });
    expect(mark.parent?.props.accessibilityElementsHidden).toBe(true);
    expect(mark.parent?.props.importantForAccessibility).toBe('no-hide-descendants');
  });

  it('shows updating and clears it after a success', async () => {
    const view = await render(<ServerNoticeBanner />);
    await act(async () => {
      applyHttpResponse(503, 'updating');
    });
    expect(view.getByText(UPDATING_SENTENCE)).toBeTruthy();
    await act(async () => {
      notifyApiSuccess();
    });
    expect(view.queryByText(UPDATING_SENTENCE)).toBeNull();
    expect(view.queryByTestId('server-notice-banner')).toBeNull();
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
