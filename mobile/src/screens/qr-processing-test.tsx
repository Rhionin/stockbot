import { act, fireEvent, waitFor, within } from '@testing-library/react-native';
import { ApiError, type StockbotClient } from '../api/client';
import type { ProcessingNotice, ScanEntry } from '@stockbot/shared/types';
import { createLiveHub, LiveHubProvider, type LiveHub } from '../live/LiveEvents';
import { setForeground } from '../test/fakeForeground';
import { renderScreen } from '../test/renderScreen';
import { QueueScreen } from './QueueScreen';

jest.mock('../live/foreground', () => jest.requireActual('../test/fakeForeground'));

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

const qr = (overrides: Partial<ScanEntry> = {}): ScanEntry =>
  scan({
    id: 'qr-1',
    barcode: 'https://example.com/groceries/oats?token=synthetic-secret',
    status: 'qr',
    productId: null,
    product: undefined,
    ...overrides,
  });

const notice = (overrides: Partial<ProcessingNotice> = {}): ProcessingNotice => ({
  id: 'lookup-1',
  userId: 'user-1',
  barcode: '999888777666',
  direction: 'stock_in',
  scannedAt: '2026-10-16T12:00:00Z',
  ...overrides,
});

function client(rows: ScanEntry[], overrides: Partial<StockbotClient> = {}): StockbotClient {
  return {
    events: { url: 'https://stockbot.example/api/events', headers: {}, rejected: jest.fn() },
    getScannerConfig: jest.fn(async () => ({
      stockInBarcode: 'IN',
      stockOutBarcode: 'OUT',
      currentMode: 'stock_in' as const,
      connected: true,
    })),
    setScannerMode: jest.fn(async (mode) => ({ mode })),
    listPending: jest.fn(async () => rows),
    cancelScan: jest.fn(async (id: string) => ({ ...(rows.find((row) => row.id === id) ?? scan({ id })), status: 'cancelled' as const })),
    batchCommit: jest.fn(async () => ({ updatedCount: 0, committedIds: [] })),
    getInventoryList: jest.fn(async () => []),
    updateScan: jest.fn(async () => { throw new Error('updateScan not stubbed'); }),
    listInstances: jest.fn(async () => []),
    undoMove: jest.fn(async () => undefined),
    ...overrides,
  } as unknown as StockbotClient;
}

async function open(api: StockbotClient, onRescan: () => void = () => undefined) {
  const hub: LiveHub = createLiveHub();
  const screen = await renderScreen(
    <LiveHubProvider hub={hub}>
      <QueueScreen client={api} warning="" onRescan={onRescan} />
    </LiveHubProvider>,
  );
  await waitFor(() => expect(api.listPending).toHaveBeenCalled());
  const send = (event: string, body: unknown) =>
    act(async () => hub.emit({ kind: 'message', event, data: JSON.stringify(body) }));
  return { screen, send };
}

beforeEach(() => setForeground(true));

describe('QR misread cards', () => {
  it('shows the host of a URL, never the payload, and offers rescan and dismiss', async () => {
    const onRescan = jest.fn();
    const { screen } = await open(client([qr(), scan()]), onRescan);

    const card = within(await screen.findByTestId('qr-scan-qr-1'));
    expect(card.getByText('This was a QR code, not the barcode')).toBeTruthy();
    expect(card.getByText('example.com')).toBeTruthy();
    expect(screen.queryByText(/synthetic-secret/)).toBeNull();
    expect(screen.getByLabelText('This was a QR code, not the barcode. example.com')).toBeTruthy();

    await fireEvent.press(card.getByRole('button', { name: 'Rescan the barcode' }));
    expect(onRescan).toHaveBeenCalledTimes(1);
  });

  it.each([
    ['WIFI:T:WPA;S:SyntheticNet;P:synthetic-pass-123;;', 'Wi-Fi code', 'synthetic-pass-123'],
    ['BEGIN:VCARD\nFN:Synthetic Person\nEND:VCARD', 'Contact card', 'Synthetic Person'],
    ['otpauth://totp/Synthetic:user?secret=SYNTHETICSECRET123', 'Authenticator code', 'SYNTHETICSECRET123'],
    ['buy milk', 'QR code', 'buy milk'],
  ])('labels %j as %s without leaking it', async (payload, label, leaked) => {
    const { screen } = await open(client([qr({ barcode: payload })]));
    await screen.findByText(label);
    expect(screen.queryByText(new RegExp(leaked))).toBeNull();
  });

  it('dismisses through the API and keeps a normal scan with the same barcode', async () => {
    const shared = 'https://example.com/shared';
    const rows = [qr({ barcode: shared }), scan({ id: 'scan-2', barcode: shared })];
    const api = client(rows);
    const { screen } = await open(api);
    await screen.findByTestId('qr-scan-qr-1');

    await fireEvent.press(screen.getByRole('button', { name: 'Dismiss' }));

    await waitFor(() => expect(screen.queryByTestId('qr-scan-qr-1')).toBeNull());
    expect(api.cancelScan).toHaveBeenCalledWith('qr-1');
    expect(screen.getByTestId('scan-scan-2')).toBeTruthy();
  });

  it('keeps the card and says why when the dismiss fails', async () => {
    const api = client([qr()], {
      cancelScan: jest.fn(async () => { throw new ApiError(500, 'Server said no.'); }),
    });
    const { screen } = await open(api);
    await fireEvent.press(await screen.findByRole('button', { name: 'Dismiss' }));

    expect(await screen.findByText('Server said no.')).toBeTruthy();
    expect(screen.getByTestId('qr-scan-qr-1')).toBeTruthy();
  });

  it('adds and removes a card from live scan events', async () => {
    const { screen, send } = await open(client([scan()]));
    await screen.findByText('Synthetic oats');
    expect(screen.queryByTestId('qr-scan-qr-9')).toBeNull();

    await send('scan', qr({ id: 'qr-9' }));
    expect(await screen.findByTestId('qr-scan-qr-9')).toBeTruthy();

    await send('scan', qr({ id: 'qr-9', status: 'cancelled' }));
    await waitFor(() => expect(screen.queryByTestId('qr-scan-qr-9')).toBeNull());
  });

  it('keeps a QR misread out of Confirm all', async () => {
    const api = client([qr(), scan(), scan({ id: 'scan-2', barcode: '555' })]);
    const { screen } = await open(api);
    await fireEvent.press(await screen.findByLabelText('Confirm all'));
    await waitFor(() => expect(api.batchCommit).toHaveBeenCalledWith(['scan-1', 'scan-2']));
  });

  it('shows the card in its own direction tab', async () => {
    const { screen } = await open(client([qr({ direction: 'stock_out' }), scan()]));
    await screen.findByText('Synthetic oats');
    expect(screen.queryByTestId('qr-scan-qr-1')).toBeNull();
  });

  it('is not the empty queue', async () => {
    const { screen } = await open(client([qr()]));
    await screen.findByTestId('qr-scan-qr-1');
    expect(screen.queryByText(/Nothing is waiting/)).toBeNull();
  });
});

describe('processing cards', () => {
  it('appears on scan_processing in a polite live region, ahead of the batches', async () => {
    const { screen, send } = await open(client([scan()]));
    await screen.findByText('Synthetic oats');

    await send('scan_processing', notice());

    const card = within(await screen.findByTestId('processing-lookup-1'));
    expect(card.getByText('Looking up product')).toBeTruthy();
    expect(card.getByText('Barcode: 999888777666')).toBeTruthy();
    expect(card.getByText('Checking the open food database…')).toBeTruthy();
    expect(screen.getByLabelText('Scan 999888777666 processing')).toBeTruthy();
    const status = card.getByTestId('processing-status-lookup-1');
    expect(status.props.role).toBe('status');
    expect(status.props.accessibilityLiveRegion).toBe('polite');
    expect(within(status).getByText('Looking up product')).toBeTruthy();
  });

  it('settles on the follow-up scan and stays gone on a late duplicate event', async () => {
    const { screen, send } = await open(client([]));
    await send('scan_processing', notice());
    await screen.findByTestId('processing-lookup-1');

    const done = scan({ id: 'scan-9', barcode: '999888777666' });
    await send('scan', done);
    await waitFor(() => expect(screen.queryByTestId('processing-lookup-1')).toBeNull());
    expect(await screen.findByTestId('scan-scan-9')).toBeTruthy();

    await send('scan_processing', notice());
    await send('scan', done);
    expect(screen.queryByTestId('processing-lookup-1')).toBeNull();
    expect(screen.getAllByTestId('scan-scan-9')).toHaveLength(1);
  });

  it('does not stack a second card for a replayed notice', async () => {
    const { screen, send } = await open(client([]));
    await send('scan_processing', notice());
    await send('scan_processing', notice());
    expect(await screen.findAllByTestId('processing-lookup-1')).toHaveLength(1);
  });

  it('settles only the lookup that matches the scan', async () => {
    const { screen, send } = await open(client([]));
    await send('scan_processing', notice());
    await send('scan_processing', notice({ id: 'lookup-2', barcode: '123' }));

    await send('scan', scan({ id: 'scan-3', barcode: '123' }));

    await waitFor(() => expect(screen.queryByTestId('processing-lookup-2')).toBeNull());
    expect(screen.getByTestId('processing-lookup-1')).toBeTruthy();
  });

  it('leaves the card for a committed or cancelled scan with the same barcode', async () => {
    const { screen, send } = await open(client([]));
    await send('scan_processing', notice());
    await send('scan', scan({ id: 'old', barcode: '999888777666', status: 'committed' }));
    await send('scan', scan({ id: 'old2', barcode: '999888777666', status: 'cancelled' }));
    expect(screen.getByTestId('processing-lookup-1')).toBeTruthy();
  });

  it('clears the card on scan_processing_failed and shows the message as an alert', async () => {
    const { screen, send } = await open(client([]));
    await send('scan_processing', notice());
    await screen.findByTestId('processing-lookup-1');

    await send('scan_processing_failed', { id: 'lookup-1', barcode: '999888777666', message: "Couldn't look up that barcode." });

    await waitFor(() => expect(screen.queryByTestId('processing-lookup-1')).toBeNull());
    expect(screen.getByRole('alert', { name: "Couldn't look up that barcode." })).toBeTruthy();

    await send('scan_processing', notice());
    expect(screen.queryByTestId('processing-lookup-1')).toBeNull();
  });

  it('adds nothing for a failure with an unknown id', async () => {
    const { screen, send } = await open(client([scan()]));
    await screen.findByText('Synthetic oats');

    await send('scan_processing_failed', { id: 'never-seen', barcode: '1', message: 'Should not show.' });

    expect(screen.queryByText('Should not show.')).toBeNull();
    expect(screen.queryByTestId('lookup-failure')).toBeNull();
    expect(screen.queryByTestId('processing-never-seen')).toBeNull();
  });

  it('shows a lookup only in the tab of its direction and for this user', async () => {
    const { screen, send } = await open(client([]));
    await send('scan_processing', notice({ id: 'out', direction: 'stock_out' }));
    await send('scan_processing', notice({ id: 'other', userId: 'user-2' }));
    expect(screen.queryByTestId('processing-out')).toBeNull();
    expect(screen.queryByTestId('processing-other')).toBeNull();
  });

  it('ignores a malformed processing event', async () => {
    const { screen, send } = await open(client([]));
    await send('scan_processing', { barcode: 5 });
    expect(screen.queryByText('Looking up product')).toBeNull();
  });
});
