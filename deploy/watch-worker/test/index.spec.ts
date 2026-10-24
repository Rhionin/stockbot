import { describe, expect, it } from 'vitest';

import wranglerConfig from '../wrangler.jsonc?raw';

import worker, {
  FRESHNESS_MS,
  OVER_AGE_MS,
  REMINDER_MS,
  STATE_KEY,
  UPTIME_TIMEOUT_MS,
  backupDue,
  backupPrefixes,
  buildRawMime,
  checkBackups,
  checkUptime,
  deliverEmail,
  evaluateBackups,
  formatInstant,
  handleManual,
  handleScheduled,
  healthcheckTarget,
  r2Store,
  runWatch,
  type EmailMessageCtor,
  type ListedObject,
  type ObjectStore,
  type R2BucketLike,
  type WatchBindings,
} from '../src/index';

const NOW = Date.parse('2026-10-24T12:00:00.000Z');
const HOUR = 3600 * 1000;
const DAY = 24 * HOUR;

interface Stored {
  body: string;
  size: number;
  uploadedMs: number;
}

class MemoryStore implements ObjectStore {
  objects = new Map<string, Stored>();
  lists: string[] = [];

  putObject(key: string, uploadedMs: number, size: number, body = ''): void {
    this.objects.set(key, { body, size, uploadedMs });
  }

  async list(prefix: string): Promise<ListedObject[]> {
    this.lists.push(prefix);
    return [...this.objects.entries()]
      .filter(([key]) => key.startsWith(prefix))
      .map(([key, obj]) => ({ key, size: obj.size, uploadedMs: obj.uploadedMs }));
  }

  async getText(key: string): Promise<string | null> {
    return this.objects.get(key)?.body ?? null;
  }

  async putText(key: string, body: string): Promise<void> {
    const prev = this.objects.get(key);
    this.objects.set(key, { body, size: body.length, uploadedMs: prev?.uploadedMs ?? NOW });
  }
}

class Inbox {
  sent: unknown[] = [];

  async send(message: unknown): Promise<void> {
    this.sent.push(message);
  }

  raws(): string[] {
    return this.sent.map((message) => {
      if (typeof message === 'string') return message;
      if (message && typeof message === 'object' && 'raw' in message && typeof message.raw === 'string') {
        return message.raw;
      }
      return '';
    });
  }

  subjects(): string[] {
    return this.raws().map((raw) => /Subject: ([^\r\n]*)/.exec(raw)?.[1] ?? '');
  }
}

function manifest(size: number, integrity = 'ok', extra: Record<string, unknown> = {}): string {
  return JSON.stringify({ integrity, ciphertext_size: size, ...extra });
}

function seedSnapshot(
  store: MemoryStore,
  key: string,
  uploadedMs: number,
  size: number,
  body?: string | null,
): void {
  store.putObject(key, uploadedMs, size, '');
  if (body === null) return;
  const manifestKey = key.replace(/\.pantry\.db\.gz\.age$/, '.manifest.json');
  const text = body ?? manifest(size);
  store.putObject(manifestKey, uploadedMs, text.length, text);
}

function wipeBackups(store: MemoryStore): void {
  for (const key of [...store.objects.keys()]) {
    if (key !== STATE_KEY) store.objects.delete(key);
  }
}

function bindings(store: ObjectStore, inbox: Inbox, extra: Partial<WatchBindings> = {}): WatchBindings {
  return {
    TARGET_URL: 'https://stockbot.rhionin.com',
    ALERT_TO: 'rhionin@gmail.com',
    ALERT_FROM: 'pantry-pi-watch@rhionin.com',
    BACKUPS: store,
    send: (message) => inbox.send(message),
    ...extra,
  };
}

function jsonFetch(status: number, body: string): typeof fetch {
  return (async () => new Response(body, { status })) as typeof fetch;
}

function recordFetch(body: string, seen: { url?: string; init?: RequestInit }): typeof fetch {
  return (async (input, init) => {
    seen.url = String(input);
    seen.init = init;
    return new Response(body, { status: 200 });
  }) as typeof fetch;
}

describe('uptime', () => {
  it('accepts HTTP 200 with JSON', async () => {
    const seen: { url?: string; init?: RequestInit } = {};
    const result = await checkUptime(
      'https://stockbot.rhionin.com/',
      recordFetch('{"version":"test"}', seen),
    );
    expect(result).toEqual({ ok: true, reason: '' });
    expect(seen.url).toBe('https://stockbot.rhionin.com/api/build');
    expect(seen.init?.method).toBe('GET');
    expect(seen.init?.redirect).toBe('manual');
    expect(seen.init?.signal).toBeInstanceOf(AbortSignal);
    expect(UPTIME_TIMEOUT_MS).toBe(10_000);
  });

  it('rejects a non-200 response without copying the body', async () => {
    const result = await checkUptime(
      'https://stockbot.rhionin.com',
      jsonFetch(503, 'household barcode 012345678905'),
    );
    expect(result).toEqual({ ok: false, reason: 'HTTP 503' });
  });

  it('rejects a redirect instead of following it', async () => {
    const result = await checkUptime('https://stockbot.rhionin.com', jsonFetch(302, '<html>login</html>'));
    expect(result).toEqual({ ok: false, reason: 'HTTP 302' });
  });

  it('rejects invalid JSON', async () => {
    const result = await checkUptime('https://stockbot.rhionin.com', jsonFetch(200, 'not-json'));
    expect(result).toEqual({ ok: false, reason: 'response was not valid JSON' });
  });

  it('reports a timeout when the build request does not finish', async () => {
    const fetchImpl = (async (_input: RequestInfo | URL, init?: RequestInit) => {
      await new Promise((_resolve, reject) => {
        const signal = init?.signal;
        if (!signal) {
          reject(new Error('missing signal'));
          return;
        }
        signal.addEventListener('abort', () => reject(signal.reason), { once: true });
      });
      return new Response('');
    }) as typeof fetch;
    const result = await checkUptime('https://stockbot.rhionin.com', fetchImpl, 30);
    expect(result).toEqual({ ok: false, reason: 'timed out after 10 seconds' });
  });

  it('reports a failed request without the runtime message', async () => {
    const fetchImpl = (async () => {
      throw new TypeError('network down at household barcode');
    }) as typeof fetch;
    const result = await checkUptime('https://stockbot.rhionin.com', fetchImpl);
    expect(result).toEqual({ ok: false, reason: 'request failed (TypeError)' });
  });

  it('rejects a target that is not a URL', async () => {
    const result = await checkUptime('not a url', jsonFetch(200, '{}'));
    expect(result).toEqual({ ok: false, reason: 'TARGET_URL is not a valid URL' });
  });
});

describe('backup freshness, manifest, size, and retention', () => {
  const key = 'nightly/2026-10-24T120000Z.pantry.db.gz.age';

  async function atAge(ageMs: number) {
    const store = new MemoryStore();
    seedSnapshot(store, key, NOW - ageMs, 80);
    return checkBackups(store, NOW);
  }

  it('keeps a snapshot 25h59m59s old', async () => {
    const age = 25 * HOUR + 59 * 60 * 1000 + 59 * 1000;
    expect(await atAge(age)).toEqual({ ok: true, reason: '' });
  });

  it('keeps a snapshot exactly 26 hours old', async () => {
    expect(FRESHNESS_MS).toBe(26 * 3600 * 1000);
    expect(await atAge(FRESHNESS_MS)).toEqual({ ok: true, reason: '' });
  });

  it('flags a snapshot 26h00m01s old', async () => {
    const result = await atAge(FRESHNESS_MS + 1000);
    expect(result.ok).toBe(false);
    expect(result.reason).toContain(`${key} is 26h00m01s old, past 26 hours`);
  });

  it('uses a manual snapshot when nightly has none', async () => {
    const store = new MemoryStore();
    seedSnapshot(store, 'manual/2026-10-24T110000Z.manual.pantry.db.gz.age', NOW - HOUR, 40);
    expect(await checkBackups(store, NOW)).toEqual({ ok: true, reason: '' });
  });

  it('uses the newest snapshot across nightly and manual', async () => {
    const store = new MemoryStore();
    seedSnapshot(store, 'nightly/old.pantry.db.gz.age', NOW - 30 * HOUR, 500);
    seedSnapshot(store, 'manual/2026-10-24T110000Z.pre-deploy.pantry.db.gz.age', NOW - HOUR, 80);
    expect(await checkBackups(store, NOW)).toEqual({ ok: true, reason: '' });
  });

  it('fails when the only snapshot is older than 26 hours', async () => {
    const store = new MemoryStore();
    seedSnapshot(store, 'nightly/old.pantry.db.gz.age', NOW - 30 * HOUR, 100);
    const result = await checkBackups(store, NOW);
    expect(result.ok).toBe(false);
    expect(result.reason).toContain('past 26 hours');
  });

  it('flags a missing manifest', async () => {
    const store = new MemoryStore();
    seedSnapshot(store, key, NOW - HOUR, 80, null);
    const result = await checkBackups(store, NOW);
    expect(result.ok).toBe(false);
    expect(result.reason).toContain(`missing manifest for ${key}`);
  });

  it('flags a manifest that is not JSON', async () => {
    const store = new MemoryStore();
    seedSnapshot(store, key, NOW - HOUR, 80, '{');
    const result = await checkBackups(store, NOW);
    expect(result.reason).toContain('is not valid JSON');
  });

  it('flags integrity other than ok without copying manifest contents', async () => {
    const store = new MemoryStore();
    const leaked = 'household-milk-barcode-012345678905';
    seedSnapshot(store, key, NOW - HOUR, 80, manifest(80, 'failed', { tables: { products: 4 }, note: leaked }));
    const result = await checkBackups(store, NOW);
    expect(result.ok).toBe(false);
    expect(result.reason).toContain('manifest integrity is "failed"');
    expect(result.reason).not.toContain(leaked);
    expect(result.reason).not.toContain('products');
  });

  it('flags a ciphertext size that does not match the object', async () => {
    const store = new MemoryStore();
    const leaked = 'household-milk-barcode-012345678905';
    seedSnapshot(store, key, NOW - HOUR, 80, manifest(10, 'ok', { tables: { products: 4 }, note: leaked }));
    const result = await checkBackups(store, NOW);
    expect(result.reason).toContain('manifest ciphertext size 10 does not match object size 80');
    expect(result.reason).not.toContain(leaked);
    expect(result.reason).not.toContain('products');
  });

  it('flags a newest snapshot under half the previous size in the same prefix', async () => {
    const store = new MemoryStore();
    seedSnapshot(store, 'nightly/previous.pantry.db.gz.age', NOW - 2 * HOUR, 200);
    seedSnapshot(store, key, NOW - HOUR, 99);
    const result = await checkBackups(store, NOW);
    expect(result.ok).toBe(false);
    expect(result.reason).toContain(`${key} is 99 bytes, under half of previous nightly/previous.pantry.db.gz.age (200 bytes)`);
  });

  it('keeps a newest snapshot that is exactly half the previous size', async () => {
    const store = new MemoryStore();
    seedSnapshot(store, 'nightly/previous.pantry.db.gz.age', NOW - 2 * HOUR, 200);
    seedSnapshot(store, key, NOW - HOUR, 100);
    expect(await checkBackups(store, NOW)).toEqual({ ok: true, reason: '' });
  });

  it('does not compare size with a snapshot in the other prefix', async () => {
    const store = new MemoryStore();
    seedSnapshot(store, 'manual/big.manual.pantry.db.gz.age', NOW - 2 * HOUR, 1000);
    seedSnapshot(store, key, NOW - HOUR, 40);
    expect(await checkBackups(store, NOW)).toEqual({ ok: true, reason: '' });
  });

  it('keeps an object exactly 15 days old and one a millisecond younger', async () => {
    expect(OVER_AGE_MS).toBe(15 * 24 * 3600 * 1000);
    for (const age of [OVER_AGE_MS, OVER_AGE_MS - 1]) {
      const store = new MemoryStore();
      seedSnapshot(store, key, NOW - HOUR, 80);
      store.putObject('manual/edge.txt', NOW - age, 3, 'edge');
      store.putObject(STATE_KEY, NOW - 20 * DAY, 12, '{}');
      expect(await checkBackups(store, NOW)).toEqual({ ok: true, reason: '' });
    }
  });

  it('flags an object one millisecond older than 15 days', async () => {
    const store = new MemoryStore();
    seedSnapshot(store, key, NOW - HOUR, 80);
    store.putObject('manual/too-old.manifest.json', NOW - OVER_AGE_MS - 1, 4, '{}');
    const result = await checkBackups(store, NOW);
    expect(result.ok).toBe(false);
    expect(result.reason).toContain('object manual/too-old.manifest.json is older than 15 days');
  });

  it('ignores _watch/state.json and objects outside nightly and manual', async () => {
    const store = new MemoryStore();
    seedSnapshot(store, key, NOW - HOUR, 80);
    store.putObject(STATE_KEY, NOW - 20 * DAY, 12, '{}');
    store.putObject('secrets/old.tar.age', NOW - 20 * DAY, 30, 'cipher');
    const result = await checkBackups(store, NOW);
    expect(result).toEqual({ ok: true, reason: '' });
    expect(store.lists).toEqual(['nightly/', 'manual/']);

    const evaluated = evaluateBackups(
      [
        { key, size: 80, uploadedMs: NOW - HOUR },
        { key: STATE_KEY, size: 12, uploadedMs: NOW - 20 * DAY },
        { key: 'secrets/old.tar.age', size: 30, uploadedMs: NOW - 20 * DAY },
      ],
      new Map([['nightly/2026-10-24T120000Z.manifest.json', manifest(80)]]),
      NOW,
      ['nightly/', 'manual/', '_watch/'],
    );
    expect(evaluated).toEqual({ ok: true, reason: '' });
  });

  it('fails when the bucket has no snapshot', async () => {
    const result = await checkBackups(new MemoryStore(), NOW);
    expect(result).toEqual({ ok: false, reason: 'no backup snapshot in nightly/ or manual/' });
  });

  it('reports a list failure without an internal error string', async () => {
    const store: ObjectStore = {
      async list() {
        throw new Error('R2 account secret leaked');
      },
      async getText() {
        return null;
      },
      async putText() {},
    };
    const result = await checkBackups(store, NOW);
    expect(result).toEqual({ ok: false, reason: 'could not list backup objects' });
  });

  it('pages through an R2 listing', async () => {
    const bucket: R2BucketLike = {
      async list(options) {
        if (!options.cursor) {
          return {
            objects: [{ key, size: 80, uploaded: new Date(NOW - HOUR) }],
            truncated: true,
            cursor: 'next',
          };
        }
        expect(options).toEqual({ prefix: 'nightly/', cursor: 'next' });
        return {
          objects: [{ key: 'nightly/2026-10-24T120000Z.manifest.json', size: 8, uploaded: new Date(NOW - HOUR) }],
          truncated: false,
        };
      },
      async get(objectKey) {
        expect(objectKey).toBe('nightly/2026-10-24T120000Z.manifest.json');
        return { text: async () => manifest(80) };
      },
      async put() {
        return null;
      },
    };
    expect(await checkBackups(r2Store(bucket), NOW, ['nightly/'])).toEqual({ ok: true, reason: '' });
  });
});

describe('alert dedup', () => {
  it('emails uptime once, stays quiet through the cooldown, reminds, then resolves', async () => {
    const store = new MemoryStore();
    const inbox = new Inbox();
    const env = bindings(store, inbox);
    const fail = jsonFetch(503, 'household barcode 012345678905');

    await runWatch(env, { nowMs: NOW, runBackup: false, fetchImpl: fail });
    await runWatch(env, { nowMs: NOW + REMINDER_MS - 1, runBackup: false, fetchImpl: fail });
    expect(inbox.subjects()).toEqual(['[ALERT] pantry-pi-watch uptime']);

    const during = JSON.parse(store.objects.get(STATE_KEY)?.body ?? '{}') as {
      uptime: { status: string; failingSince: string; lastAlertAt: string };
    };
    expect(during.uptime.status).toBe('failing');
    expect(during.uptime.failingSince).toBe(new Date(NOW).toISOString());
    expect(during.uptime.lastAlertAt).toBe(new Date(NOW).toISOString());

    await runWatch(env, { nowMs: NOW + REMINDER_MS, runBackup: false, fetchImpl: fail });
    expect(inbox.subjects()).toEqual([
      '[ALERT] pantry-pi-watch uptime',
      '[ALERT] pantry-pi-watch uptime reminder',
    ]);
    const reminded = JSON.parse(store.objects.get(STATE_KEY)?.body ?? '{}') as {
      uptime: { failingSince: string; lastAlertAt: string };
    };
    expect(reminded.uptime.failingSince).toBe(new Date(NOW).toISOString());
    expect(reminded.uptime.lastAlertAt).toBe(new Date(NOW + REMINDER_MS).toISOString());

    await runWatch(env, { nowMs: NOW + REMINDER_MS + 60_000, runBackup: false, fetchImpl: jsonFetch(200, '{}') });
    await runWatch(env, { nowMs: NOW + REMINDER_MS + 120_000, runBackup: false, fetchImpl: jsonFetch(200, '{}') });
    expect(inbox.subjects()).toEqual([
      '[ALERT] pantry-pi-watch uptime',
      '[ALERT] pantry-pi-watch uptime reminder',
      '[RESOLVED] pantry-pi-watch uptime',
    ]);

    const alert = inbox.raws()[0] ?? '';
    expect(alert).toContain('To: rhionin@gmail.com');
    expect(alert).toContain('From: pantry-pi-watch@rhionin.com');
    expect(alert).toContain(`at: ${formatInstant(NOW)}`);
    expect(alert).toContain('reason: HTTP 503');
    expect(alert).toContain('target: https://stockbot.rhionin.com/api/build');
    expect(alert).not.toContain('012345678905');
    expect(alert).not.toContain('barcode');
    expect(inbox.raws()[1]).toContain(`failing since: ${formatInstant(NOW)}`);
    expect(inbox.raws()[2]).toContain('status: ok');
    expect(inbox.raws()[2]).toContain(`at: ${formatInstant(NOW + REMINDER_MS + 60_000)}`);
  });

  it('emails a backup failure once, stays quiet, and resolves when the snapshot is fresh', async () => {
    const store = new MemoryStore();
    const inbox = new Inbox();
    const env = bindings(store, inbox);
    const stale = 'nightly/stale.pantry.db.gz.age';
    seedSnapshot(store, stale, NOW - FRESHNESS_MS - 1000, 80);

    await runWatch(env, { nowMs: NOW, runBackup: true, fetchImpl: jsonFetch(200, '{}') });
    await runWatch(env, { nowMs: NOW + HOUR, runBackup: true, fetchImpl: jsonFetch(200, '{}') });
    expect(inbox.subjects()).toEqual(['[ALERT] pantry-pi-watch backup']);

    wipeBackups(store);
    seedSnapshot(store, 'nightly/fresh.pantry.db.gz.age', NOW + 2 * HOUR - HOUR, 80);
    await runWatch(env, { nowMs: NOW + 2 * HOUR, runBackup: true, fetchImpl: jsonFetch(200, '{}') });
    await runWatch(env, { nowMs: NOW + 3 * HOUR, runBackup: true, fetchImpl: jsonFetch(200, '{}') });
    expect(inbox.subjects()).toEqual(['[ALERT] pantry-pi-watch backup', '[RESOLVED] pantry-pi-watch backup']);
    expect(inbox.raws()[0]).not.toContain('ciphertext_sha256');
  });

  it('does not let an uptime cooldown suppress a new backup alert', async () => {
    const store = new MemoryStore();
    const inbox = new Inbox();
    const env = bindings(store, inbox);
    await runWatch(env, { nowMs: NOW, runBackup: false, fetchImpl: jsonFetch(500, '') });
    await runWatch(env, { nowMs: NOW + 5 * 60 * 1000, runBackup: true, fetchImpl: jsonFetch(500, '') });
    expect(inbox.subjects()).toEqual(['[ALERT] pantry-pi-watch uptime', '[ALERT] pantry-pi-watch backup']);
  });

  it('treats a corrupt state file as healthy so the next failure still alerts', async () => {
    const store = new MemoryStore();
    const inbox = new Inbox();
    await store.putText(STATE_KEY, 'not-json');
    await runWatch(bindings(store, inbox), { nowMs: NOW, runBackup: false, fetchImpl: jsonFetch(500, '') });
    expect(inbox.subjects()).toEqual(['[ALERT] pantry-pi-watch uptime']);
  });

  it('skips the backup check off the hour and runs it for a drill prefix', async () => {
    const store = new MemoryStore();
    const inbox = new Inbox();
    seedSnapshot(store, 'nightly/fresh.pantry.db.gz.age', NOW - HOUR, 80);
    await runWatch(bindings(store, inbox), {
      nowMs: Date.parse('2026-10-24T12:05:00.000Z'),
      runBackup: false,
      fetchImpl: jsonFetch(200, '{}'),
    });
    expect(inbox.subjects()).toEqual([]);

    const drill = new Inbox();
    const report = await runWatch(bindings(store, drill, { BACKUP_DRILL_PREFIX: 'drill-empty' }), {
      nowMs: NOW,
      runBackup: true,
      fetchImpl: jsonFetch(200, '{}'),
    });
    expect(report.backup).toMatchObject({ ok: false, email: 'alert' });
    expect(drill.subjects()).toEqual(['[ALERT] pantry-pi-watch backup']);
    expect(drill.raws()[0]).toContain('no backup snapshot in drill-empty/');
  });

  it('pings optional healthchecks URLs and stays quiet when they are unset', async () => {
    const pings: string[] = [];
    const pingImpl = (async (input: RequestInfo | URL) => {
      pings.push(String(input));
      return new Response('', { status: 200 });
    }) as typeof fetch;
    const store = new MemoryStore();
    seedSnapshot(store, 'nightly/fresh.pantry.db.gz.age', NOW - HOUR, 80);
    await runWatch(bindings(store, new Inbox()), {
      nowMs: Date.parse('2026-10-24T12:05:00.000Z'),
      runBackup: false,
      fetchImpl: jsonFetch(200, '{}'),
      pingImpl,
    });
    expect(pings).toEqual([]);

    const failing: string[] = [];
    const failPing = (async (input: RequestInfo | URL) => {
      failing.push(String(input));
      return new Response('', { status: 599 });
    }) as typeof fetch;
    const errors: string[] = [];
    const original = console.error;
    console.error = (...args: unknown[]) => {
      errors.push(args.map((arg) => String(arg)).join(' '));
    };
    try {
      await runWatch(bindings(new MemoryStore(), new Inbox(), {
        HC_UPTIME_URL: 'https://hc-ping.example/uptime/',
        HC_BACKUP_URL: 'https://hc-ping.example/backup',
      }), {
        nowMs: Date.parse('2026-10-24T13:00:00.000Z'),
        runBackup: true,
        fetchImpl: jsonFetch(200, '{}'),
        pingImpl: failPing,
      });
    } finally {
      console.error = original;
    }
    expect(failing).toEqual([
      'https://hc-ping.example/uptime',
      'https://hc-ping.example/backup/fail',
    ]);
    expect(errors.join('\n')).not.toContain('hc-ping.example');
  });
});

describe('email adapter', () => {
  it('sends an EmailMessage when the runtime provides the constructor', async () => {
    class MimeMessage {
      constructor(
        readonly from: string,
        readonly to: string,
        readonly raw: string,
      ) {}
    }
    const inbox = new Inbox();
    await deliverEmail((message) => inbox.send(message), {
      from: 'pantry-pi-watch@rhionin.com',
      to: 'rhionin@gmail.com',
      subject: 'Hello\r\nBcc: evil@example.com',
      text: 'status: ok',
    }, MimeMessage as EmailMessageCtor);
    const message = inbox.sent[0] as MimeMessage;
    expect(message).toBeInstanceOf(MimeMessage);
    expect(message.from).toBe('pantry-pi-watch@rhionin.com');
    expect(message.to).toBe('rhionin@gmail.com');
    expect(message.raw).toContain('Subject: Hello Bcc: evil@example.com\r\n');
    expect(message.raw).not.toMatch(/^Bcc:/m);
    expect(message.raw).toContain('\r\n\r\nstatus: ok');
  });

  it('sends the same MIME string when EmailMessage is unavailable', () => {
    const raw = buildRawMime({
      from: 'pantry-pi-watch@rhionin.com',
      to: 'rhionin@gmail.com',
      subject: '[ALERT] pantry-pi-watch uptime',
      text: 'check: uptime',
    });
    expect(raw.startsWith('From: pantry-pi-watch@rhionin.com\r\n')).toBe(true);
    expect(raw).toContain('Subject: [ALERT] pantry-pi-watch uptime\r\n');
    expect(raw).toContain('\r\n\r\ncheck: uptime');
  });
});

describe('schedule and manual trigger', () => {
  function envFor(store: MemoryStore, inbox: Inbox): Parameters<typeof handleScheduled>[1] {
    return {
      TARGET_URL: 'https://stockbot.rhionin.com',
      ALERT_TO: 'rhionin@gmail.com',
      ALERT_FROM: 'pantry-pi-watch@rhionin.com',
      BACKUPS: {
        async list(options: { prefix?: string; cursor?: string }) {
          const prefix = options.prefix ?? '';
          return {
            objects: [...store.objects.entries()]
              .filter(([key]) => key.startsWith(prefix))
              .map(([key, obj]) => ({ key, size: obj.size, uploaded: new Date(obj.uploadedMs) })),
            truncated: false,
          };
        },
        async get(key: string) {
          const obj = store.objects.get(key);
          if (!obj) return null;
          return { text: async () => obj.body };
        },
        async put(key: string, value: string) {
          store.putObject(key, store.objects.get(key)?.uploadedMs ?? NOW, value.length, value);
        },
      },
      EMAIL: { send: (message: unknown) => inbox.send(message) },
    } as unknown as Parameters<typeof handleScheduled>[1];
  }

  async function withFetch<T>(fetchImpl: typeof fetch, body: () => Promise<T>): Promise<T> {
    const previous = globalThis.fetch;
    globalThis.fetch = fetchImpl;
    try {
      return await body();
    } finally {
      globalThis.fetch = previous;
    }
  }

  it('runs the backup check on the hour and skips it at minute 5', async () => {
    expect(backupDue(Date.parse('2026-10-24T12:00:30.000Z'))).toBe(true);
    expect(backupDue(Date.parse('2026-10-24T12:05:00.000Z'))).toBe(false);
    expect(backupPrefixes(undefined)).toEqual(['nightly/', 'manual/']);
    expect(backupPrefixes(' drill-empty ')).toEqual(['drill-empty/']);

    const skipped = new MemoryStore();
    const skippedInbox = new Inbox();
    const skippedReport = await withFetch(jsonFetch(500, ''), () =>
      handleScheduled(Date.parse('2026-10-24T12:05:00.000Z'), envFor(skipped, skippedInbox)),
    );
    expect(skippedReport.backup).toEqual({ skipped: true });
    expect(skippedInbox.subjects()).toEqual(['[ALERT] pantry-pi-watch uptime']);

    const hourly = new MemoryStore();
    const hourlyInbox = new Inbox();
    await withFetch(jsonFetch(500, ''), () =>
      worker.scheduled?.(
        { scheduledTime: Date.parse('2026-10-24T13:00:00.000Z'), cron: '*/5 * * * *', noRetry() {} },
        envFor(hourly, hourlyInbox),
        {} as ExecutionContext,
      ),
    );
    expect(hourlyInbox.subjects()).toEqual([
      '[ALERT] pantry-pi-watch uptime',
      '[ALERT] pantry-pi-watch backup',
    ]);
  });

  it('runs both checks for /run and ignores other paths', async () => {
    const store = new MemoryStore();
    seedSnapshot(store, 'nightly/fresh.pantry.db.gz.age', Date.now() - HOUR, 80);
    const inbox = new Inbox();
    const env = envFor(store, inbox);
    const missed = await withFetch(
      (async () => {
        throw new Error('should not fetch');
      }) as typeof fetch,
      () => worker.fetch?.(new Request('https://pantry-pi-watch.example/'), env, {} as ExecutionContext),
    );
    expect(missed?.status).toBe(404);
    expect(inbox.sent).toEqual([]);

    const response = await withFetch(jsonFetch(200, '{}'), () =>
      worker.fetch?.(new Request('https://pantry-pi-watch.example/run', { method: 'POST' }), env, {} as ExecutionContext),
    );
    expect(response?.status).toBe(200);
    expect(response?.headers.get('cache-control')).toBe('no-store');
    const body = await response?.json() as { uptime: { ok: boolean; email: null }; backup: { ok: boolean } };
    expect(body.uptime.ok).toBe(true);
    expect(body.backup.ok).toBe(true);
    expect(inbox.sent).toEqual([]);

    const manual = await handleManual(envFor(new MemoryStore(), inbox), NOW);
    expect(manual.backup).toMatchObject({ ok: false, email: 'alert' });
  });
});

describe('Denver timestamps', () => {
  it('formats MDT and MST next to UTC', () => {
    expect(formatInstant(Date.parse('2026-10-24T18:00:00.000Z'))).toBe(
      '2026-10-24 12:00:00 MDT (2026-10-24T18:00:00Z)',
    );
    expect(formatInstant(Date.parse('2026-12-24T19:00:00.000Z'))).toBe(
      '2026-12-24 12:00:00 MST (2026-12-24T19:00:00Z)',
    );
  });
});

describe('healthcheck URL', () => {
  it('appends /fail only for a failing check', () => {
    expect(healthcheckTarget(undefined, true)).toBeNull();
    expect(healthcheckTarget('  ', false)).toBeNull();
    expect(healthcheckTarget('https://hc-ping.example/uuid/', true)).toBe('https://hc-ping.example/uuid');
    expect(healthcheckTarget('https://hc-ping.example/uuid', false)).toBe('https://hc-ping.example/uuid/fail');
  });
});

describe('wrangler config', () => {
  it('binds the cron, the backup bucket, and the destination address', () => {
    const stripped = wranglerConfig.replace(/\/\*[\s\S]*?\*\//g, '').replace(/^\s*\/\/.*$/gm, '');
    const config = JSON.parse(stripped) as {
      name: string;
      main: string;
      workers_dev: boolean;
      compatibility_date: string;
      triggers: { crons: string[] };
      r2_buckets: Array<{ binding: string; bucket_name: string }>;
      send_email: Array<{ name: string; destination_address: string }>;
      vars: Record<string, string>;
      observability: { enabled: boolean; logs: { enabled: boolean }; traces: { enabled: boolean } };
    };
    expect(config.name).toBe('pantry-pi-watch');
    expect(config.main).toBe('src/index.ts');
    expect(config.workers_dev).toBe(false);
    expect(config.compatibility_date).toBe('2026-10-24');
    expect(config.triggers.crons).toEqual(['*/5 * * * *']);
    expect(config.r2_buckets).toEqual([{ binding: 'BACKUPS', bucket_name: 'pantry-pi-backups' }]);
    expect(config.send_email).toEqual([{ name: 'EMAIL', destination_address: 'rhionin@gmail.com' }]);
    expect(config.vars).toEqual({
      TARGET_URL: 'https://stockbot.rhionin.com',
      ALERT_TO: 'rhionin@gmail.com',
      ALERT_FROM: 'pantry-pi-watch@rhionin.com',
    });
    expect(config.observability.enabled).toBe(true);
    expect(config.observability.logs.enabled).toBe(true);
    expect(config.observability.traces.enabled).toBe(true);
    expect(config.vars.HC_UPTIME_URL).toBeUndefined();
    expect(config.vars.HC_BACKUP_URL).toBeUndefined();
  });
});
