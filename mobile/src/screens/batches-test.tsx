import { useEffect, useState } from 'react';
import { act, fireEvent, waitFor, within } from '@testing-library/react-native';
import { BATCH_FAILURE_FALLBACK, CODE_PRODUCT_UNRESOLVED } from '@stockbot/shared/contract';
import type { ScanDirection, ScanEntry, ScannerConfig } from '@stockbot/shared/types';
import type { StockbotClient } from '../api/client';
import { createLiveHub, LiveHubProvider, type LiveHub } from '../live/LiveEvents';
import { setForeground } from '../test/fakeForeground';
import { renderScreen } from '../test/renderScreen';
import { QueueScreen } from './QueueScreen';

jest.mock('../live/foreground', () => jest.requireActual('../test/fakeForeground'));

const scan = (id: string, name: string, direction: ScanDirection | null, scannedAt = '2026-10-16T12:00:00Z', extra: Partial<ScanEntry> = {}): ScanEntry => ({
  id,
  userId: 'user-1',
  barcode: `code-${id}`,
  scannedAt,
  direction,
  unitCount: 1,
  expiresAt: null,
  status: 'pending',
  productId: `prod-${id}`,
  product: { id: `prod-${id}`, name, category: 'Pantry', unitOfMeasure: 'bag' },
  committedAt: null,
  createdAt: scannedAt,
  ...extra,
});

const config = (currentMode: ScanDirection): ScannerConfig => ({
  stockInBarcode: 'IN',
  stockOutBarcode: 'OUT',
  currentMode,
  connected: true,
});

interface Setup {
  rows: ScanEntry[];
  mode?: ScanDirection;
  batchCommit?: StockbotClient['batchCommit'];
}

function setup({ rows, mode = 'stock_in', batchCommit }: Setup) {
  const state = { rows: [...rows] };
  const api = {
    events: { url: 'https://stockbot.example/api/events', headers: {}, rejected: jest.fn() },
    getScannerConfig: jest.fn(async () => config(mode)),
    setScannerMode: jest.fn(async (next: ScanDirection) => ({ mode: next })),
    listPending: jest.fn(async () => [...state.rows]),
    getInventoryList: jest.fn(async () => []),
    updateScan: jest.fn(async () => { throw new Error('updateScan not stubbed'); }),
    listInstances: jest.fn(async () => []),
    undoMove: jest.fn(async () => undefined),
    batchCommit: batchCommit ?? jest.fn(async (ids: string[]) => {
      state.rows = state.rows.filter((row) => !ids.includes(row.id));
      return { updatedCount: ids.length, committedIds: ids };
    }),
  } as unknown as StockbotClient;
  return { api, state };
}

function Harness({ api }: { api: StockbotClient }) {
  const [focused] = useState(true);
  useEffect(() => undefined, []);
  return <QueueScreen client={api} warning="" focused={focused} />;
}

async function open(api: StockbotClient): Promise<{ hub: LiveHub; screen: Awaited<ReturnType<typeof renderScreen>> }> {
  const hub = createLiveHub();
  const screen = await renderScreen(
    <LiveHubProvider hub={hub}>
      <Harness api={api} />
    </LiveHubProvider>,
  );
  return { hub, screen };
}

const emitScan = (hub: LiveHub, entry: ScanEntry) =>
  act(async () => hub.emit({ kind: 'message', event: 'scan', data: JSON.stringify(entry) }));

beforeEach(() => setForeground(true));

const stockIn = [
  scan('a1', 'Synthetic oats', 'stock_in', '2026-10-16T12:02:00Z'),
  scan('a2', 'Synthetic rice', 'stock_in', '2026-10-16T12:01:00Z'),
  scan('a3', 'Synthetic beans', 'stock_in', '2026-10-16T12:00:00Z', { unitCount: 3 }),
];
const stockOut = [
  scan('o1', 'Synthetic flour', 'stock_out', '2026-10-16T12:00:00Z'),
  scan('o2', 'Synthetic salt', 'stock_out', '2026-10-16T12:00:30Z'),
];

describe('sessions', () => {
  it('groups scans a long pause apart into separate sessions and opens only the newest', async () => {
    const earlier = scan('a4', 'Synthetic lentils', 'stock_in', '2026-10-16T08:00:00Z');
    const { api } = setup({ rows: [...stockIn, earlier] });
    const { screen } = await open(api);

    expect(await screen.findByText('Synthetic oats')).toBeTruthy();
    expect(screen.getAllByTestId(/^session-.*-toggle$/)).toHaveLength(2);
    const [newest, older] = screen.getAllByTestId(/^session-.*-toggle$/);
    expect(newest.props.accessibilityState.expanded).toBe(true);
    expect(older.props.accessibilityState.expanded).toBe(false);
    expect(screen.queryByText('Synthetic lentils')).toBeNull();

    await fireEvent.press(older);

    expect(await screen.findByText('Synthetic lentils')).toBeTruthy();
    expect(screen.getAllByTestId(/^session-.*-toggle$/)[1].props.accessibilityState.expanded).toBe(true);
  });
});

describe('selecting scans', () => {
  it('gives every card a checkbox with a checked state and counts the selection', async () => {
    const { api } = setup({ rows: stockIn });
    const { screen } = await open(api);
    await screen.findByText('Synthetic oats');

    const oats = screen.getByRole('checkbox', { name: 'Select Synthetic oats' });
    expect(oats.props.accessibilityState.checked).toBe(false);
    expect(screen.queryByRole('button', { name: /^Review/ })).toBeNull();

    await fireEvent.press(oats);

    expect(screen.getByRole('checkbox', { name: 'Select Synthetic oats' }).props.accessibilityState.checked).toBe(true);
    expect(screen.getByRole('button', { name: 'Review 1 selected' })).toBeTruthy();
    expect(screen.queryByRole('button', { name: 'Confirm all' })).toBeNull();

    await fireEvent.press(screen.getByRole('checkbox', { name: 'Select Synthetic oats' }));
    expect(screen.queryByRole('button', { name: /^Review/ })).toBeNull();
    expect(screen.getByRole('button', { name: 'Confirm all' })).toBeTruthy();
  });

  it('shows a mixed select-all while some of a session is picked, and clears it with Clear', async () => {
    const { api } = setup({ rows: stockIn });
    const { screen } = await open(api);
    await screen.findByText('Synthetic oats');
    const selectAll = () => screen.getByRole('checkbox', { name: /^Select all eligible scans in / });
    expect(selectAll().props.accessibilityState.checked).toBe(false);

    await fireEvent.press(screen.getByRole('checkbox', { name: 'Select Synthetic rice' }));
    expect(selectAll().props.accessibilityState.checked).toBe('mixed');

    await fireEvent.press(selectAll());
    expect(selectAll().props.accessibilityState.checked).toBe(true);
    expect(screen.getByRole('button', { name: 'Review 3 selected' })).toBeTruthy();

    await fireEvent.press(screen.getByRole('button', { name: 'Clear' }));
    expect(selectAll().props.accessibilityState.checked).toBe(false);
    expect(screen.queryByRole('button', { name: /^Review/ })).toBeNull();
  });

  it('selects across sessions from the tab-level select all, including a collapsed one', async () => {
    const earlier = scan('a4', 'Synthetic lentils', 'stock_in', '2026-10-16T08:00:00Z');
    const { api } = setup({ rows: [...stockIn, earlier] });
    const { screen } = await open(api);
    await screen.findByText('Synthetic oats');
    expect(screen.queryByText('Synthetic lentils')).toBeNull();

    await fireEvent.press(screen.getByRole('checkbox', { name: 'Select all eligible scans for batch approval' }));

    expect(screen.getByRole('button', { name: 'Review 4 selected' })).toBeTruthy();
  });

  it('leaves a scan with nothing on hand and a scan with no direction out of select all', async () => {
    const rows = [
      scan('o1', 'Synthetic flour', 'stock_out', '2026-10-16T12:00:00Z', { onHand: 0 } as Partial<ScanEntry>),
      scan('o2', 'Synthetic salt', 'stock_out', '2026-10-16T12:00:30Z'),
      scan('o3', 'Synthetic mystery', null, '2026-10-16T12:00:40Z'),
    ];
    const { api } = setup({ rows, mode: 'stock_out' });
    const { screen } = await open(api);
    await screen.findByText('Synthetic salt');

    expect(screen.getByRole('checkbox', { name: 'Select Synthetic flour' }).props.accessibilityState.disabled).toBe(true);
    expect(screen.getByRole('checkbox', { name: 'Select Synthetic mystery' }).props.accessibilityState.disabled).toBe(true);

    await fireEvent.press(screen.getByRole('checkbox', { name: /^Select all eligible scans in / }));

    expect(screen.getByRole('button', { name: 'Review 1 selected' })).toBeTruthy();
    expect(screen.getByRole('checkbox', { name: 'Select Synthetic salt' }).props.accessibilityState.checked).toBe(true);
    expect(screen.getByRole('checkbox', { name: 'Select Synthetic flour' }).props.accessibilityState.checked).toBe(false);
  });
});

describe('review and confirm', () => {
  it('lists exactly what will be confirmed, warns about the skipped scans, and sends only those', async () => {
    const rows = [
      scan('o1', 'Synthetic flour', 'stock_out', '2026-10-16T12:00:00Z', { onHand: 0 } as Partial<ScanEntry>),
      scan('o2', 'Synthetic salt', 'stock_out', '2026-10-16T12:00:30Z', { unitCount: 2 }),
      scan('o3', 'Synthetic sugar', 'stock_out', '2026-10-16T12:00:40Z'),
    ];
    const { api } = setup({ rows, mode: 'stock_out' });
    const { screen } = await open(api);
    await screen.findByText('Synthetic salt');
    await fireEvent.press(screen.getByRole('checkbox', { name: 'Select Synthetic salt' }));

    await fireEvent.press(screen.getByRole('button', { name: 'Review 1 selected' }));

    const panel = screen.getByTestId('batch-review');
    expect(panel.props.role).toBe('region');
    expect(panel.props['aria-label']).toBe('Confirm 1 scan');
    expect(within(panel).getByRole('heading', { name: 'Confirm 1 scan' })).toBeTruthy();
    expect(within(panel).getByText('Synthetic salt')).toBeTruthy();
    expect(within(panel).getByText('×2')).toBeTruthy();
    expect(within(panel).queryByText('Synthetic sugar')).toBeNull();
    expect(within(panel).getByText('Confirm skips the 1 with nothing on hand.')).toBeTruthy();
    expect(api.batchCommit).not.toHaveBeenCalled();

    await fireEvent.press(within(panel).getByRole('button', { name: 'Confirm 1 selected scans' }));

    await waitFor(() => expect(api.batchCommit).toHaveBeenCalledWith(['o2']));
    await waitFor(() => expect(screen.queryByTestId('batch-review')).toBeNull());
    expect(screen.queryByText('Synthetic salt')).toBeNull();
    expect(screen.getByText('Synthetic sugar')).toBeTruthy();
  });

  it('goes back from the review without sending anything and keeps the selection', async () => {
    const { api } = setup({ rows: stockIn });
    const { screen } = await open(api);
    await screen.findByText('Synthetic oats');
    await fireEvent.press(screen.getByRole('checkbox', { name: 'Select Synthetic oats' }));
    await fireEvent.press(screen.getByRole('button', { name: 'Review 1 selected' }));

    await fireEvent.press(screen.getByRole('button', { name: 'Back' }));

    expect(screen.queryByTestId('batch-review')).toBeNull();
    expect(screen.getByRole('button', { name: 'Review 1 selected' })).toBeTruthy();
    expect(api.batchCommit).not.toHaveBeenCalled();
  });

  it('keeps the failed scans, their notices and their selection, and removes only the ones that saved', async () => {
    const { api, state } = setup({
      rows: stockIn,
      batchCommit: jest.fn(async () => {
        state.rows = state.rows.filter((row) => row.id === 'a2' || row.id === 'a3');
        return {
          updatedCount: 1,
          committedIds: ['a1'],
          failedIds: ['a2', 'a3'],
          failedReasons: ['Choose which item this is first.', 'Could not save this one.'],
          failedCodes: [CODE_PRODUCT_UNRESOLVED, 'other'],
        };
      }),
    } as Setup) as unknown as ReturnType<typeof setup>;
    const { screen } = await open(api);
    await screen.findByText('Synthetic oats');
    await fireEvent.press(screen.getByRole('checkbox', { name: /^Select all eligible scans in / }));
    await fireEvent.press(screen.getByRole('button', { name: 'Review 3 selected' }));

    await fireEvent.press(screen.getByRole('button', { name: 'Confirm 3 selected scans' }));

    await waitFor(() => expect(screen.queryByText('Synthetic oats')).toBeNull());
    expect(screen.getByText('Synthetic rice')).toBeTruthy();
    expect(screen.getByText('Synthetic beans')).toBeTruthy();
    expect(screen.getByText('Choose which item this is first.')).toBeTruthy();
    expect(screen.getByText('Could not save this one.')).toBeTruthy();
    expect(screen.getByText(BATCH_FAILURE_FALLBACK)).toBeTruthy();
    expect(screen.getByRole('checkbox', { name: 'Select Synthetic rice' }).props.accessibilityState.checked).toBe(true);
    expect(screen.getByRole('checkbox', { name: 'Select Synthetic beans' }).props.accessibilityState.checked).toBe(true);
    expect(screen.getByRole('button', { name: 'Review 2 selected' })).toBeTruthy();
  });

  it('shows a request failure inside the review and keeps the selection for another try', async () => {
    const { api } = setup({
      rows: stockIn,
      batchCommit: jest.fn(async () => { throw new Error('boom'); }),
    });
    const { screen } = await open(api);
    await screen.findByText('Synthetic oats');
    await fireEvent.press(screen.getByRole('checkbox', { name: 'Select Synthetic oats' }));
    await fireEvent.press(screen.getByRole('button', { name: 'Review 1 selected' }));

    await fireEvent.press(screen.getByRole('button', { name: 'Confirm 1 selected scans' }));

    expect(await within(screen.getByTestId('batch-review')).findByRole('alert')).toBeTruthy();
    expect(screen.getByRole('checkbox', { name: 'Select Synthetic oats' }).props.accessibilityState.checked).toBe(true);
    expect(screen.getByRole('button', { name: 'Confirm 1 selected scans' })).toBeTruthy();
  });
});

describe('what a selection can reach', () => {
  it('never confirms a scan that was selected on the other tab', async () => {
    const { api } = setup({ rows: [...stockIn, ...stockOut] });
    const { hub, screen } = await open(api);
    await screen.findByText('Synthetic oats');
    await fireEvent.press(screen.getByRole('checkbox', { name: 'Select Synthetic oats' }));
    expect(screen.getByRole('button', { name: 'Review 1 selected' })).toBeTruthy();

    await act(async () => hub.emit({ kind: 'message', event: 'scanner_mode', data: '"stock_out"' }));
    await screen.findByText('Synthetic flour');

    expect(screen.queryByRole('button', { name: /^Review/ })).toBeNull();
    await fireEvent.press(screen.getByRole('checkbox', { name: 'Select Synthetic flour' }));
    await fireEvent.press(screen.getByRole('button', { name: 'Review 1 selected' }));
    await fireEvent.press(screen.getByRole('button', { name: 'Confirm 1 selected scans' }));

    await waitFor(() => expect(api.batchCommit).toHaveBeenCalledWith(['o1']));
    expect(api.batchCommit).toHaveBeenCalledTimes(1);

    await act(async () => hub.emit({ kind: 'message', event: 'scanner_mode', data: '"stock_in"' }));
    await screen.findByText('Synthetic oats');
    expect(screen.getByRole('checkbox', { name: 'Select Synthetic oats' }).props.accessibilityState.checked).toBe(false);
    expect(screen.queryByRole('button', { name: /^Review/ })).toBeNull();
  });

  it('clears the selection when the tab is pressed', async () => {
    const { api } = setup({ rows: [...stockIn, ...stockOut] });
    const { screen } = await open(api);
    await screen.findByText('Synthetic oats');
    await fireEvent.press(screen.getByRole('checkbox', { name: 'Select Synthetic oats' }));

    await fireEvent.press(screen.getByRole('tab', { name: /^Stock out/ }));
    await screen.findByText('Synthetic flour');
    await fireEvent.press(screen.getByRole('tab', { name: /^Stock in/ }));
    await screen.findByText('Synthetic oats');

    expect(screen.getByRole('checkbox', { name: 'Select Synthetic oats' }).props.accessibilityState.checked).toBe(false);
    expect(screen.queryByRole('button', { name: /^Review/ })).toBeNull();
  });

  it('does not send a selected scan that a live event removed, even with the review open', async () => {
    const { api, state } = setup({ rows: stockIn });
    const { hub, screen } = await open(api);
    await screen.findByText('Synthetic oats');
    await fireEvent.press(screen.getByRole('checkbox', { name: 'Select Synthetic oats' }));
    await fireEvent.press(screen.getByRole('checkbox', { name: 'Select Synthetic rice' }));
    await fireEvent.press(screen.getByRole('button', { name: 'Review 2 selected' }));
    const panel = screen.getByTestId('batch-review');
    expect(within(panel).getByText('Synthetic oats')).toBeTruthy();

    state.rows = state.rows.filter((row) => row.id !== 'a1');
    await emitScan(hub, { ...stockIn[0], status: 'cancelled' });

    await waitFor(() => expect(within(screen.getByTestId('batch-review')).queryByText('Synthetic oats')).toBeNull());
    expect(within(screen.getByTestId('batch-review')).getByText('Synthetic rice')).toBeTruthy();
    await fireEvent.press(screen.getByRole('button', { name: 'Confirm 1 selected scans' }));

    await waitFor(() => expect(api.batchCommit).toHaveBeenCalledWith(['a2']));
  });

  it('closes the review when the last selected scan leaves', async () => {
    const { api, state } = setup({ rows: stockIn });
    const { hub, screen } = await open(api);
    await screen.findByText('Synthetic oats');
    await fireEvent.press(screen.getByRole('checkbox', { name: 'Select Synthetic oats' }));
    await fireEvent.press(screen.getByRole('button', { name: 'Review 1 selected' }));

    state.rows = state.rows.filter((row) => row.id !== 'a1');
    await emitScan(hub, { ...stockIn[0], status: 'committed' });

    await waitFor(() => expect(screen.queryByTestId('batch-review')).toBeNull());
    expect(screen.queryByRole('button', { name: /^Review/ })).toBeNull();
    expect(api.batchCommit).not.toHaveBeenCalled();
  });
});

describe('Confirm all keeps working', () => {
  it('still confirms the whole tab when nothing is selected', async () => {
    const { api } = setup({ rows: stockIn });
    const { screen } = await open(api);

    await fireEvent.press(await screen.findByRole('button', { name: 'Confirm all' }));

    await waitFor(() => expect(api.batchCommit).toHaveBeenCalledWith(['a1', 'a2', 'a3']));
  });
});
