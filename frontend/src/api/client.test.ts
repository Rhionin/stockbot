import { afterEach, describe, expect, it, vi } from 'vitest';
import { ApiError, commitScanEntry, listProducts } from './client';
import { OFFLINE_SENTENCE, SERVER_ERROR_FALLBACK, UnreachableError } from './contract';

describe('api error messages', () => {
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it.each([
    {
      name: '502 with no body',
      status: 502,
      statusText: 'Bad Gateway',
      body: '',
      contentType: undefined,
      message: 'Something went wrong. Try again.',
    },
    {
      name: '503 with an HTML body',
      status: 503,
      statusText: 'Service Unavailable',
      body: '<html><body>migrating</body></html>',
      contentType: 'text/html',
      message: 'Something went wrong. Try again.',
    },
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
});
