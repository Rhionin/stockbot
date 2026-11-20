import type { ReactElement } from 'react';
import { render, screen } from '@testing-library/react';
import { MantineProvider } from '@mantine/core';
import { describe, expect, it, vi } from 'vitest';
import { ChangeAmountWithSkipConfirm } from './ChangeAmountWithSkipConfirm';

const restock = {
  household: { kind: 'months' as const, value: 2 },
  householdLabel: '2 months',
  appliesTo: 1,
  itemCount: 1,
  items: [{
    itemId: 'tea',
    productId: 'p1',
    name: 'Green Tea',
    unit: 'boxes',
    target: { kind: 'amount' as const, value: 2 },
    level: 'low',
    sourceLabel: '',
    valueLabel: '2',
    rowSource: '',
    chain: [],
    categories: [],
    setLabel: '',
  }],
  categories: [],
  notRestocked: 0,
  suggestionCount: 0,
};

const renderConfirm = (ui: ReactElement) => render(<MantineProvider>{ui}</MantineProvider>);

describe('ChangeAmountWithSkipConfirm', () => {
  it('shows inline nudge after saving 0', () => {
    renderConfirm(
      <ChangeAmountWithSkipConfirm
        productName="Green Tea"
        entryId="line-1"
        line={{ itemId: 'tea', productName: 'Green Tea' }}
        restock={restock}
        defaultQuantity={0}
        inputKey="line-1-0-true"
        showInlineConfirm
        onBlurSave={vi.fn()}
        nudge={{ onDontRestock: vi.fn() }}
      />,
    );
    expect(screen.getByText('Skipped for this trip. It comes back next trip.')).toBeInTheDocument();
    expect(screen.getByText("Don't want it back next trip?")).toBeInTheDocument();
  });

  it('shows the inline confirm without the nudge when restock has no target row', () => {
    renderConfirm(
      <ChangeAmountWithSkipConfirm
        productName="Green Tea"
        entryId="line-1"
        line={{ itemId: 'tea', productName: 'Green Tea' }}
        restock={{ ...restock, items: [] }}
        defaultQuantity={0}
        inputKey="line-1-0-true"
        showInlineConfirm
        onBlurSave={vi.fn()}
        nudge={{ onDontRestock: vi.fn() }}
      />,
    );
    expect(screen.getByText('Skipped for this trip. It comes back next trip.')).toBeInTheDocument();
    expect(screen.queryByText("Don't want it back next trip?")).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: "Don't restock Green Tea" })).not.toBeInTheDocument();
  });

  it('removes the nudge when the skip is undone', () => {
    const { rerender } = renderConfirm(
      <ChangeAmountWithSkipConfirm
        productName="Green Tea"
        entryId="line-1"
        line={{ itemId: 'tea', productName: 'Green Tea' }}
        restock={restock}
        defaultQuantity={0}
        inputKey="line-1-0-true"
        showInlineConfirm
        onBlurSave={vi.fn()}
        nudge={{ onDontRestock: vi.fn() }}
      />,
    );
    expect(screen.getByText("Don't want it back next trip?")).toBeInTheDocument();
    rerender(
      <MantineProvider>
        <ChangeAmountWithSkipConfirm
          productName="Green Tea"
          entryId="line-1"
          line={{ itemId: 'tea', productName: 'Green Tea' }}
          restock={restock}
          defaultQuantity={2}
          inputKey="line-1-2-false"
          showInlineConfirm={false}
          onBlurSave={vi.fn()}
          nudge={{ onDontRestock: vi.fn() }}
        />
      </MantineProvider>,
    );
    expect(screen.queryByText("Don't want it back next trip?")).not.toBeInTheDocument();
  });
});