import { act, fireEvent, waitFor } from '@testing-library/react-native';
import { StyleSheet } from 'react-native';
import { mobileChrome } from '@stockbot/shared/brandTokens';
import type { ProcessingNotice, ScanEntry } from '@stockbot/shared/types';
import { ApiError, type StockbotClient } from '../api/client';
import { createLiveHub, LiveHubProvider, type LiveHub } from '../live/LiveEvents';
import { CAPTURE_HIGHLIGHT_MS } from '../scanner/useCaptureHighlight';
import type { WedgeInputProps } from '../scanner/wedgeInput';
import { setForeground } from '../test/fakeForeground';
import { renderScreen } from '../test/renderScreen';
import { QueueScreen } from './QueueScreen';

const mockWedge: { props: WedgeInputProps } = { props: { onScan: () => undefined } };
const mockScrolled = jest.fn();

jest.mock('../live/foreground', () => jest.requireActual('../test/fakeForeground'));
jest.mock('../scanner/WedgeInput', () => ({
  WedgeInput: (props: WedgeInputProps) => {
    mockWedge.props = props;
    return null;
  },
}));
jest.mock('../scanner/scrollCardIntoView', () => ({
  scrollCardIntoView: (testId: string) => mockScrolled(testId),
}));

const scan = (overrides: Partial<ScanEntry> = {}): ScanEntry => ({
  id: 'scan-1',
  userId: 'user-1',
  barcode: '111222333444',
  scannedAt: '2026-10-16T12:00:00Z',
  direction: 'stock_in',
  unitCount: 1,
  expiresAt: null,
  status: 'pending',
  productId: 'prod-1',
  product: { id: 'prod-1', name: 'Synthetic oats', category: 'Pantry', unitOfMeasure: 'bag' },
  committedAt: null,
  createdAt: '2026-10-16T12:00:00Z',
  ...overrides,
});

const qr = (): ScanEntry =>
  scan({ id: 'qr-1', barcode: 'https://example.com/oats', status: 'qr', productId: null, product: undefined });

const lookup = (): ProcessingNotice => ({
  id: 'lookup-1',
  userId: 'user-1',
  barcode: '999888777666',
  direction: 'stock_in',
  scannedAt: '2026-10-16T12:00:00Z',
});

function client(rows: ScanEntry[], overrides: Partial<StockbotClient> = {}) {
  let currentMode: 'stock_in' | 'stock_out' = 'stock_in';
  return {
    events: { url: 'https://stockbot.example/api/events', headers: {}, rejected: jest.fn() },
    getScannerConfig: jest.fn(async () => ({
      stockInBarcode: 'IN',
      stockOutBarcode: 'OUT',
      currentMode,
      connected: true,
    })),
    setScannerMode: jest.fn(async (mode: 'stock_in' | 'stock_out') => {
      currentMode = mode;
      return { mode };
    }),
    listPending: jest.fn(async () => rows),
    createScan: jest.fn(async (barcode: string) => scan({ id: 'scan-new', barcode })),
    cancelScan: jest.fn(),
    batchCommit: jest.fn(),
    getInventoryList: jest.fn(async () => []),
    updateScan: jest.fn(),
    listInstances: jest.fn(async () => []),
    undoMove: jest.fn(),
    ...overrides,
  } as unknown as StockbotClient;
}

async function open(api: StockbotClient, focused = true) {
  const hub: LiveHub = createLiveHub();
  const screen = await renderScreen(
    <LiveHubProvider hub={hub}>
      <QueueScreen client={api} warning="" focused={focused} />
    </LiveHubProvider>,
  );
  await waitFor(() => expect(api.listPending).toHaveBeenCalled());
  await waitFor(() => expect(api.getScannerConfig).toHaveBeenCalled());
  const send = (event: string, body: unknown) =>
    act(async () => hub.emit({ kind: 'message', event, data: JSON.stringify(body) }));
  const wedge = (barcode: string) => act(async () => mockWedge.props.onScan(barcode));
  return { screen, send, wedge };
}

const wash = (view: { props: { style?: unknown } }) =>
  StyleSheet.flatten(view.props.style as Parameters<typeof StyleSheet.flatten>[0])?.backgroundColor;

beforeEach(() => {
  setForeground(true);
  mockScrolled.mockClear();
  jest.useFakeTimers({ doNotFake: ['nextTick', 'setImmediate', 'queueMicrotask'] });
});

afterEach(() => jest.useRealTimers());

describe('queue keyboard-wedge input', () => {
  it('makes the scan with the web call and the selected direction, then reloads the queue', async () => {
    const api = client([scan()]);
    const { wedge } = await open(api);
    const loads = (api.listPending as jest.Mock).mock.calls.length;

    await wedge('0123456789012');

    expect(api.createScan).toHaveBeenCalledWith('0123456789012', 'stock_in');
    await waitFor(() => expect((api.listPending as jest.Mock).mock.calls.length).toBeGreaterThan(loads));
  });

  it('drops a repeat of the same code until the cooldown passes, and lets other codes through', async () => {
    const api = client([]);
    const { wedge } = await open(api);

    await wedge('0123456789012');
    await wedge('0123456789012');
    expect(api.createScan).toHaveBeenCalledTimes(1);

    await wedge('5555555555554');
    expect(api.createScan).toHaveBeenCalledTimes(2);

    await act(async () => jest.advanceTimersByTime(2_500));
    await wedge('0123456789012');
    expect(api.createScan).toHaveBeenCalledTimes(3);
  });

  it('switches the mode on the control codes, posts no scan, and stamps the next scan with it', async () => {
    const api = client([]);
    const { wedge } = await open(api);

    await wedge('OUT');

    expect(api.setScannerMode).toHaveBeenCalledWith('stock_out');
    expect(api.createScan).not.toHaveBeenCalled();

    await wedge('0123456789012');
    expect(api.createScan).toHaveBeenCalledWith('0123456789012', 'stock_out');

    await act(async () => jest.advanceTimersByTime(2_500));
    await wedge('IN');
    expect(api.setScannerMode).toHaveBeenLastCalledWith('stock_in');
  });

  it('asks for the scanner setup when it has not arrived, and says so when it cannot', async () => {
    const api = client([], {
      getScannerConfig: jest.fn(async () => { throw new ApiError(500, 'Server said no.'); }),
    });
    const { screen, wedge } = await open(api);

    await wedge('0123456789012');

    expect(await screen.findByText('Server said no.')).toBeTruthy();
    expect(api.createScan).not.toHaveBeenCalled();
  });

  it('says the scan did not save, lifts the highlight, and lets the same code be tried again', async () => {
    const createScan = jest.fn()
      .mockRejectedValueOnce(new ApiError(500, 'Server said no.'))
      .mockImplementation(async (barcode: string) => scan({ id: 'scan-new', barcode }));
    const api = client([scan()], { createScan });
    const { screen, wedge } = await open(api);

    await wedge('111222333444');

    expect(await screen.findByText('Server said no.')).toBeTruthy();
    expect(wash(screen.getByTestId('scan-scan-1'))).not.toBe(mobileChrome.successWash);

    await wedge('111222333444');
    expect(createScan).toHaveBeenCalledTimes(2);
  });

  it('only listens while the queue is in front and no sheet covers it', async () => {
    const { screen } = await open(client([scan({ barcode: '111222333444' })]));
    expect(mockWedge.props.active).toBe(true);

    await fireEvent.press(screen.getByRole('button', { name: 'Find an item' }));

    await waitFor(() => expect(mockWedge.props.active).toBe(false));
  });

  it('does not listen on a queue that is not in front', async () => {
    await open(client([]), false);

    expect(mockWedge.props.active).toBe(false);
  });
});

describe('just-captured card', () => {
  it('washes the card of the scanned code, scrolls it into view, and lets go after a few seconds', async () => {
    const { screen, wedge } = await open(client([scan(), scan({ id: 'scan-2', barcode: '777666555444' })]));
    expect(wash(screen.getByTestId('scan-scan-1'))).not.toBe(mobileChrome.successWash);

    await wedge('111222333444');

    expect(wash(screen.getByTestId('scan-scan-1'))).toBe(mobileChrome.successWash);
    expect(wash(screen.getByTestId('scan-scan-2'))).not.toBe(mobileChrome.successWash);
    expect(mockScrolled).toHaveBeenCalledWith('scan-scan-1');

    await act(async () => jest.advanceTimersByTime(CAPTURE_HIGHLIGHT_MS + 100));
    expect(wash(screen.getByTestId('scan-scan-1'))).not.toBe(mobileChrome.successWash);
  });

  it('follows the code onto the card a live event adds, and scrolls to it', async () => {
    const { screen, send, wedge } = await open(client([scan()]));

    await wedge('0123456789012');
    expect(mockScrolled).not.toHaveBeenCalled();
    await send('scan', scan({ id: 'scan-new', barcode: '0123456789012' }));

    expect(wash(await screen.findByTestId('scan-scan-new'))).toBe(mobileChrome.successWash);
    expect(mockScrolled).toHaveBeenCalledWith('scan-scan-new');
  });

  it('washes a QR card and a lookup card for the code just scanned', async () => {
    const { screen, send, wedge } = await open(client([qr()]));
    await send('scan_processing', lookup());

    await wedge('999888777666');
    expect(wash(screen.getByTestId('processing-lookup-1'))).toBe(mobileChrome.successWash);
    expect(mockScrolled).toHaveBeenLastCalledWith('processing-lookup-1');

    await wedge('https://example.com/oats');
    expect(wash(screen.getByTestId('qr-scan-qr-1'))).toBe(mobileChrome.successWash);
    expect(mockScrolled).toHaveBeenLastCalledWith('qr-scan-qr-1');
  });

  it('washes nothing for a control code', async () => {
    const { wedge } = await open(client([scan({ barcode: 'OUT' })]));

    await wedge('OUT');

    expect(mockScrolled).not.toHaveBeenCalled();
  });
});
