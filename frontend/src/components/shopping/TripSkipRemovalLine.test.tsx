import { render, screen } from '@testing-library/react';
import { MantineProvider } from '@mantine/core';
import { describe, expect, it } from 'vitest';
import { formatStagedCartSkipRemovalLine } from '@stockbot/shared/shoppingListCopy';
import { TripSkipRemovalLine } from './TripSkipRemovalLine';

describe('TripSkipRemovalLine', () => {
  it('renders legacy skip copy without who or when', () => {
    render(
      <MantineProvider>
        <TripSkipRemovalLine skip={{}} />
      </MantineProvider>,
    );
    expect(screen.getByTestId('trip-skip-removal-line')).toHaveTextContent(
      'Removed for this trip. It comes back next trip.',
    );
  });

  it('renders actor and time when present', () => {
    const line = formatStagedCartSkipRemovalLine(
      { actorDisplayName: 'Alex', removedAt: '2026-11-03T12:00:00Z' },
      { now: new Date(2026, 10, 9), formatWhen: () => 'Mon' },
    );
    render(
      <MantineProvider>
        <TripSkipRemovalLine
          skip={{ actorDisplayName: 'Alex', removedAt: '2026-11-03T12:00:00Z' }}
          format={{ now: new Date(2026, 10, 9), formatWhen: () => 'Mon' }}
        />
      </MantineProvider>,
    );
    expect(screen.getByText(line)).toBeInTheDocument();
  });
});
