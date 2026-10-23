import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import { render, screen } from '@testing-library/react';
import { MantineProvider } from '@mantine/core';
import { afterEach, describe, expect, it, vi } from 'vitest';
import App from '../../App';
import { notifyApiSuccess, OFFLINE_SENTENCE, raiseOfflineBanner } from '../../api/contract';
import { HouseholdGate } from '../auth/HouseholdGate';
import { ServerNoticeBanner } from './ServerNoticeBanner';

const authNotConfigured = JSON.parse(
  readFileSync(join(process.cwd(), '../testdata/contract/auth_not_configured.json'), 'utf8'),
) as { code: string };

class FakeEventSource {
  readonly url: string;
  constructor(url: string) {
    this.url = url;
  }
  addEventListener() {}
  close() {}
}

const json = (body: unknown, status = 200) =>
  new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } });

function painted(color: string): string {
  const match = /rgba?\((\d+),\s*(\d+),\s*(\d+)/.exec(color);
  if (!match) return color.trim().toLowerCase();
  const hex = match.slice(1, 4).map((channel) => Number(channel).toString(16).padStart(2, '0')).join('');
  return `#${hex}`;
}

describe('server notice banner', () => {
  afterEach(() => {
    notifyApiSuccess();
    vi.unstubAllGlobals();
    window.history.pushState({}, '', '/');
  });

  it('uses the pale red alert: tint, #EF4444 edge, dark text', () => {
    raiseOfflineBanner();
    render(
      <MantineProvider>
        <ServerNoticeBanner />
      </MantineProvider>,
    );
    const alert = screen.getByRole('alert');
    const style = getComputedStyle(alert);
    expect(painted(style.backgroundColor)).toBe('#ffeeee');
    expect(painted(style.borderTopColor)).toBe('#ef4444');
    expect(style.borderTopWidth).toBe('1px');
    const sentence = screen.getByText(OFFLINE_SENTENCE);
    expect(painted(getComputedStyle(sentence).color)).toBe('#000000');
    const icon = document.querySelector('.server-notice-icon circle');
    expect(icon?.getAttribute('stroke')).toBe('#EF4444');
  });

  it('keeps the setup banner inside the sign-in screen padding', async () => {
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL) => {
      if (String(input).endsWith('/api/session')) {
        return Promise.resolve(json({ error: "Sign-in isn't set up yet.", code: authNotConfigured.code }, 503));
      }
      return Promise.reject(new Error(String(input)));
    }));
    render(
      <MantineProvider>
        <HouseholdGate>
          <h1>Scan Queue</h1>
        </HouseholdGate>
      </MantineProvider>,
    );
    const banner = await screen.findByTestId('server-notice-banner');
    const main = banner.closest('main');
    expect(main).toHaveClass('login-screen');
    expect(main).toHaveStyle({
      paddingTop: '24px',
      paddingRight: '20px',
      paddingBottom: '24px',
      paddingLeft: '20px',
    });
    expect(screen.getByRole('button', { name: 'Try again' })).toBeVisible();
  });

  it('keeps the inventory banner inside the shell padding', async () => {
    vi.stubGlobal('EventSource', FakeEventSource);
    window.history.pushState({}, '', '/inventory');
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL) => {
      const url = String(input);
      if (url.includes('/api/session')) return Promise.resolve(json({ required: false }));
      return Promise.reject(new TypeError('Failed to fetch'));
    }));
    render(
      <MantineProvider>
        <App />
      </MantineProvider>,
    );
    const banner = await screen.findByTestId('server-notice-banner');
    expect(banner).toHaveTextContent(OFFLINE_SENTENCE);
    const shell = banner.closest('main');
    expect(shell?.className ?? '').toContain('AppShell-main');
    expect(getComputedStyle(shell!).getPropertyValue('--app-shell-padding').trim()).toBe('var(--mantine-spacing-xs)');
    const shellCss = readFileSync(join(process.cwd(), 'node_modules/@mantine/core/styles.css'), 'utf8');
    const padded = shellCss.match(/\.([a-z0-9_]+) \{[^{}]*padding-top: calc\(var\(--app-shell-header-offset, 0rem\) \+ var\(--app-shell-padding\)\)/);
    expect(padded?.[1]).toBeTruthy();
    expect(shell?.classList.contains(padded![1] ?? '')).toBe(true);
  });
});
