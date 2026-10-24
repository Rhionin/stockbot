import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import { fireEvent, render, screen } from '@testing-library/react';
import { MantineProvider } from '@mantine/core';
import { afterEach, describe, expect, it, vi } from 'vitest';
import App from '../../App';
import { notifyApiSuccess, OFFLINE_SENTENCE, raiseOfflineBanner } from '../../api/contract';
import {
  NOTICE_SCREEN_PADDING_INLINE,
  NOTICE_SCREEN_PADDING_INLINE_VAR,
  NOTICE_SCREEN_PADDING_TOP,
  NOTICE_SCREEN_PADDING_TOP_VAR,
} from '../../noticeBanner';
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

function phoneShellSide(shellPadding: string, margin: string): number {
  const token = shellPadding.trim();
  const escaped = token.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
  const match = new RegExp(`^max\\(0px, calc\\((\\d+(?:\\.\\d+)?)px - ${escaped}\\)\\)$`).exec(margin.trim());
  if (!match) throw new Error(`margin ${margin} does not cancel shell padding ${token}`);
  return Number(match[1]);
}

function desktopShellMargin(css: string): { left: string; right: string; important: boolean } | undefined {
  const sheet = new CSSStyleSheet();
  sheet.replaceSync(css);
  for (const rule of sheet.cssRules) {
    if (!(rule instanceof CSSMediaRule) || !rule.conditionText.replace(/\s/g, '').includes('min-width:48em')) continue;
    for (const inner of rule.cssRules) {
      if (!(inner instanceof CSSStyleRule) || !inner.selectorText.split(',').includes('.server-notice-shell')) continue;
      return {
        left: inner.style.marginLeft,
        right: inner.style.marginRight,
        important: inner.style.getPropertyPriority('margin-left') === 'important'
          && inner.style.getPropertyPriority('margin-right') === 'important',
      };
    }
  }
  return undefined;
}

function loginRule(selector: string): CSSStyleRule {
  const css = readFileSync(join(process.cwd(), 'src/components/auth/LoginPage.css'), 'utf8');
  const sheet = new CSSStyleSheet();
  sheet.replaceSync(css);
  for (const rule of sheet.cssRules) {
    if (rule instanceof CSSStyleRule && rule.selectorText === selector) return rule;
  }
  throw new Error(`missing ${selector}`);
}

function published(name: string): string {
  const value = getComputedStyle(document.documentElement).getPropertyValue(name).trim();
  if (value === '') throw new Error(`${name} is unset`);
  return value;
}

// jsdom leaves max(var(), env(safe-area)) unresolved. Chromium paints that max,
// and with no notch the safe area is 0, so the variable is the used value.
function paints(declaration: string): string {
  const resolved = declaration
    .replace(/var\((--[\w-]+)\)/g, (_, name: string) => published(name))
    .replace(/env\(safe-area-inset-[a-z-]+\)/g, '0px');
  const max = /^max\((.+),\s*(.+)\)$/.exec(resolved.trim());
  if (!max) return resolved.trim();
  const px = (part: string) => {
    const match = /^(-?\d+(?:\.\d+)?)px$/.exec(part.trim());
    if (!match) throw new Error(`cannot paint ${declaration}`);
    return Number(match[1]);
  };
  return `${Math.max(px(max[1]), px(max[2]))}px`;
}

function expectPainted(rule: CSSStyleRule, property: string, variable: string, pixels: number) {
  const declaration = rule.style.getPropertyValue(property);
  expect(declaration).toContain(`var(${variable})`);
  expect(declaration).not.toMatch(/\d+px/);
  expect(rule.style.getPropertyPriority(property)).toBe('important');
  expect(paints(declaration)).toBe(`${pixels}px`);
}

function expectLoginScreenPadding(main: Element) {
  expect(main).toHaveClass('login-screen');
  expect((main as HTMLElement).style.padding).toBe('');
  expect((main as HTMLElement).style.paddingTop).toBe('');
  const rule = loginRule('.login-screen');
  expectPainted(rule, 'padding-top', NOTICE_SCREEN_PADDING_TOP_VAR, NOTICE_SCREEN_PADDING_TOP);
  expectPainted(rule, 'padding-bottom', NOTICE_SCREEN_PADDING_TOP_VAR, NOTICE_SCREEN_PADDING_TOP);
  expect(rule.style.getPropertyValue('padding-right')).toBe(`var(${NOTICE_SCREEN_PADDING_INLINE_VAR})`);
  expect(rule.style.getPropertyValue('padding-left')).toBe(`var(${NOTICE_SCREEN_PADDING_INLINE_VAR})`);
  expect(paints(rule.style.getPropertyValue('padding-right'))).toBe(`${NOTICE_SCREEN_PADDING_INLINE}px`);
  expect(paints(rule.style.getPropertyValue('padding-left'))).toBe(`${NOTICE_SCREEN_PADDING_INLINE}px`);
  expect(published(NOTICE_SCREEN_PADDING_TOP_VAR)).toBe(`${NOTICE_SCREEN_PADDING_TOP}px`);
  expect(published(NOTICE_SCREEN_PADDING_INLINE_VAR)).toBe(`${NOTICE_SCREEN_PADDING_INLINE}px`);
}

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
    expectLoginScreenPadding(banner.closest('main')!);
    expect(screen.getByRole('button', { name: 'Try again' })).toBeVisible();
  });

  it('pads the startup screen when Stockbot cannot be reached', async () => {
    vi.stubGlobal('fetch', vi.fn(() => Promise.reject(new TypeError('Failed to fetch'))));
    render(
      <MantineProvider>
        <HouseholdGate>
          <h1>Scan Queue</h1>
        </HouseholdGate>
      </MantineProvider>,
    );
    const banner = await screen.findByTestId('server-notice-banner');
    expect(banner).toHaveTextContent(OFFLINE_SENTENCE);
    expectLoginScreenPadding(banner.closest('main')!);
  });

  it('insets the sign-in banner by the screen padding', async () => {
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url.endsWith('/api/session')) return Promise.resolve(json({ error: 'Sign in required.' }, 401));
      if (url.endsWith('/api/login') && init?.method === 'POST') return Promise.reject(new TypeError('Failed to fetch'));
      return Promise.reject(new Error(url));
    }));
    render(
      <MantineProvider>
        <HouseholdGate>
          <h1>Scan Queue</h1>
        </HouseholdGate>
      </MantineProvider>,
    );
    fireEvent.change(await screen.findByLabelText(/Username/), { target: { value: 'pantry' } });
    fireEvent.change(screen.getByLabelText(/Password/), { target: { value: 'correct-horse-battery' } });
    fireEvent.click(screen.getByRole('button', { name: 'Sign in' }));
    const banner = await screen.findByTestId('server-notice-banner');
    expect(banner).toHaveTextContent(OFFLINE_SENTENCE);
    expect(banner).toHaveClass('server-notice-inset');
    expect(banner.style.marginTop).toBe('');
    expect(banner.style.marginRight).toBe('');
    expect(banner.style.marginLeft).toBe('');
    const inset = loginRule('.server-notice-inset');
    expectPainted(inset, 'margin-top', NOTICE_SCREEN_PADDING_TOP_VAR, NOTICE_SCREEN_PADDING_TOP);
    expectPainted(inset, 'margin-right', NOTICE_SCREEN_PADDING_INLINE_VAR, NOTICE_SCREEN_PADDING_INLINE);
    expectPainted(inset, 'margin-left', NOTICE_SCREEN_PADDING_INLINE_VAR, NOTICE_SCREEN_PADDING_INLINE);
  });

  it('gives the inventory banner 20px sides on a phone shell and lines it up from sm', async () => {
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
    const shellPadding = getComputedStyle(shell!).getPropertyValue('--app-shell-padding').trim();
    expect(shellPadding).toBe('var(--mantine-spacing-xs)');
    expect(banner).toHaveClass('server-notice-shell');
    expect(phoneShellSide(shellPadding, banner.style.marginLeft)).toBe(20);
    expect(phoneShellSide(shellPadding, banner.style.marginRight)).toBe(20);
    const appCss = readFileSync(join(process.cwd(), 'src/App.css'), 'utf8');
    expect(desktopShellMargin(appCss)).toEqual({ left: '0px', right: '0px', important: true });
  });
});
