import type { ComponentProps } from 'react';
import { describe, expect, it, vi } from 'vitest';
import { fireEvent, render, screen } from '@testing-library/react';
import { MantineProvider } from '@mantine/core';
import { ScanSessionCard } from './ScanSessionCard';
import { formatBatchTimeRange } from './queueUtils';
import type { ScanEntry } from '@stockbot/shared/types';

const scanEntry = (overrides: Partial<ScanEntry>): ScanEntry => ({
  id: 'scan-1',
  userId: 'user-1',
  barcode: '111',
  scannedAt: '2026-03-20T16:00:00Z',
  direction: 'stock_in',
  unitCount: 1,
  expiresAt: null,
  status: 'pending',
  productId: 'product-1',
  product: { id: 'product-1', name: 'Oats', category: 'Grocery', unitOfMeasure: 'bag' },
  committedAt: null,
  createdAt: '2026-03-20T16:00:00Z',
  ...overrides,
});

const renderSession = (overrides: Partial<ComponentProps<typeof ScanSessionCard>> = {}) => {
  const onSelectionChange = vi.fn();
  const onToggleExpanded = vi.fn();
  const entries = overrides.entries ?? [
    scanEntry({ id: 'oats', barcode: '111', scannedAt: '2026-03-20T16:02:00Z' }),
    scanEntry({ id: 'milk', barcode: '222', scannedAt: '2026-03-20T16:06:00Z', product: { id: 'product-2', name: 'Milk', category: 'Dairy', unitOfMeasure: 'carton' } }),
    scanEntry({ id: 'flagged', barcode: '333', scannedAt: '2026-03-20T16:08:00Z', status: 'flagged' }),
  ];
  render(
    <MantineProvider>
      <ScanSessionCard
        entries={entries}
        expanded
        onToggleExpanded={onToggleExpanded}
        directionLabel="stock in"
        selectedIds={[]}
        onSelectionChange={onSelectionChange}
        flat
        {...overrides}
      >
        {entries.map((entry) => (
          <article key={entry.id} aria-label={`Scan ${entry.barcode}`}>
            <label>
              Unit count
              <input aria-label="Unit count" defaultValue={entry.unitCount} />
            </label>
          </article>
        ))}
      </ScanSessionCard>
    </MantineProvider>,
  );
  return { onSelectionChange, onToggleExpanded, entries };
};

describe('ScanSessionCard', () => {
  it('labels an expanded session with the earliest-to-latest time and scan count', () => {
    renderSession();
    const toggle = screen.getByRole('button', { expanded: true });
    expect(toggle).toHaveTextContent(formatBatchTimeRange('2026-03-20T16:02:00Z', '2026-03-20T16:08:00Z'));
    expect(toggle).toHaveTextContent('3 scans');
    expect(toggle).toHaveTextContent('stock in');
    expect(screen.getAllByLabelText('Unit count')).toHaveLength(3);
  });

  it('hides entry rows and select-all until the session is expanded', () => {
    renderSession({ expanded: false });
    expect(screen.queryByLabelText('Unit count')).not.toBeInTheDocument();
    expect(screen.queryByRole('checkbox', { name: /Select all eligible scans in/ })).not.toBeInTheDocument();
    expect(screen.getByRole('button', { expanded: false })).toHaveTextContent('3 scans');
  });

  it('selects only the eligible scans in this session', () => {
    const { onSelectionChange } = renderSession({ selectedIds: ['outside-batch'] });
    fireEvent.click(screen.getByRole('checkbox', { name: /Select all eligible scans in/ }));
    expect(onSelectionChange).toHaveBeenCalledWith(['outside-batch', 'oats', 'milk']);
  });

  it('deselects this session when every eligible scan in it is already selected', () => {
    const { onSelectionChange } = renderSession({ selectedIds: ['outside-batch', 'oats', 'milk'] });
    const selectAll = screen.getByRole('checkbox', { name: /Select all eligible scans in/ });
    expect(selectAll).toBeChecked();
    fireEvent.click(selectAll);
    expect(onSelectionChange).toHaveBeenCalledWith(['outside-batch']);
  });

  it('does not select a flagged scan in the session', () => {
    const { onSelectionChange } = renderSession();
    fireEvent.click(screen.getByRole('checkbox', { name: /Select all eligible scans in/ }));
    const next = onSelectionChange.mock.calls[0][0] as string[];
    expect(next).not.toContain('flagged');
  });

  it('disables select-all when the session has no eligible scans', () => {
    renderSession({
      entries: [scanEntry({ id: 'flagged-only', status: 'flagged', scannedAt: '2026-03-20T16:02:00Z' })],
    });
    expect(screen.getByRole('checkbox', { name: /Select all eligible scans in/ })).toBeDisabled();
  });
});
