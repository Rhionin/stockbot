import type { ReactElement } from 'react';
import { StyleSheet, View } from 'react-native';
import { SafeAreaProvider } from 'react-native-safe-area-context';
import { render } from '@testing-library/react-native';
import { OFFLINE_SENTENCE, notifyApiSuccess, raiseOfflineBanner } from '../../../frontend/src/api/contract';
import { createStockbotClient } from '../api/client';
import { ConnectionForm } from '../screens/ConnectionForm';
import { QueueScreen } from '../screens/QueueScreen';
import { ScanScreen } from '../screens/ScanScreen';
import { SERVER_NOTICE_RED } from './ServerNoticeBanner';

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

describe('mobile server notice banner', () => {
  afterEach(() => {
    notifyApiSuccess();
  });

  it.each([
    {
      name: 'queue',
      heading: 'To confirm',
      marginTop: TOP,
      draw: () => render(framed(<QueueScreen client={offlineClient()} warning="" />)),
    },
    {
      name: 'scan',
      heading: 'Putting away',
      marginTop: TOP,
      draw: () => render(framed(<ScanScreen client={offlineClient()} warning="" />)),
    },
    {
      name: 'household',
      heading: 'Household',
      marginTop: 0,
      draw: () => {
        raiseOfflineBanner();
        return render(framed(
          <View style={{ paddingTop: TOP }}>
            <ConnectionForm
              heading="Household"
              lede="Saved on this phone."
              submitLabel="Save"
              onSubmit={async () => undefined}
            />
          </View>,
        ));
      },
    },
  ])('pins a boxed red banner above the $name heading', async ({ heading, marginTop, draw }) => {
    const view = await draw();
    const banner = await view.findByTestId('server-notice-banner');
    expect(view.getByText(OFFLINE_SENTENCE)).toBeTruthy();
    const style = StyleSheet.flatten(banner.props.style);
    expect(style.backgroundColor).toBe(SERVER_NOTICE_RED);
    expect(style.backgroundColor).toBe('#EF4444');
    expect(style.borderRadius).toBe(4);
    expect(style.marginTop).toBe(marginTop);
    const order = collect(view.toJSON());
    const bannerAt = order.indexOf('server-notice-banner');
    const headingAt = order.indexOf(heading);
    expect(bannerAt).toBeGreaterThanOrEqual(0);
    expect(headingAt).toBeGreaterThan(bannerAt);
  });
});
