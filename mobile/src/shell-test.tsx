import { act, fireEvent } from '@testing-library/react-native';
import type { ReactElement } from 'react';
import { Dimensions, Platform, StyleSheet } from 'react-native';
import type { BottomTabBarProps } from 'expo-router/tabs';
import type { AuthSession } from '@stockbot/shared/contract';
import AppLayout from './app/(app)/_layout';
import LandingRoute from './app/(app)/index';
import SignInRoute from './app/sign-in';
import type { StockbotClient } from './api/client';
import { HouseholdScreen } from './screens/HouseholdScreen';
import { QueueScreen } from './screens/QueueScreen';
import { ScanScreen } from './screens/ScanScreen';
import { renderScreen } from './test/renderScreen';
import { DESKTOP_MIN_WIDTH, PAGE_COLUMN_MAX_WIDTH, isDesktopLayout } from './ui/layout';
import { ShelfTabBar } from './ui/ShelfTabBar';

jest.mock('expo-router', () => {
  // eslint-disable-next-line @typescript-eslint/no-require-imports
  const { Text: MockText } = require('react-native');
  const Tabs = ({ children }: { children?: unknown }) => <>{children}</>;
  function Screen({ name, options }: { name: string; options: { title?: string; href?: null } }) {
    return <MockText testID={`screen-${name}`}>{options.href === null ? `hidden ${name}` : `${name}:${options.title}`}</MockText>;
  }
  Tabs.Screen = Screen;
  return {
    Tabs,
    Redirect: () => null,
    router: { replace: jest.fn() },
    useFocusEffect: jest.fn(),
  };
});

jest.mock('expo-constants', () => ({
  __esModule: true,
  default: { expoConfig: { version: '1.0.0' } },
}));

jest.mock('expo-camera', () => ({
  CameraView: () => null,
  useCameraPermissions: () => [{ granted: false, canAskAgain: true, status: 'undetermined' }, jest.fn(), jest.fn()],
}));

jest.mock('@/routes/QueueRoute', () => {
  // eslint-disable-next-line @typescript-eslint/no-require-imports
  const { Text: MockText } = require('react-native');
  return { QueueRoute: () => <MockText>queue route</MockText> };
});

jest.mock('@/routes/ScanRoute', () => {
  // eslint-disable-next-line @typescript-eslint/no-require-imports
  const { Text: MockText } = require('react-native');
  return { ScanRoute: () => <MockText>scan route</MockText> };
});

const mockSession = {
  status: 'signedOut' as const,
  serverUrl: 'https://stockbot.example',
  error: '',
  signIn: jest.fn(),
  recheck: jest.fn(),
};

jest.mock('@/session', () => ({
  useSession: () => mockSession,
}));

const originalPlatform = Object.getOwnPropertyDescriptor(Platform, 'OS')!;
const originalWindow = Dimensions.get('window');

function setPlatform(os: string) {
  Object.defineProperty(Platform, 'OS', { configurable: true, get: () => os });
}

async function setWindow(width: number) {
  await act(async () => {
    Dimensions.set({ window: { ...originalWindow, width, height: 800 } });
  });
}

afterEach(async () => {
  Object.defineProperty(Platform, 'OS', originalPlatform);
  await setWindow(originalWindow.width);
});

function tabBarProps(): BottomTabBarProps {
  const routes = [
    { key: 'index-key', name: 'index' },
    { key: 'scan-key', name: 'scan' },
    { key: 'queue-key', name: 'queue' },
  ];
  const titles: Record<string, { title: string; hidden?: boolean }> = {
    'index-key': { title: 'Queue' },
    'scan-key': { title: 'Scan' },
    'queue-key': { title: 'Hidden', hidden: true },
  };
  return {
    state: { index: 0, routes },
    descriptors: Object.fromEntries(routes.map((route) => [route.key, { options: titles[route.key] }])),
    emitter: { emit: jest.fn(() => ({ defaultPrevented: false })) },
    navigateToTab: jest.fn(),
    insets: { top: 0, right: 0, bottom: 0, left: 0 },
  } as unknown as BottomTabBarProps;
}

describe('breakpoint', () => {
  it('is desktop only on web at or above the breakpoint', () => {
    setPlatform('web');
    expect(isDesktopLayout(DESKTOP_MIN_WIDTH)).toBe(true);
    expect(isDesktopLayout(DESKTOP_MIN_WIDTH - 1)).toBe(false);
    expect(isDesktopLayout(1280)).toBe(true);
    setPlatform('android');
    expect(isDesktopLayout(1280)).toBe(false);
  });
});

describe('ShelfTabBar', () => {
  it('is a bottom tab bar on a narrow web window', async () => {
    setPlatform('web');
    await setWindow(390);
    const screen = await renderScreen(<ShelfTabBar {...tabBarProps()} />);

    expect(screen.queryByText('Stockbot')).toBeNull();
    expect(screen.queryByLabelText('Sections')).toBeNull();
    expect(screen.getAllByRole('tab').map((tab) => tab.props.accessibilityLabel)).toEqual(['Queue', 'Scan']);
  });

  it('is a top navigation with the wordmark on a desktop web window', async () => {
    setPlatform('web');
    await setWindow(1280);
    const props = tabBarProps();
    const screen = await renderScreen(<ShelfTabBar {...props} />);

    expect(screen.getByText('Stockbot')).toBeTruthy();
    expect(screen.getByLabelText('Sections')).toBeTruthy();
    expect(screen.getAllByRole('tab').map((tab) => tab.props.accessibilityLabel)).toEqual(['Queue', 'Scan']);

    await fireEvent.press(screen.getByLabelText('Scan'));
    expect(props.navigateToTab).toHaveBeenCalledWith('scan-key');
  });

  it('stays a bottom tab bar on a wide native screen', async () => {
    setPlatform('android');
    await setWindow(1280);
    const screen = await renderScreen(<ShelfTabBar {...tabBarProps()} />);

    expect(screen.queryByText('Stockbot')).toBeNull();
    expect(screen.queryByLabelText('Sections')).toBeNull();
  });
});

describe('AppLayout tabs', () => {
  it('orders the web tabs queue first and hides the phone queue route', async () => {
    setPlatform('web');
    const screen = await renderScreen(<AppLayout />);

    expect(screen.getAllByTestId(/^screen-/).map((node) => node.props.children)).toEqual([
      'index:Queue',
      'scan:Scan',
      'household:Household',
      'hidden queue',
    ]);
  });

  it('keeps Scan as the first native tab and hides the web scan route', async () => {
    setPlatform('android');
    const screen = await renderScreen(<AppLayout />);

    expect(screen.getAllByTestId(/^screen-/).map((node) => node.props.children)).toEqual([
      'index:Scan',
      'queue:Queue',
      'household:Household',
      'hidden scan',
    ]);
  });
});

describe('landing route', () => {
  it('lands on the queue on web', async () => {
    setPlatform('web');
    const screen = await renderScreen(<LandingRoute />);
    expect(screen.getByText('queue route')).toBeTruthy();
  });

  it('lands on scan on native', async () => {
    setPlatform('android');
    const screen = await renderScreen(<LandingRoute />);
    expect(screen.getByText('scan route')).toBeTruthy();
  });
});

describe('sign-in copy', () => {
  it('drops the phone wording on web', async () => {
    setPlatform('web');
    const screen = await renderScreen(<SignInRoute />);
    expect(screen.queryByText('Scan the pantry from the phone.')).toBeNull();
    expect(screen.getByText('Sign in with your email and password.')).toBeTruthy();
  });

  it('keeps the phone wording on native', async () => {
    setPlatform('android');
    const screen = await renderScreen(<SignInRoute />);
    expect(screen.getByText('Scan the pantry from the phone.')).toBeTruthy();
  });
});

const account: AuthSession = {
  user: { id: 'u1', email: 'ada@example.test', name: 'Ada' },
  kind: 'account',
  memberships: [{ householdId: 'hh-1', name: 'Home', role: 'owner', deletedAt: null }],
  households: [{ id: 'hh-1', name: 'Home', role: 'owner', deletedAt: null }],
  activeHouseholdId: 'hh-1',
  operator: false,
};

function household(): ReactElement {
  return <HouseholdScreen topInset={0} serverUrl="https://stockbot.example" account={account} onSwitch={jest.fn()} onSignOut={jest.fn()} />;
}

function columnOf(style: unknown) {
  return (StyleSheet.flatten(style as never) ?? {}) as { maxWidth?: number; alignSelf?: string };
}

describe('Household on web', () => {
  it('hides the server row and shows only the app version', async () => {
    setPlatform('web');
    await setWindow(1280);
    const screen = await renderScreen(household());

    expect(screen.queryByText('Server')).toBeNull();
    expect(screen.queryByText('https://stockbot.example')).toBeNull();
    expect(screen.getByText('Version 1.0.0')).toBeTruthy();
    expect(screen.queryByText(/Update /)).toBeNull();
    expect(screen.queryByText(/Runtime /)).toBeNull();
  });

  it('centers a bounded column on a desktop window and fills a narrow one', async () => {
    setPlatform('web');
    await setWindow(1280);
    const wide = await renderScreen(household());
    expect(columnOf(wide.getByRole('header').parent!.props.style)).toMatchObject({
      maxWidth: PAGE_COLUMN_MAX_WIDTH,
      alignSelf: 'center',
    });
    await wide.unmount();

    await setWindow(390);
    const narrow = await renderScreen(household());
    expect(columnOf(narrow.getByRole('header').parent!.props.style).maxWidth).toBeUndefined();
  });

  it('keeps the server row and build stamp on native', async () => {
    setPlatform('android');
    await setWindow(1280);
    const screen = await renderScreen(household());

    expect(screen.getByText('Server')).toBeTruthy();
    expect(screen.getByText('https://stockbot.example')).toBeTruthy();
    expect(screen.getByText(/^Update /)).toBeTruthy();
    expect(columnOf(screen.getByRole('header').parent!.props.style).maxWidth).toBeUndefined();
  });
});

function emptyClient(): StockbotClient {
  return {
    getScannerConfig: jest.fn(async () => ({
      stockInBarcode: 'STOCK_IN',
      stockOutBarcode: 'STOCK_OUT',
      currentMode: 'stock_in' as const,
      connected: false,
    })),
    listPending: jest.fn(async () => []),
    getInventoryList: jest.fn(async () => []),
    updateScan: jest.fn(async () => { throw new Error('updateScan not stubbed'); }),
    listInstances: jest.fn(async () => []),
    undoMove: jest.fn(async () => undefined),
  } as unknown as StockbotClient;
}

describe('Queue column', () => {
  it('is bounded and centered on a desktop window only', async () => {
    setPlatform('web');
    await setWindow(1280);
    const wide = await renderScreen(<QueueScreen client={emptyClient()} warning="" focused />);
    const wideBody = wide.getByText('Scan queue').parent!;
    expect(columnOf(wideBody.props.style)).toMatchObject({ maxWidth: PAGE_COLUMN_MAX_WIDTH, alignSelf: 'center' });
    await wide.unmount();

    await setWindow(390);
    const narrow = await renderScreen(<QueueScreen client={emptyClient()} warning="" focused />);
    expect(columnOf(narrow.getByText('Scan queue').parent!.props.style).maxWidth).toBeUndefined();
  });
});

describe('Scan stage', () => {
  it('is bounded to the column with a fixed ratio on a desktop window', async () => {
    setPlatform('web');
    await setWindow(1280);
    const wide = await renderScreen(<ScanScreen client={emptyClient()} warning="" />);
    const stage = StyleSheet.flatten(wide.getByTestId('scan-stage').props.style) as { aspectRatio?: number; flexGrow?: number };
    expect(stage.aspectRatio).toBeGreaterThan(1);
    expect(stage.flexGrow).toBe(0);
    await wide.unmount();

    await setWindow(390);
    const narrow = await renderScreen(<ScanScreen client={emptyClient()} warning="" />);
    const phoneStage = StyleSheet.flatten(narrow.getByTestId('scan-stage').props.style) as { aspectRatio?: number; flex?: number };
    expect(phoneStage.aspectRatio).toBeUndefined();
    expect(phoneStage.flex).toBe(1);
  });
});
