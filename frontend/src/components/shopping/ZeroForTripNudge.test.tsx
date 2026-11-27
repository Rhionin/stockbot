import type { ReactElement } from 'react';
import { fireEvent, render, screen } from '@testing-library/react';
import { MantineProvider } from '@mantine/core';
import { describe, expect, it, vi } from 'vitest';
import { ZERO_FOR_TRIP_DONT_RESTOCK_ERROR, ZERO_FOR_TRIP_NUDGE_OFFLINE } from '@stockbot/shared/zeroForTripCopy';
import { ZeroForTripNudge } from './ZeroForTripNudge';

const renderNudge = (ui: ReactElement) => render(<MantineProvider>{ui}</MantineProvider>);

describe('ZeroForTripNudge', () => {
  it('calls Dont restock on tap', () => {
    const onDontRestock = vi.fn();
    renderNudge(
      <ZeroForTripNudge
        line={{ productName: 'Milk' }}
        onDontRestock={onDontRestock}
      />,
    );
    fireEvent.click(screen.getByRole('button', { name: "Don't restock Milk" }));
    expect(onDontRestock).toHaveBeenCalledTimes(1);
  });

  it('keeps button width while loading', () => {
    renderNudge(
      <ZeroForTripNudge line={{ productName: 'Milk' }} loading onDontRestock={vi.fn()} />,
    );
    const button = screen.getByRole('button', { name: "Don't restock Milk" });
    expect(button).toBeDisabled();
    expect(button.querySelector('.zero-for-trip-nudge-button-label')).toBeTruthy();
  });

  it('shows offline copy without the app name', () => {
    renderNudge(
      <ZeroForTripNudge line={{ productName: 'Milk' }} offline onDontRestock={vi.fn()} />,
    );
    expect(screen.getByText(ZERO_FOR_TRIP_NUDGE_OFFLINE)).toBeInTheDocument();
  });

  it('shows B3 beside the button and re-enables when not loading', () => {
    const { rerender } = renderNudge(
      <ZeroForTripNudge
        line={{ productName: 'Milk' }}
        error={ZERO_FOR_TRIP_DONT_RESTOCK_ERROR}
        onDontRestock={vi.fn()}
      />,
    );
    expect(screen.getByRole('alert')).toHaveTextContent(ZERO_FOR_TRIP_DONT_RESTOCK_ERROR);
    rerender(
      <MantineProvider>
        <ZeroForTripNudge line={{ productName: 'Milk' }} onDontRestock={vi.fn()} />
      </MantineProvider>,
    );
    expect(screen.getByRole('button', { name: "Don't restock Milk" })).toBeEnabled();
  });
});