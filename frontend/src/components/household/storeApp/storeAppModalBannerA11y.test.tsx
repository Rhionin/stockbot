import axe from 'axe-core';
import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { MantineProvider } from '@mantine/core';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { krogerAppCopy } from '@stockbot/shared/krogerAppCopy';
import { KrogerAppSetupModal } from './KrogerAppSetupModal';
import { StoreLinkPreStepDialog } from './StoreLinkPreStepDialog';
import './storeApp.css';

const app = {
  configured: false,
  clientIdLast4: null,
  redirectUri: 'https://app.example/cb',
  modality: 'PICKUP' as const,
  productAccess: 'unknown' as const,
  status: 'not_set_up' as const,
  canManage: true,
};

async function expectNoDuplicateBanner(root: Element | Document = document) {
  const results = await axe.run(root, {
    runOnly: { type: 'rule', values: ['landmark-no-duplicate-banner'] },
  });
  expect(results.violations).toEqual([]);
}

describe('store app modal banner landmarks', () => {
  beforeEach(() => {
    vi.stubGlobal('fetch', vi.fn(() => Promise.reject(new Error('unexpected fetch'))));
  });

  for (const colorScheme of ['light', 'dark'] as const) {
    it(`Close setup confirm header is role=group with clean landmark-no-duplicate-banner in ${colorScheme} mode`, async () => {
      const { unmount } = render(
        <MantineProvider env="test" forceColorScheme={colorScheme}>
          <KrogerAppSetupModal
            opened
            onClose={() => undefined}
            householdId="hh-banner"
            householdName="Test Household"
            app={app}
            initialStep={6}
            onSaved={() => undefined}
          />
        </MantineProvider>,
      );
      fireEvent.change(screen.getByLabelText(/App ID/i), { target: { value: 'x' } });
      fireEvent.click(screen.getByRole('button', { name: krogerAppCopy.closeLabel }));
      await screen.findByRole('dialog', { name: krogerAppCopy.closeConfirmTitle });
      await waitFor(() => {
        const header = document.querySelector('.store-app-close-confirm-modal .mantine-Modal-header');
        expect(header?.getAttribute('role')).toBe('group');
      });
      await expectNoDuplicateBanner();
      unmount();
    });

    it(`StoreLinkPreStepDialog header is role=group with clean landmark-no-duplicate-banner in ${colorScheme} mode`, async () => {
      const { unmount } = render(
        <MantineProvider env="test" forceColorScheme={colorScheme}>
          <StoreLinkPreStepDialog opened onClose={() => undefined} onContinue={() => undefined} />
        </MantineProvider>,
      );
      await waitFor(() => {
        const header = document.querySelector('.store-app-pre-step-modal .mantine-Modal-header');
        expect(header?.getAttribute('role')).toBe('group');
      });
      await expectNoDuplicateBanner();
      unmount();
    });
  }
});
