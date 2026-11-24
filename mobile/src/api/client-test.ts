import { AUTH_LOGIN_PATH, AUTH_LOGOUT_PATH, AUTH_SESSION_PATH, CLIENT_HEADER, SESSION_EXPIRED_SENTENCE, SWITCH_HOUSEHOLD_PATH, UPGRADE_REQUIRED_SENTENCE, scansQuery, scanCommitPath, BATCH_COMMIT_PATH, SCANNER_CONFIG_PATH, SCANNER_MODE_PATH, AUTH_NOT_CONFIGURED_SENTENCE, INVALID_CREDENTIALS_SENTENCE, OFFLINE_SENTENCE, UPDATING_SENTENCE, notifyApiSuccess, offlineBannerActive, raiseOfflineBanner, serverNoticeText, ServerNoticeError, subscribeApiSuccess, UnreachableError } from '@stockbot/shared/contract';
import authNotConfigured from '../../../testdata/contract/auth_not_configured.json';
import { createStockbotClient } from './client';

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

describe('createStockbotClient', () => {
  const token = 'sbs_test-token';

  afterEach(() => {
    notifyApiSuccess();
  });

  function clientWith(fetchImpl: typeof fetch, onUnauthorized = jest.fn(), onUpgradeRequired = jest.fn()) {
    return {
      onUnauthorized,
      onUpgradeRequired,
      client: createStockbotClient(
        { serverUrl, token },
        { fetchImpl, onUnauthorized, onUpgradeRequired, appVersion: '1.2.3' },
      ),
    };
  }

  const signedOutAccount = {
    user: null,
    kind: 'signed_out',
    memberships: [],
    households: [],
    activeHouseholdId: null,
    operator: false,
  };

  it('sends the Expo client header and the Bearer token on a real request', async () => {
    const fetchImpl = jest.fn(async (_input: RequestInfo | URL, _init?: RequestInit) => jsonResponse(200, signedOutAccount));
    const { client } = clientWith(fetchImpl);

    await client.getAuthSession();

    expect(fetchImpl).toHaveBeenCalledWith(`${serverUrl}${AUTH_SESSION_PATH}`, expect.objectContaining({
      headers: expect.any(Headers),
    }));
    const headers = new Headers(fetchImpl.mock.calls[0]?.[1]?.headers);
    expect(headers.get(CLIENT_HEADER)).toBe('expo/1.2.3');
    expect(headers.get('Authorization')).toBe(`Bearer ${token}`);
  });

  it('sends the client header and no Authorization before sign-in', async () => {
    const fetchImpl = jest.fn(async (_input: RequestInfo | URL, _init?: RequestInit) => jsonResponse(200, { ...signedOutAccount, token: 'sbs_new' }));
    const client = createStockbotClient({ serverUrl, token: null }, { fetchImpl, appVersion: '1.2.3' });

    await expect(client.login('ada@example.test', 'correct horse battery')).resolves.toMatchObject({ token: 'sbs_new' });

    expect(fetchImpl.mock.calls[0]?.[0]).toBe(`${serverUrl}${AUTH_LOGIN_PATH}`);
    const init = fetchImpl.mock.calls[0]?.[1];
    expect(init?.method).toBe('POST');
    expect(JSON.parse(String(init?.body))).toEqual({ email: 'ada@example.test', password: 'correct horse battery' });
    const headers = new Headers(init?.headers);
    expect(headers.get(CLIENT_HEADER)).toBe('expo/1.2.3');
    expect(headers.has('Authorization')).toBe(false);
  });

  it('uses the cookie session on web: same-origin credentials, no client header, no Bearer token', async () => {
    const fetchImpl = jest.fn(async (_input: RequestInfo | URL, _init?: RequestInit) => jsonResponse(200, signedOutAccount));
    const client = createStockbotClient({ serverUrl, token }, { fetchImpl, cookieSession: true });

    await client.getAuthSession();

    const init = fetchImpl.mock.calls[0]?.[1];
    expect(init?.credentials).toBe('same-origin');
    const headers = new Headers(init?.headers);
    expect(headers.has(CLIENT_HEADER)).toBe(false);
    expect(headers.has('Authorization')).toBe(false);
  });

  it('switches household and signs out with the shared paths', async () => {
    const fetchImpl = jest.fn(async (_input: RequestInfo | URL, _init?: RequestInit) => jsonResponse(200, signedOutAccount));
    const { client } = clientWith(fetchImpl);

    await client.switchHousehold('hh-2');
    await client.logout();

    expect(fetchImpl.mock.calls[0]?.[0]).toBe(`${serverUrl}${SWITCH_HOUSEHOLD_PATH}`);
    expect(JSON.parse(String(fetchImpl.mock.calls[0]?.[1]?.body))).toEqual({ householdId: 'hh-2' });
    expect(fetchImpl.mock.calls[1]?.[0]).toBe(`${serverUrl}${AUTH_LOGOUT_PATH}`);
    expect(fetchImpl.mock.calls[1]?.[1]?.method).toBe('POST');
  });

  it('reports an expired session on a 401 away from the sign-in routes', async () => {
    const { client, onUnauthorized } = clientWith(jest.fn(async () => jsonResponse(401, {
      error: SESSION_EXPIRED_SENTENCE,
      code: 'unauthorized',
    })));

    await expect(client.listPending()).rejects.toMatchObject({ status: 401 });
    await expect(client.getAuthSession()).rejects.toMatchObject({ status: 401 });
    expect(onUnauthorized).toHaveBeenCalledTimes(2);
  });

  it('does not report an expired session when the sign-in request itself gets a 401', async () => {
    const { client, onUnauthorized } = clientWith(jest.fn(async () => jsonResponse(401, {
      error: SESSION_EXPIRED_SENTENCE,
      code: 'unauthorized',
    })));

    await expect(client.login('ada@example.test', 'wrong password')).rejects.toMatchObject({ status: 401 });
    expect(onUnauthorized).not.toHaveBeenCalled();
  });

  it.each([
    { name: '426 with the code', status: 426, body: { error: 'x', code: 'upgrade_required' } },
    { name: '426 without a body', status: 426, body: {} },
  ])('$name shows the update sentence and is not a sign-out', async ({ status, body }) => {
    const { client, onUnauthorized, onUpgradeRequired } = clientWith(jest.fn(async () => jsonResponse(status, body)));

    await expect(client.listPending()).rejects.toMatchObject({
      status: 426,
      code: 'upgrade_required',
      message: UPGRADE_REQUIRED_SENTENCE,
    });
    expect(onUpgradeRequired).toHaveBeenCalledTimes(1);
    expect(onUnauthorized).not.toHaveBeenCalled();
  });

  it('keeps the server sentence on a 401', async () => {
    const { client, onUnauthorized } = clientWith(jest.fn(async () => jsonResponse(401, {
      error: 'Sign in required.',
      code: 'unauthorized',
    })));

    await expect(client.getAuthSession()).rejects.toMatchObject({
      status: 401,
      code: 'unauthorized',
      message: 'Sign in required.',
    });
    expect(onUnauthorized).toHaveBeenCalled();
  });

  it('keeps the wrong-password sentence', async () => {
    const { client } = clientWith(jest.fn(async () => jsonResponse(401, {
      error: INVALID_CREDENTIALS_SENTENCE,
      code: 'unauthorized',
    })));

    await expect(client.getAuthSession()).rejects.toMatchObject({
      status: 401,
      code: 'unauthorized',
      message: INVALID_CREDENTIALS_SENTENCE,
    });
  });

  it('keeps item id and on hand from the error body', async () => {
    const { client } = clientWith(jest.fn(async () => jsonResponse(409, {
      error: 'Nothing on hand to take out.',
      code: 'none_on_hand',
      itemId: 'item-1',
      onHand: 0,
    })));

    await expect(client.commitScan('scan-1')).rejects.toMatchObject({
      status: 409,
      code: 'none_on_hand',
      itemId: 'item-1',
      onHand: 0,
    });
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

    await expect(client.getAuthSession()).rejects.toBeInstanceOf(UnreachableError);
    await expect(client.getAuthSession()).rejects.toMatchObject({ kind: 'network', message: OFFLINE_SENTENCE });
  });

  it.each([
    { name: 'plain error', error: new Error('offline') },
    { name: 'invalid header', error: new TypeError("Failed to construct 'Headers': Invalid name") },
  ])('$name keeps the previous fallback', async ({ error }) => {
    const { client } = clientWith(jest.fn(async () => {
      throw error;
    }));

    await expect(client.getAuthSession()).rejects.toMatchObject({
      status: 0,
      message: 'Stockbot did not answer at that address.',
    });
  });

  it('notifies listeners only after a response is read', async () => {
    const listener = jest.fn();
    const stop = subscribeApiSuccess(listener);
    try {
      const { client } = clientWith(jest.fn(async () => jsonResponse(200, { required: false })));
      await client.getAuthSession();
      expect(listener).toHaveBeenCalledTimes(1);

      listener.mockClear();
      const failing = clientWith(jest.fn(async () => {
        throw new TypeError('Network request failed');
      }));
      await expect(failing.client.getAuthSession()).rejects.toBeInstanceOf(UnreachableError);
      expect(listener).not.toHaveBeenCalled();
    } finally {
      stop();
    }
  });

  it('clears offline when Stockbot answers with an error', async () => {
    raiseOfflineBanner();
    const { client } = clientWith(jest.fn(async () => jsonResponse(500, { error: 'nope', code: 'internal' })));
    await expect(client.getAuthSession()).rejects.toMatchObject({ status: 500, code: 'internal' });
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
    { name: 'updating', status: 503, code: 'updating', message: UPDATING_SENTENCE, notice: UPDATING_SENTENCE },
    { name: 'provider unavailable', status: 502, code: 'provider_unavailable', message: "Kroger isn't responding. Try again later.", notice: '' },
    { name: 'coded 503 internal', status: 503, code: 'internal', message: 'Something went wrong. Try again.', notice: '' },
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

  it('clears updating on the next success', async () => {
    const { client } = clientWith(jest.fn(async (input: RequestInfo | URL) => {
      if (String(input).includes(AUTH_SESSION_PATH)) return jsonResponse(503, { error: UPDATING_SENTENCE, code: 'updating' });
      return jsonResponse(200, []);
    }));
    await expect(client.getAuthSession()).rejects.toBeInstanceOf(ServerNoticeError);
    expect(serverNoticeText()).toBe(UPDATING_SENTENCE);
    await client.listPending();
    expect(serverNoticeText()).toBe('');
  });

  it('clears the banner after a session is read', async () => {
    raiseOfflineBanner();
    const { client } = clientWith(jest.fn(async () => jsonResponse(200, { required: false })));
    await client.getAuthSession();
    expect(offlineBannerActive()).toBe(false);
  });

  it('clears the auth banner on a later response and lets offline or busy replace it', async () => {
    const auth = async () => jsonResponse(503, { error: "Sign-in isn't set up yet.", code: authNotConfigured.code });
    const raised = clientWith(jest.fn(auth));
    await expect(raised.client.getAuthSession()).rejects.toBeInstanceOf(ServerNoticeError);
    expect(serverNoticeText()).toBe("Sign-in isn't set up yet.");
    expect(serverNoticeText().charCodeAt(serverNoticeText().indexOf("'"))).toBe(39);

    const signedOut = clientWith(jest.fn(async () => jsonResponse(401, { error: 'Sign in required.' })));
    await expect(signedOut.client.getAuthSession()).rejects.toMatchObject({ status: 401 });
    expect(serverNoticeText()).toBe('');

    const again = clientWith(jest.fn(auth));
    await expect(again.client.getAuthSession()).rejects.toBeInstanceOf(ServerNoticeError);
    const busy = clientWith(jest.fn(async () => jsonResponse(503, {
      error: 'Stockbot is busy right now. Try again in a moment.',
      code: 'busy',
    })));
    await expect(busy.client.listPending()).rejects.toMatchObject({ code: 'busy' });
    expect(serverNoticeText()).toBe('');

    const authAgain = clientWith(jest.fn(auth));
    await expect(authAgain.client.getAuthSession()).rejects.toBeInstanceOf(ServerNoticeError);
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
    await expect(html.client.getAuthSession()).rejects.toBeInstanceOf(UnreachableError);
    await expect(html.client.getAuthSession()).rejects.toMatchObject({ message: OFFLINE_SENTENCE });
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
      if (url.endsWith(scansQuery('user-1', 'flagged'))) {
        return jsonResponse(200, [{ id: 'scan-flagged' }]);
      }
      if (url.endsWith(scansQuery('user-1', 'qr'))) {
        return jsonResponse(200, [{ id: 'scan-qr' }]);
      }
      if (url.endsWith(scanCommitPath('scan-1'))) {
        const body = init?.body === undefined ? undefined : JSON.parse(String(init.body)) as { backfill?: boolean };
        if (body?.backfill === true) {
          return jsonResponse(200, {
            removed: 1,
            backfilled: 1,
            itemId: 'item-1',
            backfill: { stockInMoveId: 'move-in', stockOutMoveId: 'move-out' },
          });
        }
        return jsonResponse(200, { id: 'scan-1', status: 'committed' });
      }
      if (url.endsWith(BATCH_COMMIT_PATH)) return jsonResponse(200, { updatedCount: 2, committedIds: ['scan-1', 'scan-2'] });
      if (init?.method === 'POST' && url.endsWith('/api/scans')) {
        return jsonResponse(201, { id: 'scan-1', barcode: '111' });
      }
      if (init?.method === 'PATCH') return jsonResponse(200, { id: 'scan-1', status: 'cancelled' });
      return jsonResponse(500, { error: 'unexpected' });
    });
    const { client } = clientWith(fetchImpl);

    await client.createScan('111', 'stock_in');
    await expect(client.listPending()).resolves.toEqual([{ id: 'scan-1' }, { id: 'scan-flagged' }, { id: 'scan-qr' }]);
    await client.commitScan('scan-1');
    await client.commitBackfill('scan-1');
    await client.cancelScan('scan-1');
    await client.batchCommit(['scan-1', 'scan-2']);

    const post = calls.find((call) => call.init?.method === 'POST' && call.url.endsWith('/api/scans'));
    expect(post?.url).toBe(`${serverUrl}/api/scans`);
    expect(JSON.parse(String(post?.init?.body))).toEqual({ barcode: '111', direction: 'stock_in', userId: 'user-1' });
    expect(calls.some((call) => call.url === `${serverUrl}${scansQuery('user-1', 'pending')}`)).toBe(true);
    const commits = calls.filter((call) => call.url.endsWith(scanCommitPath('scan-1')));
    expect(commits).toHaveLength(2);
    const normalCommit = commits.find((call) => {
      const body = JSON.parse(String(call.init?.body)) as { backfill?: boolean };
      return body.backfill !== true;
    });
    const backfillCommit = commits.find((call) => {
      const body = JSON.parse(String(call.init?.body)) as { backfill?: boolean };
      return body.backfill === true;
    });
    expect(normalCommit?.init?.method).toBe('POST');
    expect(JSON.parse(String(normalCommit?.init?.body))).toEqual({});
    expect(JSON.parse(String(backfillCommit?.init?.body))).toEqual({ backfill: true });
    const cancel = calls.find((call) => call.init?.method === 'PATCH');
    expect(cancel?.url).toBe(`${serverUrl}/api/scans/scan-1`);
    expect(JSON.parse(String(cancel?.init?.body))).toEqual({ status: 'cancelled' });
    const batch = calls.find((call) => call.url.endsWith(BATCH_COMMIT_PATH));
    expect(JSON.parse(String(batch?.init?.body))).toEqual({ scanEntryIds: ['scan-1', 'scan-2'], commit: true });
    for (const call of calls) {
      expect(new Headers(call.init?.headers).get('Authorization')).toBe(`Bearer ${token}`);
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
    expect(new Headers(linkCall?.[1]?.headers).get('Authorization')).toBe(`Bearer ${token}`);
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

    await expect(client.getAuthSession()).rejects.toMatchObject({ status, message });
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
  it('sends the product calls the web resolver sends, to the same paths', async () => {
    const seen: { method: string; url: string; body: unknown }[] = [];
    const fetchImpl = jest.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      seen.push({
        method: init?.method ?? 'GET',
        url: String(input).replace(serverUrl, ''),
        body: init?.body === undefined ? undefined : JSON.parse(String(init.body)),
      });
      if (String(input).includes('/api/products/search')) return jsonResponse(200, [{ productId: 'p1' }]);
      if (String(input).includes('/api/products/lookup')) return jsonResponse(200, { product: null, source: '' });
      if (String(input).endsWith('/api/products')) return jsonResponse(201, { id: 'p1', name: 'Oats' });
      if (String(input).includes('/api/settings/contribution')) return jsonResponse(200, { enabled: true, configured: false });
      return jsonResponse(200, {});
    });
    const { client } = clientWith(fetchImpl);

    await client.lookupProduct('0123 45');
    await expect(client.searchProducts('oat', 20)).resolves.toEqual([{ productId: 'p1' }]);
    await client.createProduct({ name: 'Oats', category: '', unitOfMeasure: '', barcode: '012345' });
    await client.createProductOverride('012345', 'p1');
    await client.updateScan('scan-1', { productId: 'p1', status: 'pending' });
    await client.getContributionSettings();
    await client.updateContributionSettings(true);
    await client.listLinkSuggestions('scan-1');

    expect(seen).toEqual([
      { method: 'GET', url: '/api/products/lookup?barcode=0123+45', body: undefined },
      { method: 'GET', url: '/api/products/search?q=oat&limit=20', body: undefined },
      { method: 'POST', url: '/api/products', body: { name: 'Oats', category: '', unitOfMeasure: '', barcode: '012345' } },
      { method: 'POST', url: '/api/products/overrides', body: { barcode: '012345', productId: 'p1' } },
      { method: 'PATCH', url: '/api/scans/scan-1', body: { productId: 'p1', status: 'pending' } },
      { method: 'GET', url: '/api/settings/contribution', body: undefined },
      { method: 'PUT', url: '/api/settings/contribution', body: { enabled: true } },
      { method: 'GET', url: '/api/scans/scan-1/link-suggestions', body: undefined },
    ]);
  });

  it('sends the find item calls the web sheet sends, to the same paths', async () => {
    const seen: { method: string; url: string; body: unknown }[] = [];
    const fetchImpl = jest.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      seen.push({
        method: init?.method ?? 'GET',
        url: String(input).replace(serverUrl, ''),
        body: init?.body === undefined ? undefined : JSON.parse(String(init.body)),
      });
      if (String(input).endsWith('/item')) return jsonResponse(200, { itemId: 'item-1' });
      if (String(input).endsWith('/instances')) return jsonResponse(201, { id: 'inst-1', itemId: 'item-1' });
      if (String(input).includes('/instances/')) return new Response(null, { status: 204 });
      return jsonResponse(200, { stockOutMoveId: 'move-1' });
    });
    const { client } = clientWith(fetchImpl);

    await expect(client.ensureProductItem('prod-1')).resolves.toEqual({ itemId: 'item-1' });
    await expect(client.addItemInstance('item-1')).resolves.toMatchObject({ id: 'inst-1' });
    await expect(client.stockOutItem('item-1')).resolves.toEqual({ stockOutMoveId: 'move-1' });
    await client.stockOutItem('item-1', { backfill: true });
    await client.removeItemInstance('inst-1');

    expect(seen).toEqual([
      { method: 'POST', url: '/api/products/prod-1/item', body: {} },
      { method: 'POST', url: '/api/inventory/item-1/instances', body: {} },
      { method: 'POST', url: '/api/inventory/item-1/stock-out', body: {} },
      { method: 'POST', url: '/api/inventory/item-1/stock-out', body: { backfill: true } },
      { method: 'DELETE', url: '/api/inventory/instances/inst-1', body: undefined },
    ]);
  });

  it('lists the flagged and QR scans after the pending ones and stops at the first failure', async () => {
    const urls: string[] = [];
    const fetchImpl = jest.fn(async (input: RequestInfo | URL) => {
      urls.push(String(input).replace(serverUrl, ''));
      return jsonResponse(String(input).includes('status=pending') ? 401 : 200, { error: SESSION_EXPIRED_SENTENCE, code: 'unauthorized' });
    });
    const { client, onUnauthorized } = clientWith(fetchImpl);

    await expect(client.listPending()).rejects.toMatchObject({ status: 401 });

    expect(urls).toEqual(['/api/scans?userId=user-1&status=pending']);
    expect(onUnauthorized).toHaveBeenCalledTimes(1);
  });
});
