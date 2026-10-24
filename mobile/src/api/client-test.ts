import { scansQuery, SESSION_PATH, scanCommitPath, BATCH_COMMIT_PATH, SCANNER_CONFIG_PATH, SCANNER_MODE_PATH, AUTH_NOT_CONFIGURED_SENTENCE, OFFLINE_SENTENCE, notifyApiSuccess, offlineBannerActive, raiseOfflineBanner, serverNoticeText, ServerNoticeError, subscribeApiSuccess, UnreachableError } from '@stockbot/shared/contract';
import authNotConfigured from '../../../testdata/contract/auth_not_configured.json';
import { basicAuthorization, createStockbotClient, HOUSEHOLD_USERNAME } from './client';

const serverUrl = 'https://stockbot.example';

function jsonResponse(status: number, body: unknown): Response {
  return {
    ok: status >= 200 && status < 300,
    status,
    statusText: status === 200 ? 'OK' : 'Error',
    text: async () => JSON.stringify(body),
    json: async () => body,
  } as Response;
}

describe('basicAuthorization', () => {
  it('encodes the household username and password as UTF-8 basic auth', () => {
    const header = basicAuthorization(HOUSEHOLD_USERNAME, 'päss');
    const encoded = header.slice('Basic '.length);
    const binary = atob(encoded);
    const bytes = Uint8Array.from(binary, (char) => char.charCodeAt(0));
    expect(new TextDecoder().decode(bytes)).toBe('pantry:päss');
  });
});

describe('createStockbotClient', () => {
  const password = 'correct-horse';

  afterEach(() => {
    notifyApiSuccess();
  });

  function clientWith(fetchImpl: typeof fetch, onUnauthorized = jest.fn()) {
    return {
      onUnauthorized,
      client: createStockbotClient({ serverUrl, password }, { fetchImpl, onUnauthorized }),
    };
  }

  it('validates the session with basic auth and reads a signed-in household', async () => {
    const fetchImpl = jest.fn(async (_input: RequestInfo | URL, _init?: RequestInit) => jsonResponse(200, { required: true, username: 'pantry' }));
    const { client } = clientWith(fetchImpl);

    await expect(client.getSession()).resolves.toEqual({ required: true, username: 'pantry', error: '' });

    expect(fetchImpl).toHaveBeenCalledWith(`${serverUrl}${SESSION_PATH}`, expect.objectContaining({
      headers: expect.any(Headers),
    }));
    const init = fetchImpl.mock.calls[0]?.[1];
    expect(new Headers(init?.headers).get('Authorization')).toBe(basicAuthorization(HOUSEHOLD_USERNAME, password));
  });

  it('rejects a wrong password without using the server wording', async () => {
    const { client, onUnauthorized } = clientWith(jest.fn(async () => jsonResponse(401, { error: 'Sign in required.' })));

    await expect(client.getSession()).rejects.toMatchObject({ status: 401, message: 'That password was rejected.' });
    expect(onUnauthorized).toHaveBeenCalled();
  });

  it.each([
    { name: 'network request failed', error: new TypeError('Network request failed') },
    { name: 'abort', error: Object.assign(new Error('The operation was aborted.'), { name: 'AbortError' }) },
    { name: 'timeout', error: Object.assign(new Error('The operation was aborted due to timeout'), { name: 'TimeoutError' }) },
    { name: 'dns', error: Object.assign(new Error('getaddrinfo ENOTFOUND'), { code: 'ENOTFOUND' }) },
  ])('$name maps to the offline sentence', async ({ error }) => {
    const { client } = clientWith(jest.fn(async () => {
      throw error;
    }));

    await expect(client.getSession()).rejects.toBeInstanceOf(UnreachableError);
    await expect(client.getSession()).rejects.toMatchObject({ kind: 'network', message: OFFLINE_SENTENCE });
  });

  it.each([
    { name: 'plain error', error: new Error('offline') },
    { name: 'invalid header', error: new TypeError("Failed to construct 'Headers': Invalid name") },
  ])('$name keeps the previous fallback', async ({ error }) => {
    const { client } = clientWith(jest.fn(async () => {
      throw error;
    }));

    await expect(client.getSession()).rejects.toMatchObject({
      status: 0,
      message: 'Stockbot did not answer at that address.',
    });
  });

  it('notifies listeners only after a response is read', async () => {
    const listener = jest.fn();
    const stop = subscribeApiSuccess(listener);
    try {
      const { client } = clientWith(jest.fn(async () => jsonResponse(200, { required: false })));
      await client.getSession();
      expect(listener).toHaveBeenCalledTimes(1);

      listener.mockClear();
      const failing = clientWith(jest.fn(async () => {
        throw new TypeError('Network request failed');
      }));
      await expect(failing.client.getSession()).rejects.toBeInstanceOf(UnreachableError);
      expect(listener).not.toHaveBeenCalled();
    } finally {
      stop();
    }
  });

  it('clears offline when Stockbot answers with an error', async () => {
    raiseOfflineBanner();
    const { client } = clientWith(jest.fn(async () => jsonResponse(500, { error: 'nope', code: 'internal' })));
    await expect(client.getSession()).rejects.toMatchObject({ status: 500, code: 'internal' });
    expect(offlineBannerActive()).toBe(false);
    expect(serverNoticeText()).toBe('');
  });

  it.each([
    { status: 502, body: '<html>gateway</html>' },
    { status: 503, body: '<html>unavailable</html>' },
    { status: 504, body: '' },
    { status: 530, body: 'tunnel' },
  ])('status $status with no code is offline', async ({ status, body }) => {
    raiseOfflineBanner();
    const { client } = clientWith(jest.fn(async () => ({
      ok: false,
      status,
      statusText: 'Bad Gateway',
      text: async () => body,
      json: async () => JSON.parse(body),
    }) as unknown as Response));
    await expect(client.listPending()).rejects.toBeInstanceOf(UnreachableError);
    await expect(client.listPending()).rejects.toMatchObject({ message: OFFLINE_SENTENCE });
    expect(offlineBannerActive()).toBe(true);
  });

  it.each([
    { name: 'busy', status: 503, code: 'busy', message: 'Stockbot is busy right now. Try again in a moment.', notice: '' },
    { name: 'no_space', status: 507, code: 'no_space', message: "Stockbot is out of storage space, so this wasn't saved.", notice: '' },
    { name: 'item failure', status: 409, code: 'not_pending', message: 'That scan is no longer pending.', notice: '' },
    { name: 'auth not configured', status: 503, code: authNotConfigured.code, message: AUTH_NOT_CONFIGURED_SENTENCE, notice: "Sign-in isn't set up yet." },
  ])('$name clears offline and keeps its own message', async ({ status, code, message, notice }) => {
    raiseOfflineBanner();
    const { client } = clientWith(jest.fn(async () => jsonResponse(status, { error: message, code })));
    const attempt = client.listPending();
    if (notice !== '') {
      await expect(attempt).rejects.toBeInstanceOf(ServerNoticeError);
      await expect(attempt).rejects.toMatchObject({ message: notice });
      expect(offlineBannerActive()).toBe(false);
      expect(serverNoticeText()).toBe(notice);
      return;
    }
    await expect(attempt).rejects.toMatchObject({ status, code, message });
    expect(offlineBannerActive()).toBe(false);
    expect(serverNoticeText()).toBe('');
  });

  it('clears the banner after a session is read', async () => {
    raiseOfflineBanner();
    const { client } = clientWith(jest.fn(async () => jsonResponse(200, { required: false })));
    await client.getSession();
    expect(offlineBannerActive()).toBe(false);
  });

  it('clears the auth banner on a later response and lets offline or busy replace it', async () => {
    const auth = async () => jsonResponse(503, { error: "Sign-in isn't set up yet.", code: authNotConfigured.code });
    const raised = clientWith(jest.fn(auth));
    await expect(raised.client.getSession()).rejects.toBeInstanceOf(ServerNoticeError);
    expect(serverNoticeText()).toBe("Sign-in isn't set up yet.");
    expect(serverNoticeText().charCodeAt(serverNoticeText().indexOf("'"))).toBe(39);

    const signedOut = clientWith(jest.fn(async () => jsonResponse(401, { error: 'Sign in required.' })));
    await expect(signedOut.client.getSession()).rejects.toMatchObject({ status: 401 });
    expect(serverNoticeText()).toBe('');

    const again = clientWith(jest.fn(auth));
    await expect(again.client.getSession()).rejects.toBeInstanceOf(ServerNoticeError);
    const busy = clientWith(jest.fn(async () => jsonResponse(503, {
      error: 'Stockbot is busy right now. Try again in a moment.',
      code: 'busy',
    })));
    await expect(busy.client.listPending()).rejects.toMatchObject({ code: 'busy' });
    expect(serverNoticeText()).toBe('');

    const authAgain = clientWith(jest.fn(auth));
    await expect(authAgain.client.getSession()).rejects.toBeInstanceOf(ServerNoticeError);
    const offline = clientWith(jest.fn(async () => {
      throw new TypeError('Network request failed');
    }));
    await expect(offline.client.listPending()).rejects.toBeInstanceOf(UnreachableError);
    expect(serverNoticeText()).toBe(OFFLINE_SENTENCE);
  });

  it('treats an HTML success and a failed body read as unreachable', async () => {
    const html = clientWith(jest.fn(async () => ({
      ok: true,
      status: 200,
      text: async () => '<html>',
    }) as unknown as Response));
    await expect(html.client.getSession()).rejects.toBeInstanceOf(UnreachableError);
    await expect(html.client.getSession()).rejects.toMatchObject({ message: OFFLINE_SENTENCE });
    expect(offlineBannerActive()).toBe(true);

    const dropped = clientWith(jest.fn(async () => ({
      ok: true,
      status: 200,
      text: async () => { throw new TypeError('Network request failed'); },
    }) as unknown as Response));
    await expect(dropped.client.listPending()).rejects.toBeInstanceOf(UnreachableError);
    await expect(dropped.client.listPending()).rejects.not.toMatchObject({ message: 'Network request failed' });
    expect(offlineBannerActive()).toBe(true);
  });

  it('posts a scan, lists pending scans, and commits them for the shared household user', async () => {
    const calls: { url: string; init?: RequestInit }[] = [];
    const fetchImpl = jest.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      calls.push({ url, init });
      if (url.endsWith(scansQuery('user-1', 'pending'))) {
        return jsonResponse(200, [{ id: 'scan-1' }]);
      }
      if (url.endsWith(scanCommitPath('scan-1'))) return jsonResponse(200, { id: 'scan-1', status: 'committed' });
      if (url.endsWith(BATCH_COMMIT_PATH)) return jsonResponse(200, { updatedCount: 2, committedIds: ['scan-1', 'scan-2'] });
      if (init?.method === 'POST' && url.endsWith('/api/scans')) {
        return jsonResponse(201, { id: 'scan-1', barcode: '111' });
      }
      if (init?.method === 'PATCH') return jsonResponse(200, { id: 'scan-1', status: 'cancelled' });
      return jsonResponse(500, { error: 'unexpected' });
    });
    const { client } = clientWith(fetchImpl);

    await client.createScan('111', 'stock_in');
    await expect(client.listPending()).resolves.toEqual([{ id: 'scan-1' }]);
    await client.commitScan('scan-1');
    await client.cancelScan('scan-1');
    await client.batchCommit(['scan-1', 'scan-2']);

    const post = calls.find((call) => call.init?.method === 'POST' && call.url.endsWith('/api/scans'));
    expect(post?.url).toBe(`${serverUrl}/api/scans`);
    expect(JSON.parse(String(post?.init?.body))).toEqual({ barcode: '111', direction: 'stock_in', userId: 'user-1' });
    expect(calls.some((call) => call.url === `${serverUrl}${scansQuery('user-1', 'pending')}`)).toBe(true);
    const commit = calls.find((call) => call.url.endsWith(scanCommitPath('scan-1')));
    expect(commit?.init?.method).toBe('POST');
    const cancel = calls.find((call) => call.init?.method === 'PATCH');
    expect(cancel?.url).toBe(`${serverUrl}/api/scans/scan-1`);
    expect(JSON.parse(String(cancel?.init?.body))).toEqual({ status: 'cancelled' });
    expect(JSON.parse(String(commit?.init?.body))).toEqual({});
    const batch = calls.find((call) => call.url.endsWith(BATCH_COMMIT_PATH));
    expect(JSON.parse(String(batch?.init?.body))).toEqual({ scanEntryIds: ['scan-1', 'scan-2'], commit: true });
    for (const call of calls) {
      expect(new Headers(call.init?.headers).get('Authorization')).toBe(basicAuthorization(HOUSEHOLD_USERNAME, password));
    }
  });

  it('keeps pantry suggestions when a stock-out commit is refused', async () => {
    const suggestions = [{ itemId: 'item-pears', productId: 'prod-pears', name: 'Sliced Pears', onHand: 3 }];
    const fetchImpl = jest.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const body = JSON.parse(String(init?.body)) as { linkProductId?: string };
      if (body.linkProductId === 'prod-pears') return jsonResponse(200, { id: 'scan-1', status: 'committed' });
      return jsonResponse(409, {
        error: 'Which item is this?',
        code: 'no_item',
        suggestions,
      });
    });
    const { client } = clientWith(fetchImpl);

    await expect(client.commitScan('scan-1')).rejects.toMatchObject({
      status: 409,
      code: 'no_item',
      message: 'Which item is this?',
      suggestions,
    });
    await expect(client.commitScan('scan-1', 'prod-pears')).resolves.toMatchObject({ status: 'committed' });
    const linkCall = fetchImpl.mock.calls.find((call) => String(call[1]?.body).includes('prod-pears'));
    expect(String(linkCall?.[0])).toBe(`${serverUrl}${scanCommitPath('scan-1')}`);
    expect(JSON.parse(String(linkCall?.[1]?.body))).toEqual({ linkProductId: 'prod-pears' });
    expect(new Headers(linkCall?.[1]?.headers).get('Authorization')).toBe(basicAuthorization(HOUSEHOLD_USERNAME, password));
  });

  it.each([
    { status: 500, body: JSON.stringify({ error: 'The shelf did not save.' }), message: 'The shelf did not save.' },
  ])('status $status uses the server error when present', async ({ status, body, message }) => {
    const fetchImpl = jest.fn(async () => ({
      ok: false,
      status,
      statusText: 'Bad Gateway',
      text: async () => body,
      json: async () => JSON.parse(body),
    } as Response));
    const { client } = clientWith(fetchImpl);

    await expect(client.getSession()).rejects.toMatchObject({ status, message });
  });

  it('reads household items from the inventory list, not the product catalog', async () => {
    const urls: string[] = [];
    const fetchImpl = jest.fn(async (input: RequestInfo | URL) => {
      urls.push(String(input));
      return jsonResponse(200, []);
    });
    const { client } = clientWith(fetchImpl);

    await expect(client.getInventoryList()).resolves.toEqual([]);
    expect(urls).toEqual([`${serverUrl}/api/inventory?userId=user-1`]);
  });

  it('reads and sets the shared scanner mode', async () => {
    const fetchImpl = jest.fn(async (url: RequestInfo | URL, init?: RequestInit) => {
      if (init?.method === 'POST') return jsonResponse(200, { mode: 'stock_out' });
      expect(String(url)).toBe(`${serverUrl}${SCANNER_CONFIG_PATH}`);
      return jsonResponse(200, {
        stockInBarcode: 'STOCK_IN',
        stockOutBarcode: 'STOCK_OUT',
        currentMode: 'stock_in',
        connected: false,
      });
    });
    const { client } = clientWith(fetchImpl);

    await expect(client.getScannerConfig()).resolves.toMatchObject({ currentMode: 'stock_in' });
    await expect(client.setScannerMode('stock_out')).resolves.toEqual({ mode: 'stock_out' });
    const modeCall = fetchImpl.mock.calls.find((call) => call[1]?.method === 'POST');
    expect(String(modeCall?.[0])).toBe(`${serverUrl}${SCANNER_MODE_PATH}`);
    expect(JSON.parse(String(modeCall?.[1]?.body))).toEqual({ mode: 'stock_out' });
  });
});
