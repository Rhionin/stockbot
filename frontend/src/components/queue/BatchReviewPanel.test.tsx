import { afterEach, describe, expect, it, vi } from 'vitest';
import { fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { MantineProvider } from '@mantine/core';
import { getInventoryList } from '../../api/client';
import { OFFLINE_SENTENCE } from '../../api/contract';
import { ServerNoticeBanner } from '../shell/ServerNoticeBanner';
import { BatchReviewPanel } from './BatchReviewPanel';

const jsonResponse = (body: unknown) =>
  new Response(JSON.stringify(body), { status: 200, headers: { 'Content-Type': 'application/json' } });

describe('BatchReviewPanel', () => {
  it('renders the approve heading, button with correct text and aria-label', () => {
    render(
      <MantineProvider>
        <BatchReviewPanel selectedIds={['a', 'b']} onComplete={vi.fn()} />
      </MantineProvider>,
    );

    expect(screen.getByRole('heading', { level: 2, name: 'Confirm scans' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Confirm 2 selected scans' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Confirm 2 selected scans' })).toHaveAttribute(
      'aria-label',
      'Confirm 2 selected scans',
    );
  });

  it('shows error message on simulated failure', async () => {
    const fetchMock = vi.fn<typeof fetch>().mockRejectedValue(new Error('Network error'));
    vi.stubGlobal('fetch', fetchMock);
    const onComplete = vi.fn();

    render(
      <MantineProvider>
        <BatchReviewPanel selectedIds={['a', 'b']} onComplete={onComplete} />
      </MantineProvider>,
    );

    fireEvent.click(screen.getByRole('button', { name: 'Confirm 2 selected scans' }));

    await waitFor(() =>
      expect(screen.getByText('Unable to confirm selected scans.')).toBeInTheDocument(),
    );
  });

  it('only includes entries from Active_View in the approve request when filtered by ScanQueuePage', async () => {
    const fetchMock = vi.fn<typeof fetch>().mockResolvedValue(jsonResponse({}));
    vi.stubGlobal('fetch', fetchMock);
    const onComplete = vi.fn();

    // Simulate ScanQueuePage filtering selectedIds to only include entries from the Active_View
    // selectedIds contains entries from both views, but BatchReviewPanel receives only Active_View entries
    const activeViewSelectedIds = ['stock-out-1', 'stock-out-2'];

    render(
      <MantineProvider>
        <BatchReviewPanel selectedIds={activeViewSelectedIds} onComplete={onComplete} />
      </MantineProvider>,
    );

    fireEvent.click(screen.getByRole('button', { name: 'Confirm 2 selected scans' }));

    await waitFor(() => {
      expect(fetchMock).toHaveBeenCalledWith(
        '/api/scans/batch-commit',
        expect.objectContaining({
          method: 'POST',
          body: JSON.stringify({
            scanEntryIds: activeViewSelectedIds,
            commit: true,
          }),
        }),
      );
    });

    expect(onComplete).toHaveBeenCalled();
  });

  it('sends approve request with only visible view entries when entries from other views are filtered out', async () => {
    const fetchMock = vi.fn<typeof fetch>().mockResolvedValue(jsonResponse({}));
    vi.stubGlobal('fetch', fetchMock);
    const onComplete = vi.fn();

    // This simulates the scenario where ScanQueuePage has entries from both views,
    // but filters selectedIds to only include entries from the currently active view.
    // Example: user selected entries in Stock_Out_View (stock-out-1, stock-out-2)
    // but stock-in-1 and stock-in-2 also exist in selectedIds before filtering.
    // After filtering, only stock-out entries are passed to BatchReviewPanel.
    const viewScopedSelectedIds = ['stock-out-1', 'stock-out-2'];

    render(
      <MantineProvider>
        <BatchReviewPanel selectedIds={viewScopedSelectedIds} onComplete={onComplete} />
      </MantineProvider>,
    );

    fireEvent.click(screen.getByRole('button', { name: 'Confirm 2 selected scans' }));

    await waitFor(() => {
      // Verify the exact request body sent to the API
      const calls = fetchMock.mock.calls;
      const batchCommitCall = calls.find((call) => String(call[0]).includes('batch-commit'));
      expect(batchCommitCall).toBeDefined();

      const requestBody = JSON.parse(batchCommitCall![1]?.body as string);
      // Verify only the view-scoped entries are included
      expect(requestBody.scanEntryIds).toEqual(['stock-out-1', 'stock-out-2']);
      expect(requestBody.scanEntryIds).not.toContain('stock-in-1');
      expect(requestBody.scanEntryIds).not.toContain('stock-in-2');
    });
  });
});

describe('BatchReviewPanel offline sentence', () => {
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it('keeps the sentence until a later request succeeds', async () => {
    vi.stubGlobal('fetch', vi.fn(() => Promise.reject(new TypeError('Failed to fetch'))));
    render(
      <MantineProvider>
        <ServerNoticeBanner />
        <BatchReviewPanel selectedIds={['a', 'b']} onComplete={vi.fn()} />
      </MantineProvider>,
    );

    fireEvent.click(screen.getByRole('button', { name: 'Confirm 2 selected scans' }));
    expect(await screen.findByTestId('server-notice-banner')).toHaveTextContent(OFFLINE_SENTENCE);
    expect(within(screen.getByRole('region', { name: 'Confirm scans' })).queryByText(OFFLINE_SENTENCE)).not.toBeInTheDocument();

    let rejectNext: (error: unknown) => void = () => {};
    vi.stubGlobal('fetch', vi.fn(() => new Promise((_resolve, reject) => {
      rejectNext = reject;
    })));
    fireEvent.click(screen.getByRole('button', { name: 'Confirm 2 selected scans' }));
    expect(screen.getByTestId('server-notice-banner')).toHaveTextContent(OFFLINE_SENTENCE);
    rejectNext(new TypeError('Failed to fetch'));
    expect(await screen.findByTestId('server-notice-banner')).toHaveTextContent(OFFLINE_SENTENCE);
    expect(screen.queryByText('Failed to fetch')).not.toBeInTheDocument();
    expect(within(screen.getByRole('region', { name: 'Confirm scans' })).queryByText(OFFLINE_SENTENCE)).not.toBeInTheDocument();

    vi.stubGlobal('fetch', vi.fn(() => Promise.resolve(jsonResponse([]))));
    await getInventoryList();
    await waitFor(() => expect(screen.queryByText(OFFLINE_SENTENCE)).not.toBeInTheDocument());
  });

  it('does not treat an invalid header as offline', async () => {
    vi.stubGlobal('fetch', vi.fn(() => Promise.reject(new TypeError("Failed to construct 'Headers': Invalid name"))));
    render(
      <MantineProvider>
        <BatchReviewPanel selectedIds={['a', 'b']} onComplete={vi.fn()} />
      </MantineProvider>,
    );

    fireEvent.click(screen.getByRole('button', { name: 'Confirm 2 selected scans' }));
    expect(await screen.findByText('Unable to confirm selected scans.')).toBeInTheDocument();
    expect(screen.queryByText(OFFLINE_SENTENCE)).not.toBeInTheDocument();
    expect(screen.queryByText("Failed to construct 'Headers': Invalid name")).not.toBeInTheDocument();
  });
});
