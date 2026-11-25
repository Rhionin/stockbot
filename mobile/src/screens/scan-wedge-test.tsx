import { act, waitFor } from '@testing-library/react-native';
import type { ScanEntry, ScannerConfig } from '@stockbot/shared/types';
import type { StockbotClient } from '../api/client';
import { LiveHubProvider, createLiveHub } from '../live/LiveEvents';
import { renderScreen } from '../test/renderScreen';
import type { WedgeInputProps } from '../scanner/wedgeInput';
import { ScanScreen } from './ScanScreen';

const mockHandles: { wedge: WedgeInputProps; camera: (result: { data: string }) => void } = {
  wedge: { onScan: () => undefined },
  camera: () => undefined,
};

jest.mock('../scanner/WedgeInput', () => ({
  WedgeInput: (props: WedgeInputProps) => {
    mockHandles.wedge = props;
    return null;
  },
}));

jest.mock('expo-camera', () => {
  // eslint-disable-next-line @typescript-eslint/no-require-imports
  const { View } = require('react-native');
  return {
    CameraView: ({ onBarcodeScanned }: { onBarcodeScanned?: (result: { data: string }) => void }) => {
      mockHandles.camera = onBarcodeScanned ?? mockHandles.camera;
      return <View testID="camera" />;
    },
    useCameraPermissions: () => [{ granted: true, canAskAgain: true, status: 'granted' }, jest.fn(), jest.fn()],
  };
});

jest.mock('expo-haptics', () => ({
  notificationAsync: jest.fn(async () => undefined),
  NotificationFeedbackType: { Success: 'success', Warning: 'warning', Error: 'error' },
}));

const created = {
  id: 'scan-1',
  barcode: '0123456789012',
  direction: 'stock_in',
  status: 'pending',
  product: { id: 'prod-1', name: 'Oat milk' },
} as unknown as ScanEntry;

function client() {
  return {
    getScannerConfig: jest.fn(async () => ({
      stockInBarcode: 'STOCK_IN',
      stockOutBarcode: 'STOCK_OUT',
      currentMode: 'stock_in' as const,
      connected: false,
    })),
    setScannerMode: jest.fn(async (mode: 'stock_in' | 'stock_out') => ({ mode })),
    createScan: jest.fn(async () => created),
  };
}

async function ready(api: ReturnType<typeof client>, focused = true) {
  const screen = await renderScreen(<ScanScreen client={api as unknown as StockbotClient} warning="" focused={focused} />);
  await screen.findByLabelText('Switch to using up');
  return screen;
}

describe('ScanScreen keyboard-wedge input', () => {
  beforeEach(() => jest.useFakeTimers({ doNotFake: ['nextTick', 'setImmediate', 'queueMicrotask'] }));
  afterEach(() => jest.useRealTimers());

  it('makes a scan with the same call a camera read makes', async () => {
    const api = client();
    const screen = await ready(api);

    await act(async () => mockHandles.wedge.onScan('0123456789012'));

    await waitFor(() => expect(api.createScan).toHaveBeenCalledWith('0123456789012', 'stock_in'));
    expect(await screen.findByText('Added Oat milk')).toBeTruthy();
  });

  it('takes the same repeat suppression as the camera, whichever device read the code first', async () => {
    const api = client();
    await ready(api);

    await act(async () => mockHandles.camera({ data: '0123456789012' }));
    await act(async () => mockHandles.wedge.onScan('0123456789012'));
    expect(api.createScan).toHaveBeenCalledTimes(1);

    await act(async () => mockHandles.wedge.onScan('9999999999994'));
    expect(api.createScan).toHaveBeenCalledTimes(2);

    jest.advanceTimersByTime(2_500);
    await act(async () => mockHandles.wedge.onScan('9999999999994'));
    expect(api.createScan).toHaveBeenCalledTimes(3);
  });

  it('switches the mode on the control codes and posts no scan', async () => {
    const api = client();
    const screen = await ready(api);

    await act(async () => mockHandles.wedge.onScan('STOCK_OUT'));

    await waitFor(() => expect(api.setScannerMode).toHaveBeenCalledWith('stock_out'));
    expect(api.createScan).not.toHaveBeenCalled();
    expect(await screen.findByText('Switched to using up')).toBeTruthy();
    expect(screen.getByLabelText('Switch to putting away')).toBeTruthy();

    jest.advanceTimersByTime(2_500);
    await act(async () => mockHandles.wedge.onScan('STOCK_IN'));
    await waitFor(() => expect(api.setScannerMode).toHaveBeenCalledWith('stock_in'));
  });

  it('stamps a scan with the direction the control code just chose', async () => {
    const api = client();
    await ready(api);

    await act(async () => mockHandles.wedge.onScan('STOCK_OUT'));
    await act(async () => mockHandles.wedge.onScan('0123456789012'));

    await waitFor(() => expect(api.createScan).toHaveBeenCalledWith('0123456789012', 'stock_out'));
  });

  it('keeps the mode a control code chose when a refresh already in flight answers with the old mode', async () => {
    const hub = createLiveHub();
    let answer!: (config: ScannerConfig) => void;
    let confirm!: (reply: { mode: 'stock_out' }) => void;
    const api = client();
    const screen = await renderScreen(
      <LiveHubProvider hub={hub}>
        <ScanScreen client={api as unknown as StockbotClient} warning="" />
      </LiveHubProvider>,
    );
    await screen.findByLabelText('Switch to using up');
    (api.getScannerConfig as jest.Mock).mockReturnValueOnce(new Promise<ScannerConfig>((done) => {
      answer = done;
    }));
    (api.setScannerMode as jest.Mock).mockReturnValueOnce(new Promise((done) => {
      confirm = done;
    }));

    await act(async () => hub.emit({ kind: 'refresh', reason: 'reconnect' }));
    await act(async () => mockHandles.wedge.onScan('STOCK_OUT'));
    await act(async () => confirm({ mode: 'stock_out' }));
    expect(screen.getByLabelText('Switch to putting away')).toBeTruthy();
    await act(async () => answer({ stockInBarcode: 'STOCK_IN', stockOutBarcode: 'STOCK_OUT', currentMode: 'stock_in', connected: false }));

    expect(screen.getByLabelText('Switch to putting away')).toBeTruthy();
  });

  it('reads only while the screen is in front, and stops the camera when it is not', async () => {
    const api = client();
    const screen = await ready(api, false);

    expect(mockHandles.wedge.active).toBe(false);
    expect(screen.queryByTestId('camera')).toBeNull();
  });
});
