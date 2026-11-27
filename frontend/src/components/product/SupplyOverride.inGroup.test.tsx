import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { MantineProvider } from '@mantine/core';
import { MemoryRouter } from 'react-router-dom';
import { describe, expect, it, vi } from 'vitest';
import { ApiError } from '../../api/client';
import { SupplyOverride } from './SupplyOverride';

vi.mock('../../api/client', async (importOriginal) => {
  const mod = await importOriginal<typeof import('../../api/client')>();
  return {
    ...mod,
    getSupplyOverride: vi.fn(async () => ({ quantity: 2 })),
    setSupplyOverride: vi.fn(async () => {
      throw new ApiError(
        409,
        'This product is in a group. Change what the group keeps on hand.',
        'in_group',
        [],
        [],
        '',
        undefined,
        '',
        '',
        [],
        undefined,
        undefined,
        { groupId: 'g1', groupName: 'Juice' },
      );
    }),
  };
});

describe('in_group on supply override', () => {
  it('shows inline sentence and Open group settings link', async () => {
    render(
      <MantineProvider>
        <MemoryRouter>
          <SupplyOverride productId="p1" />
        </MemoryRouter>
      </MantineProvider>,
    );
    await waitFor(() => expect(screen.getByLabelText('Quantity')).toBeInTheDocument());
    fireEvent.blur(screen.getByLabelText('Quantity'));
    await waitFor(() => {
      expect(screen.getByText(/This product is in a group/)).toBeInTheDocument();
      expect(screen.getByRole('link', { name: 'Open group settings ›' })).toHaveAttribute('href', '/groups/g1#keep');
    });
  });
});