import { render, screen } from '@testing-library/react';
import { MantineProvider } from '@mantine/core';
import { describe, expect, it } from 'vitest';
import type { RestockItem } from '../../restock/types';
import { plainCopy } from '../../restock/copy';
import { TargetCard } from './TargetCard';

const item: RestockItem = {
  itemId: 'pt',
  productId: 'pt',
  name: 'Pretzel twists',
  unit: 'bags',
  target: { kind: 'months', value: 2 },
  level: 'category',
  levelName: 'Snacks & sweets',
  sourceLabel: 'From Snacks & sweets',
  valueLabel: '2 months',
  rowSource: '2 months, from Snacks & sweets',
  chain: [
    { key: 'item', label: 'Item', value: '—', win: false, struck: false },
    { key: 'group', label: 'Group', value: '—', win: false, struck: false },
    { key: 'category', label: 'Category', value: '2 mo', win: true, struck: false },
    { key: 'household', label: 'Default', value: '3 mo', win: false, struck: true },
  ],
  categories: [{ id: 'snacks', name: 'Snacks & sweets' }],
  setLabel: 'Set for this item',
};

describe('TargetCard', () => {
  it('names the category that set the target', () => {
    render(<MantineProvider><TargetCard item={item} onEdit={() => {}} /></MantineProvider>);
    expect(screen.getByText('Snacks & sweets')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Set for this item' })).toBeInTheDocument();
    expect(screen.getByText('2 mo')).toBeInTheDocument();
    const text = document.body.textContent ?? '';
    expect(plainCopy(text)).toBe(true);
  });
});
