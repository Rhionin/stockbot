import type { ReactElement } from 'react';
import { render, screen } from '@testing-library/react';
import { MantineProvider } from '@mantine/core';
import { describe, expect, it, vi } from 'vitest';
import type { OffListRow } from '@stockbot/shared/types';
import { ZeroForTripStatusBlock } from './ZeroForTripStatusBlock';

const row: OffListRow = {
  id: 'item:tea',
  itemId: 'tea',
  name: 'Green Tea',
  offListReason: 'zero_for_trip' as const,
  sent: 0,
  storeId: null,
  canSendToCart: false,
  needsAttention: null,
  zeroForTrip: {
    entryId: 'line-1',
    providerId: 'kroger',
    actorUserId: null,
    actorDisplayName: null,
    setAt: null,
  },
};

const restock = {
  household: { kind: 'months' as const, value: 2 },
  householdLabel: '2 months',
  appliesTo: 1,
  itemCount: 1,
  items: [{ itemId: 'tea', productId: 'p1', name: 'Green Tea', unit: 'boxes', target: { kind: 'amount' as const, value: 2 }, level: 'low', sourceLabel: '', valueLabel: '2', rowSource: '', chain: [], categories: [], setLabel: '' }],
  categories: [],
  notRestocked: 0,
  suggestionCount: 0,
};

const renderBlock = (ui: ReactElement) => render(<MantineProvider>{ui}</MantineProvider>);

describe('ZeroForTripStatusBlock', () => {
  it('renders status nudge for someone who can change the target', () => {
    renderBlock(
      <ZeroForTripStatusBlock
        row={row}
        actorFields={{}}
        restock={restock}
        groups={[]}
        nudge={{ onDontRestock: vi.fn() }}
      />,
    );
    expect(screen.getByText("Don't want it back next trip?")).toBeInTheDocument();
  });

  it('hides nudge without a restock row', () => {
    renderBlock(
      <ZeroForTripStatusBlock
        row={row}
        actorFields={{}}
        restock={{ ...restock, items: [] }}
        nudge={{ onDontRestock: vi.fn() }}
      />,
    );
    expect(screen.queryByText("Don't want it back next trip?")).not.toBeInTheDocument();
  });
});