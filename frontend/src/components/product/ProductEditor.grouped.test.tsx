import { render, screen } from '@testing-library/react';
import { MantineProvider } from '@mantine/core';
import { MemoryRouter } from 'react-router-dom';
import { describe, expect, it } from 'vitest';
import { GroupedProductSettings } from '../groups/GroupedProductSettings';
import { goalTarget } from '../groups/syntheticGroup';

describe('grouped product settings (D1/D2)', () => {
  it('D1 and D2a when the product belongs to a group', () => {
    render(
      <MantineProvider>
        <MemoryRouter>
          <GroupedProductSettings
            ctx={{
              groupId: 'g-snacks',
              groupName: 'Synth Snacks',
              target: goalTarget({ kind: 'items', value: 2, unit: 'items', status: 'ok' }),
              defaultKind: 'months',
              defaultMonths: 3,
            }}
          />
        </MemoryRouter>
      </MantineProvider>,
    );
    expect(screen.getByText('This product is part of the Synth Snacks group.')).toBeInTheDocument();
    expect(screen.getByText('The group keeps 2 items on hand.')).toBeInTheDocument();
    expect(screen.getByRole('link', { name: 'Open group settings ›' })).toHaveAttribute('href', '/groups/g-snacks#keep');
  });
});