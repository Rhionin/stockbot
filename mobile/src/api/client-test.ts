import { scansQuery, SESSION_PATH, scanCommitPath, BATCH_COMMIT_PATH, SCANNER_CONFIG_PATH, SCANNER_MODE_PATH, OFFLINE_SENTENCE, subscribeApiSuccess, UnreachableError } from '../../../frontend/src/api/contract';
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
    { status: 502, body: '', message: 'Something went wrong. Try again.' },
    { status: 503, body: '<html>unavailable</html>', message: 'Something went wrong. Try again.' },
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
