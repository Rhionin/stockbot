import { render, screen } from '@testing-library/react';
import { MantineProvider } from '@mantine/core';
import { describe, expect, it } from 'vitest';
import { TRIP_SKIP_REMOVAL_LEGACY_LINE } from '@stockbot/shared/shoppingListCopy';
import { TripSkipRemovalLine } from './TripSkipRemovalLine';

describe('TripSkipRemovalLine', () => {
  it('renders legacy skip copy without who or when', () => {
    render(
      <MantineProvider>
        <TripSkipRemovalLine skip={{}} />
      </MantineProvider>,
    );
    expect(screen.getByTestId('trip-skip-removal-line')).toHaveTextContent(TRIP_SKIP_REMOVAL_LEGACY_LINE);
  });

  it('renders actor and time when present', () => {
    render(
      <MantineProvider>
        <TripSkipRemovalLine
          skip={{ actorDisplayName: 'Alex', removedAt: '2026-11-09T15:00:00Z' }}
          format={{ formatInstant: () => 'Nov 9, 2026' }}
        />
      </MantineProvider>,
    );
    expect(screen.getByText(`${TRIP_SKIP_REMOVAL_LEGACY_LINE} by Alex on Nov 9, 2026`)).toBeInTheDocument();
  });
});
