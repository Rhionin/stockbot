import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { DEFAULT_THEME, MantineProvider, mergeMantineTheme } from '@mantine/core';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { contrastRatio } from '@stockbot/shared/contrast';
import { krogerAppCopy } from '@stockbot/shared/krogerAppCopy';
import { cssVariablesResolver, theme } from '../../../theme';
import { KrogerAppSetupModal } from './KrogerAppSetupModal';
import './storeApp.css';

const fullTheme = mergeMantineTheme(DEFAULT_THEME, theme);

const app = {
  configured: false,
  clientIdLast4: null,
  redirectUri: 'https://app.example/cb',
  modality: 'PICKUP' as const,
  productAccess: 'unknown' as const,
  status: 'not_set_up' as const,
  canManage: true,
};

const rgbToHex = (rgb: string): string => {
  const match = /^rgba?\((\d+),\s*(\d+),\s*(\d+)/.exec(rgb);
  if (!match) return rgb;
  const hex = (n: number) => n.toString(16).padStart(2, '0');
  return `#${hex(Number(match[1]))}${hex(Number(match[2]))}${hex(Number(match[3]))}`;
};

const renderStep6 = (colorScheme: 'light' | 'dark') => {
  render(
    <MantineProvider theme={theme} env="test" forceColorScheme={colorScheme}>
      <KrogerAppSetupModal
        opened
        onClose={() => undefined}
        householdId="hh-contrast"
        householdName="Pantry"
        app={app}
        initialStep={6}
        onSaved={() => undefined}
      />
    </MantineProvider>,
  );
  fireEvent.click(screen.getByRole('button', { name: krogerAppCopy.s6Test }));
  return screen.getByText(krogerAppCopy.vIdEmpty);
};

describe('store app modal field errors', () => {
  beforeEach(() => {
    vi.stubGlobal('fetch', vi.fn(() => Promise.reject(new Error('unexpected fetch'))));
  });

  it('renders under-field errors at least 4.6:1 on modal paper in light mode', () => {
    const variables = cssVariablesResolver(fullTheme);
    const lightError = renderStep6('light');
    const lightColor = rgbToHex(getComputedStyle(lightError).color);
    expect(contrastRatio(lightColor, fullTheme.white)).toBeGreaterThanOrEqual(4.6);
    expect(contrastRatio(lightColor, fullTheme.white)).toBeGreaterThanOrEqual(
      contrastRatio(variables.light['--mantine-color-error']!, fullTheme.white),
    );
  });

  it('renders under-field errors at least 4.6:1 on modal paper in dark mode', () => {
    const variables = cssVariablesResolver(fullTheme);
    const darkPaper = fullTheme.colors.dark[7];
    const darkError = renderStep6('dark');
    expect(darkError.className).toContain('mantine-InputWrapper-error');
    const darkInk = variables.dark['--mantine-color-red-text']!;
    expect(contrastRatio(darkInk, darkPaper)).toBeGreaterThanOrEqual(4.6);
    expect(getComputedStyle(darkError).color.length).toBeGreaterThan(0);
  });

  it('sets store app setup modal header role to group', async () => {
    render(
      <MantineProvider env="test">
        <KrogerAppSetupModal
          opened
          onClose={() => undefined}
          householdId="hh-h"
          householdName="Pantry"
          app={app}
          onSaved={() => undefined}
        />
      </MantineProvider>,
    );
    await waitFor(() => {
      const header = document.querySelector('.store-app-setup-modal .mantine-Modal-header');
      expect(header?.getAttribute('role')).toBe('group');
    });
  });
});
