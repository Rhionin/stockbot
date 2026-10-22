/// <reference types="node" />
import type { ReactNode } from 'react';
import { readFileSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { fireEvent, render, screen, within } from '@testing-library/react';
import { MantineProvider } from '@mantine/core';
import { describe, expect, it, vi } from 'vitest';
import { ItemRow } from './ItemRow';
import type { InventoryItem } from '../../types';

const styleDir = dirname(fileURLToPath(import.meta.url));
const menuCss = [
  readFileSync(join(styleDir, '../restock/restock.css'), 'utf8'),
  readFileSync(join(styleDir, '../groups/groups.css'), 'utf8'),
].join('\n');

const inventoryItem: InventoryItem = {
  item: {
    id: 'item-1',
    userId: 'user-1',
    productId: 'product-1',
    product: {
      id: 'product-1',
      name: 'Milk',
      category: 'Dairy',
      unitOfMeasure: 'carton',
    },
    targetQuantity: null,
    createdAt: '2026-01-01T00:00:00Z',
  },
  instanceCount: 3,
  nearExpiryCount: 0,
  expiredCount: 0,
  needsAttention: false,
};

const renderRow = (
  item: InventoryItem = inventoryItem,
  props: { selected?: boolean; children?: ReactNode } = {},
) => render(
  <MantineProvider>
    <ItemRow
      inventoryItem={item}
      hand={`${item.instanceCount} ${item.item.product.unitOfMeasure}`}
      selected={props.selected ?? false}
      controlsId="controls-item-1"
      onSelect={vi.fn()}
      onAdd={vi.fn()}
      onStartGroup={vi.fn()}
    >
      {props.children}
    </ItemRow>
  </MantineProvider>,
);

const withProduct = (product: Partial<InventoryItem['item']['product']>): InventoryItem => ({
  ...inventoryItem,
  item: {
    ...inventoryItem.item,
    product: { ...inventoryItem.item.product, ...product },
  },
});

describe('ItemRow', () => {
  it('colors a stocked row full and an empty row out', () => {
    const { rerender } = renderRow();
    expect(document.querySelector('.shelf-row')).toHaveAttribute('data-stock', 'full');

    rerender(
      <MantineProvider>
        <ItemRow
          inventoryItem={{ ...inventoryItem, instanceCount: 0 }}
          hand="0 carton"
          selected={false}
          controlsId="controls-item-1"
          onSelect={vi.fn()}
          onAdd={vi.fn()}
          onStartGroup={vi.fn()}
        />
      </MantineProvider>,
    );
    expect(document.querySelector('.shelf-row')).toHaveAttribute('data-stock', 'out');
  });

  it('names the data source in quiet text instead of a control', () => {
    renderRow(withProduct({ externalSource: 'openfoodfacts' }));

    const source = screen.getByLabelText('Product data from Open Food Facts');
    expect(source).toHaveTextContent('Open Food Facts');
    expect(source.closest('.mantine-Badge-root, a, button')).toBeNull();
    expect(screen.queryByRole('link', { name: /Open Food Facts/i })).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: /Open Food Facts/i })).not.toBeInTheDocument();
  });

  it('omits the source line when the product has no external source', () => {
    renderRow();

    expect(screen.queryByLabelText(/Product data from/)).not.toBeInTheDocument();
  });

  it('shows a real category and hides missing category text', () => {
    const { rerender } = renderRow();
    expect(screen.getByText('Dairy')).toBeInTheDocument();

    for (const category of ['', '   ', 'undefined', 'UNDEFINED', 'null', 'Null']) {
      rerender(
        <MantineProvider>
          <ItemRow
            inventoryItem={withProduct({ category })}
            hand="3 carton"
            selected={false}
            controlsId="controls-item-1"
            onSelect={vi.fn()}
            onAdd={vi.fn()}
            onStartGroup={vi.fn()}
          />
        </MantineProvider>,
      );
      const shown = category.trim();
      if (shown !== '') expect(screen.queryByText(shown)).not.toBeInTheDocument();
      expect(screen.queryByText('Dairy')).not.toBeInTheDocument();
      expect(screen.getByRole('heading', { name: 'Milk' })).toBeInTheDocument();
    }
  });

  it('keeps instances inside the product and uses a compact disclosure', () => {
    renderRow(inventoryItem, {
      selected: true,
      children: <p>Stocked in Jan 1, 2026</p>,
    });

    const article = screen.getByRole('article');
    expect(within(article).getByText('Stocked in Jan 1, 2026')).toBeInTheDocument();
    fireEvent.click(within(article).getByRole('button', { name: 'Actions for Milk' }));
    const disclosure = screen.getByRole('menuitem', { name: 'Hide instances' });
    expect(disclosure).toBeInTheDocument();
  });

  it('puts the don\'t-restock hint on its own line', () => {
    render(
      <MantineProvider>
        <ItemRow
          inventoryItem={inventoryItem}
          hand="3 carton"
          selected={false}
          controlsId="controls-item-1"
          onSelect={vi.fn()}
          onAdd={vi.fn()}
          onStartGroup={vi.fn()}
          onDontRestock={vi.fn()}
          dontRestockHint="Sets this item's target to 0"
        />
      </MantineProvider>,
    );

    const style = document.createElement('style');
    style.textContent = menuCss;
    document.head.appendChild(style);
    try {
      fireEvent.click(screen.getByRole('button', { name: 'Actions for Milk' }));
      const item = screen.getByRole('menuitem', { name: /Don't restock/ });
      const label = item.querySelector('.bin-menu-label');
      const hint = item.querySelector('.bin-menu-hint');
      expect(label).toHaveTextContent("Don't restock");
      expect(hint).toHaveTextContent("Sets this item's target to 0");
      expect(hint).not.toHaveClass('restock-quiet');
      expect(getComputedStyle(label as Element).display).toBe('block');
      expect(getComputedStyle(hint as Element).display).toBe('block');
    } finally {
      style.remove();
    }
  });
});
