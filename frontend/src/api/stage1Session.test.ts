import { afterEach, describe, expect, it, vi } from 'vitest';
import { OFFLINE_SENTENCE, UPDATING_SENTENCE, applyHttpResponse, serverNoticeText } from '@stockbot/shared/contract';
import { getSession, listScanEntries, resetStage1Client, stage1AuthActive } from './client';

const json = (body: unknown, status = 200) =>
  new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } });

const account = {
  user: { id: 'user-1', email: 'ada@example.com', name: 'Ada' },
  kind: 'account',
  memberships: [{ householdId: 'hh-1', name: 'Pantry', role: 'owner', deletedAt: null }],
  households: [{ id: 'hh-1', name: 'Pantry', role: 'owner', deletedAt: null }],
  activeHouseholdId: 'hh-1',
  operator: false,
};

describe('stage-1 session compatibility', () => {
  afterEach(() => {
    resetStage1Client();
    vi.unstubAllGlobals();
  });

  it('reads only the stage-1 session route', async () => {
    const urls: string[] = [];
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL) => {
      const url = String(input);
      urls.push(url);
      if (url === '/api/auth/session') return Promise.resolve(json({ ...account, kind: 'signed_out', user: null, activeHouseholdId: null }));
      return Promise.reject(new Error(url));
    }));
    const session = await getSession();
    expect(session).toEqual({ required: true, username: '', error: '' });
    expect(stage1AuthActive()).toBe(true);
    expect(urls).toEqual(['/api/auth/session']);
  });

  it('does not fall back when the stage-1 route cannot be reached', async () => {
    let calls = 0;
    vi.stubGlobal('fetch', vi.fn(() => {
      calls += 1;
      return Promise.reject(new TypeError('Failed to fetch'));
    }));
    await expect(getSession()).rejects.toThrow(OFFLINE_SENTENCE);
    expect(calls).toBe(1);
  });

  it('strips userId and sends the household only after a stage-1 session', async () => {
    const seen: { url: string; household: string | null }[] = [];
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      const headers = new Headers(init?.headers);
      seen.push({ url, household: headers.get('X-Household-Id') });
      if (url === '/api/auth/session') return Promise.resolve(json(account));
      if (url.startsWith('/api/scans')) return Promise.resolve(json([]));
      return Promise.reject(new Error(url));
    }));

    await listScanEntries('user-1', 'pending');
    expect(seen[0]?.url).toBe('/api/scans?userId=user-1&status=pending');
    expect(seen[0]?.household).toBeNull();

    await getSession();
    expect(stage1AuthActive()).toBe(true);
    await listScanEntries('user-1', 'pending');
    expect(seen.at(-1)?.url).toBe('/api/scans?status=pending');
    expect(seen.at(-1)?.household).toBe('hh-1');
  });

  it('keeps the unreachable and updating banner sentences', () => {
    expect(applyHttpResponse(502, '')).toBe('offline');
    expect(serverNoticeText()).toBe("Can't reach Stockbot. Check your connection and try again.");
    expect(applyHttpResponse(503, 'updating')).toBe('updating');
    expect(serverNoticeText()).toBe(UPDATING_SENTENCE);
    expect(UPDATING_SENTENCE).toBe('Updating. Try again in a minute.');
  });
});
