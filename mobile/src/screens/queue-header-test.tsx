import { useEffect, useState } from 'react';
import { act, fireEvent, waitFor } from '@testing-library/react-native';
import { DIRECTION_HINT, SCANNER_CONNECTED, SCANNER_DISCONNECTED } from '@stockbot/shared/queueHeader';
import type { ScanDirection, ScanEntry, ScannerConfig } from '@stockbot/shared/types';
import type { StockbotClient } from '../api/client';
import { createLiveHub, LiveHubProvider } from '../live/LiveEvents';
import { setForeground } from '../test/fakeForeground';
import { renderScreen } from '../test/renderScreen';
import { QueueScreen } from './QueueScreen';

jest.mock('../live/foreground', () => jest.requireActual('../test/fakeForeground'));

const scan = (id: string, name: string, direction: ScanDirection | null): ScanEntry => ({
  id,
  userId: 'user-1',
  barcode: `code-${id}`,
  scannedAt: '2026-10-16T12:00:00Z',
  direction,
  unitCount: 1,
  expiresAt: null,
  status: 'pending',
  productId: `prod-${id}`,
  product: { id: `prod-${id}`, name, category: 'Pantry', unitOfMeasure: 'bag' },
  committedAt: null,
  createdAt: '2026-10-16T12:00:00Z',
});

const config = (overrides: Partial<ScannerConfig> = {}): ScannerConfig => ({
  stockInBarcode: 'IN',
  stockOutBarcode: 'OUT',
  currentMode: 'stock_in',
  connected: true,
  ...overrides,
});

const rows = [
  scan('a', 'Synthetic oats', 'stock_in'),
  scan('b', 'Synthetic rice', 'stock_out'),
  scan('c', 'Synthetic beans', 'stock_out'),
  scan('d', 'Synthetic flour', null),
];

function client(overrides: Partial<StockbotClient> = {}): StockbotClient {
  return {
    events: { url: 'https://stockbot.example/api/events', headers: {}, rejected: jest.fn() },
    getScannerConfig: jest.fn(async () => config()),
    setScannerMode: jest.fn(async (mode: ScanDirection) => ({ mode })),
    listPending: jest.fn(async () => rows),
    getInventoryList: jest.fn(async () => []),
    ...overrides,
  } as unknown as StockbotClient;
}

let setFocused: (focused: boolean) => void = () => undefined;

function Harness({ api }: { api: StockbotClient }) {
  const [focused, set] = useState(true);
  useEffect(() => {
    setFocused = set;
  }, [set]);
  return <QueueScreen client={api} warning="" focused={focused} />;
}

function render(api: StockbotClient) {
  const hub = createLiveHub();
  const screen = renderScreen(
    <LiveHubProvider hub={hub}>
      <Harness api={api} />
    </LiveHubProvider>,
  );
  return { hub, screen };
}

beforeEach(() => setForeground(true));

describe('queue header', () => {
  it('names the screen, the direction and the count on each tab', async () => {
    const screen = await render(client()).screen;

    expect(await screen.findByText('Synthetic oats')).toBeTruthy();
    expect(screen.getByRole('header', { name: 'Scan queue' })).toBeTruthy();
    expect(screen.getByLabelText('Scan direction').props.role).toBe('tablist');
    expect(screen.getByRole('tab', { name: 'Stock in 1 to review', selected: true })).toBeTruthy();
    expect(screen.getByRole('tab', { name: 'Stock out 3 to review', selected: false })).toBeTruthy();
    expect(screen.getByText(DIRECTION_HINT.stock_in)).toBeTruthy();
    expect(screen.queryByText('Synthetic rice')).toBeNull();
  });

  it('starts on the direction the server reports', async () => {
    const screen = await render(client({ getScannerConfig: jest.fn(async () => config({ currentMode: 'stock_out' })) })).screen;

    expect(await screen.findByText('Synthetic rice')).toBeTruthy();
    expect(screen.getByText('Synthetic flour')).toBeTruthy();
    expect(screen.queryByText('Synthetic oats')).toBeNull();
    expect(screen.getByText(DIRECTION_HINT.stock_out)).toBeTruthy();
  });

  it('switches the scanner mode and filters the list when a tab is chosen', async () => {
    const api = client();
    const screen = await render(api).screen;
    await screen.findByText('Synthetic oats');

    await fireEvent.press(screen.getByRole('tab', { name: 'Stock out 3 to review' }));

    await waitFor(() => expect(api.setScannerMode).toHaveBeenCalledWith('stock_out'));
    expect(screen.getByRole('tab', { name: 'Stock out 3 to review', selected: true })).toBeTruthy();
    expect(screen.getByText('Synthetic beans')).toBeTruthy();
    expect(screen.queryByText('Synthetic oats')).toBeNull();
    expect(screen.getByText(DIRECTION_HINT.stock_out)).toBeTruthy();
  });

  it('does not call the server when the chosen tab is already selected', async () => {
    const api = client();
    const screen = await render(api).screen;
    await screen.findByText('Synthetic oats');

    await fireEvent.press(screen.getByRole('tab', { name: 'Stock in 1 to review' }));

    expect(api.setScannerMode).not.toHaveBeenCalled();
  });

  it('goes back to the server direction and says so when the switch fails', async () => {
    const api = client({ setScannerMode: jest.fn(async () => { throw new Error('offline'); }) });
    const screen = await render(api).screen;
    await screen.findByText('Synthetic oats');

    await fireEvent.press(screen.getByRole('tab', { name: 'Stock out 3 to review' }));

    expect(await screen.findByText('offline')).toBeTruthy();
    await waitFor(() => expect(screen.getByRole('tab', { name: 'Stock in 1 to review', selected: true })).toBeTruthy());
  });

  it('follows a scanner_mode event from another device', async () => {
    const { hub, screen } = render(client());
    await (await screen).findByText('Synthetic oats');

    await act(async () => hub.emit({ kind: 'message', event: 'scanner_mode', data: '"stock_out"' }));

    expect((await screen).getByRole('tab', { name: 'Stock out 3 to review', selected: true })).toBeTruthy();
    expect((await screen).getByText('Synthetic rice')).toBeTruthy();
  });

  it('keeps a live direction when a config response that started earlier arrives late', async () => {
    let release: (value: ScannerConfig) => void = () => undefined;
    const late = new Promise<ScannerConfig>((resolve) => { release = resolve; });
    const getScannerConfig = jest.fn().mockReturnValueOnce(late).mockResolvedValue(config());
    const { hub, screen } = render(client({ getScannerConfig }));
    const view = await screen;
    await view.findByText('Synthetic oats');

    await act(async () => hub.emit({ kind: 'message', event: 'scanner_mode', data: '"stock_out"' }));
    await act(async () => release(config({ currentMode: 'stock_in', connected: false })));

    expect(view.getByRole('tab', { name: 'Stock out 3 to review', selected: true })).toBeTruthy();
    expect(view.getByText(SCANNER_DISCONNECTED)).toBeTruthy();
  });

  it('keeps a chosen direction when a config response that started earlier arrives late', async () => {
    let release: (value: ScannerConfig) => void = () => undefined;
    const late = new Promise<ScannerConfig>((resolve) => { release = resolve; });
    const getScannerConfig = jest.fn().mockReturnValueOnce(late).mockResolvedValue(config());
    const view = await render(client({ getScannerConfig })).screen;
    await view.findByText('Synthetic oats');

    await fireEvent.press(view.getByRole('tab', { name: 'Stock out 3 to review' }));
    await act(async () => release(config({ currentMode: 'stock_in' })));

    expect(view.getByRole('tab', { name: 'Stock out 3 to review', selected: true })).toBeTruthy();
  });

  it('reads the scanner status aloud as a live status', async () => {
    const view = await render(client()).screen;

    const status = await view.findByText(SCANNER_CONNECTED);
    expect(status.props.role).toBe('status');
    expect(status.props.accessibilityLiveRegion).toBe('polite');
  });

  it('says what to do when the scanner is disconnected', async () => {
    const view = await render(client({ getScannerConfig: jest.fn(async () => config({ connected: false })) })).screen;

    expect(await view.findByText(SCANNER_DISCONNECTED)).toBeTruthy();
    expect(view.queryByText(SCANNER_CONNECTED)).toBeNull();
  });

  it('shows no scanner status until the server has said', async () => {
    const view = await render(client({ getScannerConfig: jest.fn(async () => { throw new Error('offline'); }) })).screen;

    await view.findByText('Synthetic oats');
    expect(view.queryByTestId('scanner-status')).toBeNull();
  });

  it('refreshes the scanner status on a refresh signal and on a hint', async () => {
    const api = client();
    const { hub, screen } = render(api);
    await (await screen).findByText(SCANNER_CONNECTED);
    const before = (api.getScannerConfig as jest.Mock).mock.calls.length;

    await act(async () => hub.emit({ kind: 'refresh', reason: 'reconnect' }));
    expect((api.getScannerConfig as jest.Mock).mock.calls.length).toBe(before + 1);
    await act(async () => hub.emit({ kind: 'message', event: 'hint', data: '{"type":"inventory"}' }));
    expect((api.getScannerConfig as jest.Mock).mock.calls.length).toBe(before + 2);
  });

  describe('polling the scanner status', () => {
    beforeEach(() => jest.useFakeTimers());
    afterEach(() => jest.useRealTimers());

    it('polls while the queue is in front and the app is awake, and stops otherwise', async () => {
      const api = client();
      await render(api).screen;
      const calls = () => (api.getScannerConfig as jest.Mock).mock.calls.length;
      await act(async () => { await jest.advanceTimersByTimeAsync(0); });
      const first = calls();
      expect(first).toBeGreaterThan(0);

      await act(async () => { await jest.advanceTimersByTimeAsync(2000); });
      expect(calls()).toBe(first + 1);

      await act(async () => setForeground(false));
      const asleep = calls();
      await act(async () => { await jest.advanceTimersByTimeAsync(10_000); });
      expect(calls()).toBe(asleep);

      await act(async () => setForeground(true));
      await act(async () => { await jest.advanceTimersByTimeAsync(0); });
      expect(calls()).toBe(asleep + 1);

      await act(async () => setFocused(false));
      const away = calls();
      await act(async () => { await jest.advanceTimersByTimeAsync(10_000); });
      expect(calls()).toBe(away);
    });
  });
});
