import { afterEach, describe, expect, it, vi } from 'vitest';
import { ApiError, listProducts } from './client';
import { SERVER_ERROR_FALLBACK } from './contract';

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
});
