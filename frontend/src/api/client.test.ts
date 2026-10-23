import { afterEach, describe, expect, it, vi } from 'vitest';
import * as telemetry from '../telemetry/client';
import { ApiError, commitScanEntry, getSession, listProducts, login, logout } from './client';
import { AUTH_NOT_CONFIGURED_CODE, AUTH_NOT_CONFIGURED_SENTENCE, OFFLINE_SENTENCE, SERVER_ERROR_FALLBACK, ServerNoticeError, UnreachableError, notifyApiSuccess, offlineBannerActive, raiseOfflineBanner, serverNoticeText } from './contract';

describe('api error messages', () => {
  afterEach(() => {
    notifyApiSuccess();
    vi.unstubAllGlobals();
    vi.restoreAllMocks();
  });

  it.each([
    {
      name: '500 with an error field',
      status: 500,
      statusText: 'Internal Server Error',
      body: JSON.stringify({ error: 'The pantry is offline.' }),
      contentType: 'application/json',
      message: 'The pantry is offline.',
    },
  ])('$name', async ({ status, statusText, body, contentType, message }) => {
    expect(SERVER_ERROR_FALLBACK).toBe('Something went wrong. Try again.');
    const headers = contentType === undefined ? undefined : { 'Content-Type': contentType };
    vi.stubGlobal('fetch', vi.fn(() => Promise.resolve(new Response(body, { status, statusText, headers }))));

    const attempt = listProducts();
    await expect(attempt).rejects.toBeInstanceOf(ApiError);
    await expect(attempt).rejects.toMatchObject({ status, message });
  });

  it.each([
    { name: 'failed to fetch', error: new TypeError('Failed to fetch') },
    { name: 'abort', error: new DOMException('The operation was aborted.', 'AbortError') },
    { name: 'timeout', error: new DOMException('The operation was aborted due to timeout', 'TimeoutError') },
    { name: 'dns', error: Object.assign(new Error('getaddrinfo ENOTFOUND'), { code: 'ENOTFOUND' }) },
  ])('$name', async ({ error }) => {
    vi.stubGlobal('fetch', vi.fn(() => Promise.reject(error)));

    const attempt = listProducts();
    await expect(attempt).rejects.toBeInstanceOf(UnreachableError);
    await expect(attempt).rejects.toMatchObject({ kind: 'network', message: OFFLINE_SENTENCE });
  });

  it('leaves a TypeError raised before the request is sent', async () => {
    const error = new TypeError("Failed to construct 'Headers': Invalid name");
    vi.stubGlobal('fetch', vi.fn(() => Promise.reject(error)));

    await expect(listProducts()).rejects.toBe(error);
  });

  it('keeps a 409 body', async () => {
    vi.stubGlobal('fetch', vi.fn(() => Promise.resolve(new Response(
      JSON.stringify({ error: 'That scan is no longer pending.', code: 'not_pending' }),
      { status: 409, headers: { 'Content-Type': 'application/json' } },
    ))));

    await expect(commitScanEntry('scan-1')).rejects.toMatchObject({
      status: 409,
      message: 'That scan is no longer pending.',
      code: 'not_pending',
    });
  });

  it('clears offline when Stockbot answers with an error', async () => {
    raiseOfflineBanner();
    vi.stubGlobal('fetch', vi.fn(() => Promise.resolve(new Response(
      JSON.stringify({ error: 'Something went wrong. Try again.', code: 'internal' }),
      { status: 500, headers: { 'Content-Type': 'application/json' } },
    ))));

    await expect(listProducts()).rejects.toBeInstanceOf(ApiError);
    expect(offlineBannerActive()).toBe(false);
    expect(serverNoticeText()).toBe('');
  });

  it.each([
    { status: 502, body: '<html>gateway</html>', code: '' },
    { status: 503, body: '<html>unavailable</html>', code: '' },
    { status: 504, body: '', code: '' },
    { status: 530, body: 'tunnel down', code: '' },
    { status: 503, body: JSON.stringify({ error: 'down' }), code: '' },
  ])('status $status with no code is offline', async ({ status, body }) => {
    raiseOfflineBanner();
    vi.stubGlobal('fetch', vi.fn(() => Promise.resolve(new Response(body, { status, statusText: 'Bad Gateway' }))));
    const attempt = listProducts();
    await expect(attempt).rejects.toBeInstanceOf(UnreachableError);
    await expect(attempt).rejects.toMatchObject({ message: OFFLINE_SENTENCE });
    expect(offlineBannerActive()).toBe(true);
    expect(serverNoticeText()).toBe(OFFLINE_SENTENCE);
  });

  it.each([
    { name: 'busy', status: 503, code: 'busy', message: 'Stockbot is busy right now. Try again in a moment.' },
    { name: 'no_space', status: 507, code: 'no_space', message: "Stockbot is out of storage space, so this wasn't saved." },
    { name: 'item failure', status: 409, code: 'not_pending', message: 'That scan is no longer pending.' },
    { name: 'coded 500', status: 500, code: 'internal', message: 'Something went wrong. Try again.' },
  ])('$name clears offline and keeps its own message', async ({ status, code, message }) => {
    raiseOfflineBanner();
    vi.stubGlobal('fetch', vi.fn(() => Promise.resolve(new Response(
      JSON.stringify({ error: message, code }),
      { status, headers: { 'Content-Type': 'application/json' } },
    ))));
    await expect(listProducts()).rejects.toMatchObject({ status, code, message });
    expect(offlineBannerActive()).toBe(false);
    expect(serverNoticeText()).toBe('');
  });

  it('maps auth_not_configured to the sign-in banner', async () => {
    raiseOfflineBanner();
    vi.stubGlobal('fetch', vi.fn(() => Promise.resolve(new Response(
      JSON.stringify({ error: 'auth is off', code: AUTH_NOT_CONFIGURED_CODE }),
      { status: 503, headers: { 'Content-Type': 'application/json' } },
    ))));
    await expect(listProducts()).rejects.toBeInstanceOf(ServerNoticeError);
    await expect(listProducts()).rejects.toMatchObject({ message: AUTH_NOT_CONFIGURED_SENTENCE });
    expect(offlineBannerActive()).toBe(false);
    expect(serverNoticeText()).toBe(AUTH_NOT_CONFIGURED_SENTENCE);
  });

  it('reads a session failure the same way', async () => {
    vi.stubGlobal('fetch', vi.fn(() => Promise.resolve(new Response(
      '<html>portal</html>',
      { status: 503, headers: { 'Content-Type': 'text/html' } },
    ))));
    await expect(getSession()).rejects.toBeInstanceOf(UnreachableError);
    expect(serverNoticeText()).toBe(OFFLINE_SENTENCE);

    vi.stubGlobal('fetch', vi.fn(async () => ({
      ok: false,
      status: 503,
      text: () => Promise.reject(new TypeError('network error')),
    }) as Response));
    await expect(getSession()).rejects.toBeInstanceOf(UnreachableError);

    vi.stubGlobal('fetch', vi.fn(() => Promise.resolve(new Response(
      JSON.stringify({ error: 'auth is off', code: AUTH_NOT_CONFIGURED_CODE }),
      { status: 503, headers: { 'Content-Type': 'application/json' } },
    ))));
    await expect(getSession()).resolves.toEqual({ required: true, username: '', error: '' });
    expect(serverNoticeText()).toBe(AUTH_NOT_CONFIGURED_SENTENCE);

    raiseOfflineBanner();
    vi.stubGlobal('fetch', vi.fn(() => Promise.resolve(new Response(
      JSON.stringify({ error: 'nope' }),
      { status: 500, headers: { 'Content-Type': 'application/json' } },
    ))));
    await expect(getSession()).resolves.toEqual({ required: true, username: '', error: 'nope' });
    expect(offlineBannerActive()).toBe(false);
  });

  it('does not record 200 when the body is treated as unreachable', async () => {
    const report = vi.spyOn(telemetry, 'reportApiResult').mockImplementation(() => undefined);
    vi.stubGlobal('fetch', vi.fn(async () => ({
      ok: true,
      status: 200,
      text: () => Promise.reject(new TypeError('network error')),
    }) as Response));
    await expect(listProducts()).rejects.toBeInstanceOf(UnreachableError);
    expect(report).toHaveBeenCalled();
    expect(report.mock.calls.some((call) => call[1] === 200)).toBe(false);
    expect(report.mock.calls.some((call) => call[1] === 0)).toBe(true);

    report.mockClear();
    vi.stubGlobal('fetch', vi.fn(() => Promise.resolve(new Response(
      '<html>',
      { status: 200, headers: { 'Content-Type': 'text/html' } },
    ))));
    await expect(listProducts()).rejects.toBeInstanceOf(UnreachableError);
    expect(report.mock.calls.some((call) => call[1] === 200)).toBe(false);
    expect(report.mock.calls.some((call) => call[1] === 0)).toBe(true);

    report.mockClear();
    vi.stubGlobal('fetch', vi.fn(() => Promise.resolve(new Response('', { status: 200 }))));
    await expect(listProducts()).resolves.toBeUndefined();
    expect(report.mock.calls.some((call) => call[1] === 200)).toBe(true);
  });

  it('clears the banner when session, login, and logout succeed', async () => {
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL) => {
      const url = String(input);
      if (url.endsWith('/api/session')) return Promise.resolve(new Response(JSON.stringify({ required: false }), { status: 200 }));
      if (url.endsWith('/api/login')) return Promise.resolve(new Response('{}', { status: 200 }));
      if (url.endsWith('/api/logout')) return Promise.resolve(new Response('{}', { status: 200 }));
      return Promise.reject(new Error(url));
    }));

    raiseOfflineBanner();
    await getSession();
    expect(offlineBannerActive()).toBe(false);
    raiseOfflineBanner();
    await login('pantry', 'secret');
    expect(offlineBannerActive()).toBe(false);
    raiseOfflineBanner();
    await logout();
    expect(offlineBannerActive()).toBe(false);
  });

  it('treats an HTML success and a failed body read as unreachable', async () => {
    vi.stubGlobal('fetch', vi.fn(() => Promise.resolve(new Response(
      '<html><body>portal</body></html>',
      { status: 200, headers: { 'Content-Type': 'text/html' } },
    ))));
    await expect(listProducts()).rejects.toBeInstanceOf(UnreachableError);
    await expect(listProducts()).rejects.toMatchObject({ message: OFFLINE_SENTENCE });
    expect(offlineBannerActive()).toBe(true);

    vi.stubGlobal('fetch', vi.fn(async () => ({
      ok: true,
      status: 200,
      text: () => Promise.reject(new TypeError('network error')),
    }) as Response));
    await expect(listProducts()).rejects.toBeInstanceOf(UnreachableError);
    await expect(listProducts()).rejects.not.toMatchObject({ message: 'network error' });
    expect(offlineBannerActive()).toBe(true);
  });
});
