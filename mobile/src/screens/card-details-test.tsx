import { act, fireEvent, waitFor, within } from '@testing-library/react-native';
import {
  ADDED_AND_TAKEN_OUT,
  formatPartialRemoval,
} from '@stockbot/shared/contract';
import {
  CLEARED_EXPIRY_ISO,
  EXPIRY_INVALID,
  EXPIRY_SAVE_FAILURE,
  INSTANCE_AUTOMATIC_LABEL,
  UNIT_COUNT_INVALID,
  UNIT_COUNT_SAVE_FAILURE,
} from '@stockbot/shared/cardDetails';
import { UNDO_TOAST_MS } from '@stockbot/shared/undoToast';
import type { InventoryItem, ItemInstanceWithStatus, ScanEntry, ScannerConfig } from '@stockbot/shared/types';
import { ApiError, type StockbotClient } from '../api/client';
import { createLiveHub, LiveHubProvider, type LiveHub } from '../live/LiveEvents';
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
  product: { id: 'prod-1', name: 'Oat milk', category: 'Dairy', unitOfMeasure: 'carton' },
  committedAt: null,
  createdAt: '2026-10-16T12:00:00Z',
  ...overrides,
});

const household = (instanceCount = 3): InventoryItem[] => [{
  item: {
    id: 'item-1',
    userId: 'user-1',
    productId: 'prod-1',
    product: { id: 'prod-1', name: 'Oat milk', category: 'Dairy', unitOfMeasure: 'carton' },
    targetQuantity: null,
    createdAt: '2026-10-16T12:00:00Z',
  },
  instanceCount,
  nearExpiryCount: 0,
  expiredCount: 0,
  needsAttention: false,
}];

const instance = (id: string, expiresAt: string | null): ItemInstanceWithStatus => ({
  id,
  itemId: 'item-1',
  stockInAt: '2026-10-01T00:00:00Z',
  expiresAt,
  removedAt: null,
  removalReason: null,
  createdAt: '2026-10-01T00:00:00Z',
  expiryStatus: 'ok',
});

function deferred<T>() {
  let resolve!: (value: T) => void;
  let reject!: (reason: unknown) => void;
  const promise = new Promise<T>((done, fail) => {
    resolve = done;
    reject = fail;
  });
  return { promise, resolve, reject };
}

function config(mode: 'stock_in' | 'stock_out'): ScannerConfig {
  return { stockInBarcode: 'IN', stockOutBarcode: 'OUT', currentMode: mode, connected: true };
}

function api(rows: ScanEntry[], overrides: Partial<StockbotClient> = {}): StockbotClient {
  const mode = rows[0]?.direction === 'stock_out' ? 'stock_out' : 'stock_in';
  return {
    events: { url: 'https://stockbot.example/api/events', headers: {}, rejected: jest.fn() },
    getScannerConfig: jest.fn(async () => config(mode)),
    setScannerMode: jest.fn(async (next) => ({ mode: next })),
    listPending: jest.fn(async () => rows),
    getInventoryList: jest.fn(async () => household()),
    commitScan: jest.fn(async () => scan({ status: 'committed' })),
    commitBackfill: jest.fn(async () => ({
      removed: 1,
      backfilled: 1,
      itemId: 'item-1',
      backfill: { stockInMoveId: 'move-in', stockOutMoveId: 'move-out' },
    })),
    cancelScan: jest.fn(async () => scan({ status: 'cancelled' })),
    batchCommit: jest.fn(),
    updateScan: jest.fn(async (id, input) => scan({ id, ...input, expiresAt: null })),
    listInstances: jest.fn(async () => []),
    undoMove: jest.fn(async () => undefined),
    ...overrides,
  } as unknown as StockbotClient;
}

function show(client: StockbotClient, hub: LiveHub = createLiveHub()) {
  return renderScreen(
    <LiveHubProvider hub={hub}>
      <QueueScreen client={client} warning="" focused />
    </LiveHubProvider>,
  );
}

const SAVE_PAUSE_MS = 400;

async function pause(ms = SAVE_PAUSE_MS) {
  await act(async () => {
    await new Promise((done) => setTimeout(done, ms));
  });
}

function sendScan(hub: LiveHub, next: ScanEntry) {
  hub.emit({ kind: 'message', event: 'scan', data: JSON.stringify(next) });
}

describe('unit count on a card', () => {
  it('saves a stepper burst once, with the latest count, through PATCH', async () => {
    const client = api([scan()]);
    const screen = await show(client);
    await screen.findByText('Oat milk');

    await fireEvent.press(screen.getByLabelText('Increase unit count'));
    await fireEvent.press(screen.getByLabelText('Increase unit count'));

    expect(screen.getByLabelText('Unit count').props.value).toBe('3');
    await waitFor(() => expect(client.updateScan).toHaveBeenCalledTimes(1));
    expect(client.updateScan).toHaveBeenCalledWith('scan-1', { unitCount: 3 });
  });

  it('will not step below one', async () => {
    const client = api([scan()]);
    const screen = await show(client);
    await screen.findByText('Oat milk');

    expect(screen.getByLabelText('Decrease unit count').props.accessibilityState.disabled).toBe(true);
    await fireEvent.press(screen.getByLabelText('Decrease unit count'));
    await pause();

    expect(client.updateScan).not.toHaveBeenCalled();
    expect(screen.getByLabelText('Unit count').props.value).toBe('1');
  });

  it('saves a typed whole number', async () => {
    const client = api([scan()]);
    const screen = await show(client);
    await screen.findByText('Oat milk');

    await fireEvent.changeText(screen.getByLabelText('Unit count'), '6');

    await waitFor(() => expect(client.updateScan).toHaveBeenCalledWith('scan-1', { unitCount: 6 }));
  });

  it.each(['0', '2.5', '-3', 'abc'])('does not PATCH an invalid count (%s) and says why', async (typed) => {
    const client = api([scan()]);
    const screen = await show(client);
    await screen.findByText('Oat milk');

    await fireEvent.changeText(screen.getByLabelText('Unit count'), typed);
    await fireEvent(screen.getByLabelText('Unit count'), 'blur');
    await pause();

    expect(client.updateScan).not.toHaveBeenCalled();
    expect(screen.getByText(UNIT_COUNT_INVALID)).toBeTruthy();
    expect(screen.getByLabelText('Unit count').props.value).toBe('1');
  });

  it('does not confirm while the count is invalid', async () => {
    const client = api([scan()]);
    const screen = await show(client);
    await screen.findByText('Oat milk');

    await fireEvent.changeText(screen.getByLabelText('Unit count'), '0');
    await fireEvent.press(screen.getByLabelText('Confirm Oat milk'));
    await pause();

    expect(client.commitScan).not.toHaveBeenCalled();
    expect(client.updateScan).not.toHaveBeenCalled();
    expect(screen.getByText(UNIT_COUNT_INVALID)).toBeTruthy();
  });

  it('saves a pending count before it confirms', async () => {
    const client = api([scan()]);
    const screen = await show(client);
    await screen.findByText('Oat milk');

    await fireEvent.press(screen.getByLabelText('Increase unit count'));
    await fireEvent.press(screen.getByLabelText('Confirm Oat milk'));

    await waitFor(() => expect(client.commitScan).toHaveBeenCalledTimes(1));
    const [update] = (client.updateScan as jest.Mock).mock.invocationCallOrder;
    const [commit] = (client.commitScan as jest.Mock).mock.invocationCallOrder;
    expect(update).toBeLessThan(commit);
  });

  it('does not confirm when the count could not be saved', async () => {
    const client = api([scan()], { updateScan: jest.fn(async () => { throw new ApiError(500, 'boom'); }) });
    const screen = await show(client);
    await screen.findByText('Oat milk');

    await fireEvent.press(screen.getByLabelText('Increase unit count'));
    await fireEvent.press(screen.getByLabelText('Confirm Oat milk'));

    expect(await screen.findByText(UNIT_COUNT_SAVE_FAILURE)).toBeTruthy();
    expect(client.commitScan).not.toHaveBeenCalled();
    expect(screen.getByLabelText('Unit count').props.value).toBe('1');
  });

  it('puts the count back and says so when the save fails', async () => {
    const client = api([scan()], { updateScan: jest.fn(async () => { throw new ApiError(500, 'boom'); }) });
    const screen = await show(client);
    await screen.findByText('Oat milk');

    await fireEvent.press(screen.getByLabelText('Increase unit count'));

    expect(await screen.findByText(UNIT_COUNT_SAVE_FAILURE)).toBeTruthy();
    expect(screen.getByLabelText('Unit count').props.value).toBe('1');
  });

  it('keeps an unsaved draft when a live event carries a different count', async () => {
    const hub = createLiveHub();
    const client = api([scan()]);
    const screen = await show(client, hub);
    await screen.findByText('Oat milk');

    await fireEvent.press(screen.getByLabelText('Increase unit count'));
    await act(async () => sendScan(hub, scan({ unitCount: 9 })));

    expect(screen.getByLabelText('Unit count').props.value).toBe('2');
    await waitFor(() => expect(client.updateScan).toHaveBeenCalledWith('scan-1', { unitCount: 2 }));
  });

  it('keeps text the person is still typing when a live event arrives', async () => {
    const hub = createLiveHub();
    const client = api([scan()]);
    const screen = await show(client, hub);
    await screen.findByText('Oat milk');

    await fireEvent.changeText(screen.getByLabelText('Unit count'), '');
    await act(async () => sendScan(hub, scan({ unitCount: 4 })));

    expect(screen.getByLabelText('Unit count').props.value).toBe('');
    await fireEvent(screen.getByLabelText('Unit count'), 'blur');
    expect(screen.getByLabelText('Unit count').props.value).toBe('4');
    expect(client.updateScan).not.toHaveBeenCalled();
  });

  it('follows a live count when nothing is unsaved', async () => {
    const hub = createLiveHub();
    const screen = await show(api([scan()]), hub);
    await screen.findByText('Oat milk');

    await act(async () => sendScan(hub, scan({ unitCount: 4 })));

    expect(screen.getByLabelText('Unit count').props.value).toBe('4');
  });

  it('does not let a slow, older save response put an older count back', async () => {
    const older = deferred<ScanEntry>();
    let served = 1;
    const updateScan = jest.fn()
      .mockReturnValueOnce(older.promise)
      .mockImplementation(async (_id: string, input: { unitCount: number }) => {
        served = input.unitCount;
        return scan({ unitCount: input.unitCount });
      });
    const client = api([], {
      listPending: jest.fn(async () => [scan({ unitCount: served })]),
      updateScan,
    });
    const screen = await show(client);
    await screen.findByText('Oat milk');

    await fireEvent.press(screen.getByLabelText('Increase unit count'));
    await waitFor(() => expect(updateScan).toHaveBeenCalledTimes(1));
    await fireEvent.press(screen.getByLabelText('Increase unit count'));
    await waitFor(() => expect(updateScan).toHaveBeenCalledTimes(2));
    expect(updateScan).toHaveBeenLastCalledWith('scan-1', { unitCount: 3 });
    await waitFor(() => expect((client.listPending as jest.Mock).mock.calls.length).toBeGreaterThan(1));

    await act(async () => older.resolve(scan({ unitCount: 2 })));
    await pause();

    expect(screen.getByLabelText('Unit count').props.value).toBe('3');
    expect(updateScan).toHaveBeenCalledTimes(2);
  });

  it('does not let a slow, older save failure revert a newer count', async () => {
    const older = deferred<ScanEntry>();
    const updateScan = jest.fn()
      .mockReturnValueOnce(older.promise)
      .mockImplementation(async (_id: string, input: { unitCount: number }) => scan({ unitCount: input.unitCount }));
    const client = api([scan()], { updateScan });
    const screen = await show(client);
    await screen.findByText('Oat milk');

    await fireEvent.press(screen.getByLabelText('Increase unit count'));
    await waitFor(() => expect(updateScan).toHaveBeenCalledTimes(1));
    await fireEvent.press(screen.getByLabelText('Increase unit count'));
    await waitFor(() => expect(updateScan).toHaveBeenCalledTimes(2));

    await act(async () => older.reject(new ApiError(500, 'boom')));
    await pause();

    expect(screen.getByLabelText('Unit count').props.value).toBe('3');
    expect(screen.queryByText(UNIT_COUNT_SAVE_FAILURE)).toBeNull();
  });
});

describe('expiration date on a stock-in card', () => {
  async function openEditor(client: StockbotClient) {
    const screen = await show(client);
    await screen.findByText('Oat milk');
    await fireEvent.press(screen.getByLabelText('Add expiration'));
    return screen;
  }

  it('saves a typed date as UTC midnight when the field is left', async () => {
    const client = api([scan()]);
    const screen = await openEditor(client);

    await fireEvent.changeText(screen.getByLabelText('Expiration date'), '2027-03-04');
    expect(client.updateScan).not.toHaveBeenCalled();
    await fireEvent(screen.getByLabelText('Expiration date'), 'blur');

    await waitFor(() => expect(client.updateScan).toHaveBeenCalledWith('scan-1', { expiresAt: '2027-03-04T00:00:00.000Z' }));
    expect(await screen.findByText(/^Expires .*2027/)).toBeTruthy();
    expect(screen.getByLabelText('Clear expiration')).toBeTruthy();
  });

  it.each(['2027-02-30', '2027-13-01', '2027-3-4', 'soon'])('does not PATCH %s and names the format', async (typed) => {
    const client = api([scan()]);
    const screen = await openEditor(client);

    await fireEvent.changeText(screen.getByLabelText('Expiration date'), typed);
    await fireEvent(screen.getByLabelText('Expiration date'), 'blur');
    await pause(50);

    expect(client.updateScan).not.toHaveBeenCalled();
    expect(screen.getByText(EXPIRY_INVALID)).toBeTruthy();
  });

  it('clears with the zero timestamp, never null', async () => {
    const client = api([scan({ expiresAt: '2027-03-04T00:00:00Z' })]);
    const screen = await show(client);
    await screen.findByText(/^Expires .*2027/);

    await fireEvent.press(screen.getByLabelText('Clear expiration'));

    await waitFor(() => expect(client.updateScan).toHaveBeenCalledWith('scan-1', { expiresAt: CLEARED_EXPIRY_ISO }));
    expect(await screen.findByLabelText('Add expiration')).toBeTruthy();
  });

  it('cancels without saving', async () => {
    const client = api([scan({ expiresAt: '2027-03-04T00:00:00Z' })]);
    const screen = await show(client);
    await screen.findByText(/^Expires .*2027/);

    await fireEvent.press(screen.getByLabelText('Change expiration'));
    await fireEvent.changeText(screen.getByLabelText('Expiration date'), '2028-01-01');
    await fireEvent.press(screen.getByLabelText('Cancel expiration'));

    expect(client.updateScan).not.toHaveBeenCalled();
    expect(screen.getByText(/^Expires .*2027/)).toBeTruthy();
  });

  it('puts the date back and says so when the save fails', async () => {
    const client = api([scan({ expiresAt: '2027-03-04T00:00:00Z' })], {
      updateScan: jest.fn(async () => { throw new ApiError(500, 'boom'); }),
    });
    const screen = await show(client);
    await screen.findByText(/^Expires .*2027/);

    await fireEvent.press(screen.getByLabelText('Change expiration'));
    await fireEvent.changeText(screen.getByLabelText('Expiration date'), '2028-01-01');
    await fireEvent(screen.getByLabelText('Expiration date'), 'blur');

    expect(await screen.findByText(EXPIRY_SAVE_FAILURE)).toBeTruthy();
    expect(screen.getByLabelText('Expiration date').props.value).toBe('2027-03-04');
  });

  it('keeps a date being typed when a live event carries another one', async () => {
    const hub = createLiveHub();
    const client = api([scan()]);
    const screen = await show(client, hub);
    await screen.findByText('Oat milk');
    await fireEvent.press(screen.getByLabelText('Add expiration'));

    await fireEvent.changeText(screen.getByLabelText('Expiration date'), '2027-05-06');
    await act(async () => sendScan(hub, scan({ expiresAt: '2030-01-01T00:00:00Z' })));

    expect(screen.getByLabelText('Expiration date').props.value).toBe('2027-05-06');
    expect(client.updateScan).not.toHaveBeenCalled();
  });

  it('shows a date another device set when nothing is being edited', async () => {
    const hub = createLiveHub();
    const screen = await show(api([scan()]), hub);
    await screen.findByText('Oat milk');

    await act(async () => sendScan(hub, scan({ expiresAt: '2030-01-01T00:00:00Z' })));

    expect(await screen.findByText(/^Expires .*2030/)).toBeTruthy();
  });

  it('has no expiration control on a stock-out card', async () => {
    const screen = await show(api([scan({ direction: 'stock_out' })]));
    await screen.findByText('Oat milk');

    expect(screen.queryByLabelText('Add expiration')).toBeNull();
    expect(screen.getByLabelText('Unit count')).toBeTruthy();
  });
});

describe('choosing which unit to stock out', () => {
  const units = [
    instance('later', '2027-05-01T00:00:00Z'),
    instance('undated', null),
    instance('oldest', '2027-04-01T00:00:00Z'),
  ];

  function stockOut(overrides: Partial<StockbotClient> = {}) {
    return api([scan({ direction: 'stock_out' })], { listInstances: jest.fn(async () => units), ...overrides });
  }

  it('lists the units oldest first after the automatic choice', async () => {
    const client = stockOut();
    const screen = await show(client);
    await screen.findByText('Oat milk');

    await fireEvent.press(screen.getByLabelText('More'));

    const group = await screen.findByTestId('instances-item-1');
    await waitFor(() => expect(client.listInstances).toHaveBeenCalledWith('item-1'));
    const labels = within(group).getAllByRole('radio').map((radio) => radio.props.accessibilityLabel);
    expect(labels[0]).toBe(INSTANCE_AUTOMATIC_LABEL);
    expect(labels.slice(1)).toEqual([
      expect.stringMatching(/Apr.*2027/),
      expect.stringMatching(/May.*2027/),
      'No expiration date',
    ]);
    expect(within(group).getByRole('radio', { name: INSTANCE_AUTOMATIC_LABEL, checked: true })).toBeTruthy();
  });

  it('sends the chosen instance with the commit', async () => {
    const client = stockOut();
    const screen = await show(client);
    await screen.findByText('Oat milk');

    await fireEvent.press(screen.getByLabelText('More'));
    await fireEvent.press(await screen.findByTestId('choice-later'));
    expect(screen.getByTestId('choice-later').props.accessibilityState.checked).toBe(true);
    await fireEvent.press(screen.getByLabelText('Confirm Oat milk'));

    await waitFor(() => expect(client.commitScan).toHaveBeenCalledWith('scan-1', undefined, 'later'));
  });

  it('sends no instance when none was chosen, so the server takes the oldest', async () => {
    const client = stockOut();
    const screen = await show(client);
    await screen.findByText('Oat milk');

    await fireEvent.press(screen.getByLabelText('More'));
    await fireEvent.press(await screen.findByTestId('choice-oldest'));
    await fireEvent.press(await screen.findByTestId('choice-automatic'));
    await fireEvent.press(screen.getByLabelText('Confirm Oat milk'));

    await waitFor(() => expect(client.commitScan).toHaveBeenCalledWith('scan-1'));
    expect((client.commitScan as jest.Mock).mock.calls[0]).toHaveLength(1);
  });

  it('does not offer the picker for a stock-in card', async () => {
    const stockIn = await show(api([scan()]));
    await stockIn.findByText('Oat milk');
    expect(stockIn.queryByLabelText('More')).toBeNull();
  });

  it('does not offer the picker for an item the household does not have', async () => {
    const unknown = await show(stockOut({ getInventoryList: jest.fn(async () => []) }));
    await unknown.findByText('Oat milk');
    expect(unknown.queryByLabelText('More')).toBeNull();
  });

  it('says so when the units could not be loaded', async () => {
    const failing = await show(stockOut({ listInstances: jest.fn(async () => { throw new ApiError(500, 'Could not list units.'); }) }));
    await failing.findByText('Oat milk');
    await fireEvent.press(failing.getByLabelText('More'));

    expect(await failing.findByText('Could not list units.')).toBeTruthy();
  });

  it('says so when the item has no units', async () => {
    const empty = await show(stockOut({ listInstances: jest.fn(async () => []) }));
    await empty.findByText('Oat milk');
    await fireEvent.press(empty.getByLabelText('More'));

    expect(await empty.findByText('No available instances.')).toBeTruthy();
  });

  it('reloads the units and drops the choice when the chosen one is gone', async () => {
    const commitScan = jest.fn()
      .mockRejectedValueOnce(new ApiError(409, 'That item was already taken out.', 'instance_gone'))
      .mockResolvedValue(scan({ status: 'committed' }));
    const listInstances = jest.fn()
      .mockResolvedValueOnce(units)
      .mockResolvedValue(units.filter((unit) => unit.id !== 'later'));
    const client = stockOut({ commitScan, listInstances });
    const screen = await show(client);
    await screen.findByText('Oat milk');

    await fireEvent.press(screen.getByLabelText('More'));
    await fireEvent.press(await screen.findByTestId('choice-later'));
    await fireEvent.press(screen.getByLabelText('Confirm Oat milk'));

    expect(await screen.findByText('That item was already taken out.')).toBeTruthy();
    await waitFor(() => expect(listInstances).toHaveBeenCalledTimes(2));
    await waitFor(() => expect(screen.queryByTestId('choice-later')).toBeNull());
    expect(screen.getByTestId('choice-automatic').props.accessibilityState.checked).toBe(true);
  });

  it('shows the short-count sentence after a stock-out that found fewer units', async () => {
    const short = { ...scan({ status: 'committed' }), removedCount: 2, requestedCount: 3 };
    const client = stockOut({ commitScan: jest.fn(async () => short) });
    const screen = await show(client);
    await screen.findByText('Oat milk');

    await fireEvent.press(screen.getByLabelText('Confirm Oat milk'));

    expect(await screen.findByText(formatPartialRemoval(short) ?? 'missing')).toBeTruthy();
  });
});

describe('undo after confirm', () => {
  async function addAndTakeOut(client: StockbotClient) {
    const screen = await show(client);
    await screen.findByText('Oat milk');
    await fireEvent.press(screen.getByLabelText('Confirm Oat milk'));
    await fireEvent.press(await screen.findByLabelText(/Add it and take it out/));
    return screen;
  }

  function noneOnHand() {
    return api([scan({ direction: 'stock_out' })], {
      commitScan: jest.fn(async () => { throw new ApiError(409, 'Nothing on hand', 'none_on_hand'); }),
    });
  }

  it('offers Undo with the web copy and removes the move pair when it is pressed', async () => {
    const client = noneOnHand();
    const screen = await addAndTakeOut(client);

    expect(await screen.findByText(ADDED_AND_TAKEN_OUT)).toBeTruthy();
    await fireEvent.press(screen.getByLabelText('Undo'));

    await waitFor(() => expect(client.undoMove).toHaveBeenCalledWith('move-in'));
    expect(screen.queryByLabelText('Undo')).toBeNull();
    await waitFor(() => expect((client.listPending as jest.Mock).mock.calls.length).toBeGreaterThan(1));
  });

  it('does nothing when Undo is pressed after the toast has expired', async () => {
    jest.useFakeTimers({ advanceTimers: true });
    try {
      const client = noneOnHand();
      const screen = await addAndTakeOut(client);
      const stale = await screen.findByLabelText('Undo');

      await act(async () => { jest.advanceTimersByTime(UNDO_TOAST_MS - 100); });
      expect(screen.queryByLabelText('Undo')).toBeTruthy();
      await act(async () => { jest.advanceTimersByTime(200); });
      expect(screen.queryByLabelText('Undo')).toBeNull();
      expect(screen.queryByText(ADDED_AND_TAKEN_OUT)).toBeNull();

      await fireEvent.press(stale);

      expect(client.undoMove).not.toHaveBeenCalled();
    } finally {
      jest.useRealTimers();
    }
  });

  it('says so when the undo fails', async () => {
    const client = noneOnHand();
    (client.undoMove as jest.Mock).mockRejectedValue(new ApiError(404, 'That move is already gone.'));
    const screen = await addAndTakeOut(client);

    await fireEvent.press(await screen.findByLabelText('Undo'));

    expect(await screen.findByText('That move is already gone.')).toBeTruthy();
  });

  it('offers no Undo when the server sent no move id', async () => {
    const client = noneOnHand();
    (client.commitBackfill as jest.Mock).mockResolvedValue({
      removed: 1,
      backfilled: 1,
      itemId: 'item-1',
      backfill: { stockInMoveId: '', stockOutMoveId: '' },
    });
    const screen = await addAndTakeOut(client);

    expect(await screen.findByText(ADDED_AND_TAKEN_OUT)).toBeTruthy();
    expect(screen.queryByLabelText('Undo')).toBeNull();
  });
});
