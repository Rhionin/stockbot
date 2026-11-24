import { act, fireEvent, waitFor } from '@testing-library/react-native';
import * as Haptics from 'expo-haptics';
import { OFFLINE_SENTENCE } from '@stockbot/shared/contract';
import type { ScanEntry, ScannerConfig } from '@stockbot/shared/types';
import { createStockbotClient, type StockbotClient } from '../api/client';
import { LiveHubProvider, createLiveHub } from '../live/LiveEvents';
import { renderScreen } from '../test/renderScreen';
import { ScanScreen } from './ScanScreen';

jest.mock('expo-camera', () => {
  // eslint-disable-next-line @typescript-eslint/no-require-imports
  const React = require('react');
  // eslint-disable-next-line @typescript-eslint/no-require-imports
  const { View } = require('react-native');
  return {
    CameraView: ({ onBarcodeScanned }: { onBarcodeScanned?: (result: { data: string }) => void }) => {
      React.useEffect(() => {
        onBarcodeScanned?.({ data: '111222333444' });
        onBarcodeScanned?.({ data: '111222333444' });
      }, [onBarcodeScanned]);
      return <View />;
    },
    useCameraPermissions: () => [{ granted: true, canAskAgain: true, status: 'granted' }, jest.fn(), jest.fn()],
  };
});

jest.mock('expo-haptics', () => ({
  notificationAsync: jest.fn(async () => undefined),
  NotificationFeedbackType: { Success: 'success', Warning: 'warning', Error: 'error' },
}));

const created: ScanEntry = {
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

function client(): StockbotClient {
  return {
    events: { url: 'https://stockbot.example/api/events', headers: {}, rejected: jest.fn() },
    getConfig: jest.fn(),
    login: jest.fn(),
    logout: jest.fn(),
    getAuthSession: jest.fn(),
    switchHousehold: jest.fn(),
    getScannerConfig: jest.fn(async () => ({
      stockInBarcode: 'STOCK_IN',
      stockOutBarcode: 'STOCK_OUT',
      currentMode: 'stock_in' as const,
      connected: false,
    })),
    setScannerMode: jest.fn(async (mode: 'stock_in' | 'stock_out') => ({ mode })),
    createScan: jest.fn(async () => created),
    listPending: jest.fn(async () => []),
    commitScan: jest.fn(),
    commitBackfill: jest.fn(),
    cancelScan: jest.fn(),
    batchCommit: jest.fn(),
    getInventoryList: jest.fn(async () => []),
  };
}

describe('ScanScreen', () => {
  it('gives haptic feedback and posts one scan when the camera reads a code twice', async () => {
    const api = client();
    const screen = await renderScreen(<ScanScreen client={api} warning="" />);

    await waitFor(() => expect(api.createScan).toHaveBeenCalledTimes(1));
    expect(api.createScan).toHaveBeenCalledWith('111222333444', 'stock_in');
    expect(Haptics.notificationAsync).toHaveBeenCalledWith(Haptics.NotificationFeedbackType.Success);
    expect(await screen.findByText('Added Oat milk')).toBeTruthy();
  });

  it('switches the shared scanner mode', async () => {
    const api = client();
    const screen = await renderScreen(<ScanScreen client={api} warning="" />);

    await fireEvent.press(await screen.findByLabelText('Switch to using up'));

    await waitFor(() => expect(api.setScannerMode).toHaveBeenCalledWith('stock_out'));
    expect(await screen.findByText('Switched to using up')).toBeTruthy();
  });

  it('shows the offline banner when the scanner setup cannot be reached', async () => {
    const fetchImpl = jest.fn(async () => {
      throw new TypeError('Network request failed');
    });
    const api = createStockbotClient(
      { serverUrl: 'https://stockbot.example', token: 'sbs_test' },
      { fetchImpl },
    );
    const screen = await renderScreen(<ScanScreen client={api} warning="" />);

    expect(await screen.findByTestId('server-notice-banner')).toBeTruthy();
    expect(screen.getByText(OFFLINE_SENTENCE)).toBeTruthy();
    expect(screen.queryByText('Network request failed')).toBeNull();
    expect(screen.queryByText('The scanner setup did not load.')).toBeNull();
  });

  it('follows the scanner mode another device sets', async () => {
    const hub = createLiveHub();
    const api = client();
    const screen = await renderScreen(
      <LiveHubProvider hub={hub}>
        <ScanScreen client={api} warning="" />
      </LiveHubProvider>,
    );
    expect(await screen.findByLabelText('Switch to using up')).toBeTruthy();

    await act(async () => hub.emit({ kind: 'message', event: 'scanner_mode', data: '"stock_out"' }));

    expect(screen.getByLabelText('Switch to putting away')).toBeTruthy();
    await act(async () => hub.emit({ kind: 'message', event: 'scanner_mode', data: '"stock_in"' }));
    expect(screen.getByLabelText('Switch to using up')).toBeTruthy();
    await act(async () => hub.emit({ kind: 'message', event: 'scanner_mode', data: '"sideways"' }));
    expect(screen.getByLabelText('Switch to using up')).toBeTruthy();
  });

  it('keeps a live mode change when the setup request answers late with the old mode', async () => {
    const hub = createLiveHub();
    let answer!: (config: ScannerConfig) => void;
    const api = client();
    (api.getScannerConfig as jest.Mock).mockReturnValueOnce(new Promise<ScannerConfig>((done) => {
      answer = done;
    }));
    const screen = await renderScreen(
      <LiveHubProvider hub={hub}>
        <ScanScreen client={api} warning="" />
      </LiveHubProvider>,
    );

    await act(async () => hub.emit({ kind: 'message', event: 'scanner_mode', data: '"stock_out"' }));
    await act(async () => answer({ stockInBarcode: 'STOCK_IN', stockOutBarcode: 'STOCK_OUT', currentMode: 'stock_in', connected: false }));

    expect(screen.getByLabelText('Switch to putting away')).toBeTruthy();
  });

  it('asks for the mode again when the stream comes back or the fallback poll fires', async () => {
    const hub = createLiveHub();
    const api = client();
    const screen = await renderScreen(
      <LiveHubProvider hub={hub}>
        <ScanScreen client={api} warning="" />
      </LiveHubProvider>,
    );
    expect(await screen.findByLabelText('Switch to using up')).toBeTruthy();
    (api.getScannerConfig as jest.Mock).mockResolvedValue({
      stockInBarcode: 'STOCK_IN',
      stockOutBarcode: 'STOCK_OUT',
      currentMode: 'stock_out',
      connected: false,
    });

    await act(async () => hub.emit({ kind: 'refresh', reason: 'reconnect' }));

    expect(await screen.findByLabelText('Switch to putting away')).toBeTruthy();
  });
});
