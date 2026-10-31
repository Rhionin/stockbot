import { fireEvent, render, screen } from '@testing-library/react';
import { MantineProvider } from '@mantine/core';
import { MemoryRouter } from 'react-router-dom';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { InviteAcceptPage } from './InviteAcceptPage';

const PM_HOUSEHOLD_RESTOCK_LABELS = [
  'New items restock automatically',
  'Ask before restocking new items',
  'Ask before restocking, and hold until you answer',
] as const;

const json = (body: unknown, status = 200) =>
  new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } });

describe('InviteAcceptPage', () => {
  beforeEach(() => {
    vi.stubEnv('TZ', 'America/Denver');
  });

  afterEach(() => {
    vi.unstubAllEnvs();
    vi.restoreAllMocks();
    vi.useRealTimers();
  });

  it('shows short local expiry copy without seconds', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    vi.setSystemTime(new Date(2026, 10, 5, 23, 45, 0));
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL) => {
      const url = String(input);
      if (url === '/api/invites/preview') {
        return Promise.resolve(json({
          kind: 'start_household',
          role: 'owner',
          expiresAt: '2026-11-06T17:00:00.000Z',
        }));
      }
      if (url === '/api/auth/session') {
        return Promise.resolve(json({
          user: null,
          kind: 'signed_out',
          memberships: [],
          households: [],
          activeHouseholdId: null,
          operator: false,
        }));
      }
      return Promise.reject(new Error(url));
    }));
    render(
      <MantineProvider env="test">
        <MemoryRouter>
          <InviteAcceptPage code="start-code" />
        </MemoryRouter>
      </MantineProvider>,
    );
    expect(await screen.findByTestId('invite-expires')).toHaveTextContent('Expires Nov 6, 10:00 AM');
    expect(screen.getByTestId('invite-expires').textContent).not.toMatch(/\d:\d{2}:\d{2}/);
  });

  it('selects a restock radio when tapping the bottom of the label row', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    vi.setSystemTime(new Date(2026, 10, 5, 23, 45, 0));
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL) => {
      const url = String(input);
      if (url === '/api/invites/preview') {
        return Promise.resolve(json({
          kind: 'start_household',
          role: 'owner',
          expiresAt: '2026-11-06T17:00:00.000Z',
        }));
      }
      if (url === '/api/auth/session') {
        return Promise.resolve(json({
          user: null,
          kind: 'signed_out',
          memberships: [],
          households: [],
          activeHouseholdId: null,
          operator: false,
        }));
      }
      return Promise.reject(new Error(url));
    }));
    render(
      <MantineProvider env="test">
        <MemoryRouter>
          <InviteAcceptPage code="start-code" />
        </MemoryRouter>
      </MantineProvider>,
    );
    await screen.findByRole('heading', { name: 'Start a household' });
    for (const label of PM_HOUSEHOLD_RESTOCK_LABELS) {
      expect(screen.getByRole('radio', { name: label })).toBeInTheDocument();
    }
    fireEvent.click(screen.getByLabelText('Ask before restocking new items'));
    expect(screen.getByRole('radio', { name: 'Ask before restocking new items' })).toBeChecked();
  });
});
