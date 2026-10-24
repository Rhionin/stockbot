// Outside checks for the Pi. Uptime runs every cron. Backup freshness runs
// on the hour. Alert dedup lives in R2 because the Worker has no other disk.

export const FRESHNESS_MS = 26 * 3600 * 1000;
export const OVER_AGE_MS = 15 * 24 * 3600 * 1000;
export const REMINDER_MS = 6 * 3600 * 1000;
export const UPTIME_TIMEOUT_MS = 10_000;
export const STATE_KEY = '_watch/state.json';

const SNAPSHOT_SUFFIX = '.pantry.db.gz.age';
const DEFAULT_PREFIXES = ['nightly/', 'manual/'] as const;

export interface ListedObject {
  key: string;
  size: number;
  uploadedMs: number;
}

export interface ObjectStore {
  list(prefix: string): Promise<ListedObject[]>;
  getText(key: string): Promise<string | null>;
  putText(key: string, body: string): Promise<void>;
}

export interface CheckResult {
  ok: boolean;
  reason: string;
}

export interface CheckState {
  status: 'ok' | 'failing';
  failingSince: string | null;
  lastAlertAt: string | null;
  reason: string;
}

export interface WatchState {
  version: 1;
  updatedAt: string | null;
  uptime: CheckState;
  backup: CheckState;
}

export type AlertKind = 'alert' | 'reminder' | 'resolved';

export interface Transition {
  next: CheckState;
  email: AlertKind | null;
}

export interface OutboundEmail {
  from: string;
  to: string;
  subject: string;
  text: string;
  sentAtMs?: number;
}

export interface EmailMessageLike {
  readonly from: string;
  readonly to: string;
}

export type EmailMessageCtor = new (from: string, to: string, raw: string) => EmailMessageLike;

export interface R2ListPage {
  objects: ReadonlyArray<{ key: string; size: number; uploaded: Date }>;
  truncated: boolean;
  cursor?: string;
}

export interface R2BucketLike {
  list(options: { prefix?: string; cursor?: string }): Promise<R2ListPage>;
  get(key: string): Promise<{ text(): Promise<string> } | null>;
  put(key: string, value: string, options?: { httpMetadata?: { contentType?: string } }): Promise<unknown>;
}

export interface WatchBindings {
  TARGET_URL: string;
  ALERT_TO: string;
  ALERT_FROM: string;
  BACKUPS: ObjectStore;
  send(message: unknown): Promise<unknown>;
  HC_UPTIME_URL?: string;
  HC_BACKUP_URL?: string;
  BACKUP_DRILL_PREFIX?: string;
}

export interface RunOptions {
  nowMs: number;
  runBackup: boolean;
  fetchImpl?: typeof fetch;
  pingImpl?: typeof fetch;
  emailCtor?: EmailMessageCtor | null;
}

export interface CheckReport {
  ok: boolean;
  reason: string;
  email: AlertKind | null;
  emailError?: string;
}

export interface RunReport {
  uptime: CheckReport;
  backup: CheckReport | { skipped: true };
}

export function backupDue(scheduledTimeMs: number): boolean {
  return new Date(scheduledTimeMs).getUTCMinutes() === 0;
}

export function backupPrefixes(drillPrefix: string | undefined): string[] {
  const trimmed = drillPrefix?.trim() ?? '';
  if (!trimmed) return [...DEFAULT_PREFIXES];
  return [trimmed.endsWith('/') ? trimmed : `${trimmed}/`];
}

export function buildUrl(targetUrl: string): string {
  const base = targetUrl.endsWith('/') ? targetUrl : `${targetUrl}/`;
  return new URL('api/build', base).toString();
}

export function formatInstant(ms: number): string {
  const date = new Date(ms);
  const utc = date.toISOString().replace(/\.\d{3}Z$/, 'Z');
  const parts = Object.fromEntries(
    denver.formatToParts(date)
      .filter((part) => part.type !== 'literal')
      .map((part) => [part.type, part.value]),
  );
  const hour = parts.hour === '24' ? '00' : parts.hour;
  const local = `${parts.year}-${parts.month}-${parts.day} ${hour}:${parts.minute}:${parts.second} ${parts.timeZoneName}`;
  return `${local} (${utc})`;
}

export function formatDuration(ms: number): string {
  const sign = ms < 0 ? '-' : '';
  let rest = Math.abs(Math.trunc(ms));
  const hours = Math.floor(rest / 3_600_000);
  rest -= hours * 3_600_000;
  const minutes = Math.floor(rest / 60_000);
  rest -= minutes * 60_000;
  const seconds = Math.floor(rest / 1000);
  return `${sign}${hours}h${String(minutes).padStart(2, '0')}m${String(seconds).padStart(2, '0')}s`;
}

const denver = new Intl.DateTimeFormat('en-US', {
  timeZone: 'America/Denver',
  year: 'numeric',
  month: '2-digit',
  day: '2-digit',
  hour: '2-digit',
  minute: '2-digit',
  second: '2-digit',
  hourCycle: 'h23',
  timeZoneName: 'short',
});

export function emptyCheckState(): CheckState {
  return { status: 'ok', failingSince: null, lastAlertAt: null, reason: '' };
}

export function emptyWatchState(): WatchState {
  return { version: 1, updatedAt: null, uptime: emptyCheckState(), backup: emptyCheckState() };
}

export function transition(prev: CheckState | undefined, result: CheckResult, nowMs: number): Transition {
  const prior = prev ?? emptyCheckState();
  const nowIso = new Date(nowMs).toISOString();
  if (result.ok) {
    if (prior.status === 'failing') {
      return {
        email: 'resolved',
        next: { status: 'ok', failingSince: null, lastAlertAt: nowIso, reason: '' },
      };
    }
    return {
      email: null,
      next: { status: 'ok', failingSince: null, lastAlertAt: prior.lastAlertAt, reason: '' },
    };
  }
  if (prior.status !== 'failing') {
    return {
      email: 'alert',
      next: { status: 'failing', failingSince: nowIso, lastAlertAt: nowIso, reason: result.reason },
    };
  }
  const last = prior.lastAlertAt ? Date.parse(prior.lastAlertAt) : Number.NaN;
  const due = !Number.isFinite(last) || nowMs - last >= REMINDER_MS;
  return {
    email: due ? 'reminder' : null,
    next: {
      status: 'failing',
      failingSince: prior.failingSince ?? nowIso,
      lastAlertAt: due ? nowIso : prior.lastAlertAt,
      reason: result.reason,
    },
  };
}

export function buildRawMime(email: OutboundEmail): string {
  const sentAt = new Date(email.sentAtMs ?? Date.now()).toUTCString();
  const headers = [
    `From: ${oneLine(email.from)}`,
    `To: ${oneLine(email.to)}`,
    `Subject: ${oneLine(email.subject)}`,
    `Date: ${oneLine(sentAt)}`,
    `Message-ID: <${crypto.randomUUID()}@${oneLine(messageDomain(email.from))}>`,
    'MIME-Version: 1.0',
    'Content-Type: text/plain; charset=utf-8',
    'Content-Transfer-Encoding: 8bit',
  ];
  const body = email.text.replace(/\r\n/g, '\n').replace(/\n/g, '\r\n');
  return `${headers.join('\r\n')}\r\n\r\n${body}`;
}

export async function deliverEmail(
  send: (message: unknown) => Promise<unknown>,
  email: OutboundEmail,
  ctor: EmailMessageCtor | null,
): Promise<void> {
  const raw = buildRawMime(email);
  if (ctor) {
    await send(new ctor(email.from, email.to, raw));
    return;
  }
  // Node tests have no cloudflare:email module. The raw MIME is the same
  // payload EmailMessage carries for the destination_address binding.
  await send(raw);
}

export function healthcheckTarget(base: string | undefined, ok: boolean): string | null {
  const trimmed = base?.trim() ?? '';
  if (!trimmed) return null;
  const noTrail = trimmed.replace(/\/+$/, '');
  return ok ? noTrail : `${noTrail}/fail`;
}

export async function checkUptime(
  targetUrl: string,
  fetchImpl: typeof fetch = fetch,
  timeoutMs: number = UPTIME_TIMEOUT_MS,
): Promise<CheckResult> {
  let url: string;
  try {
    url = buildUrl(targetUrl);
  } catch {
    return { ok: false, reason: 'TARGET_URL is not a valid URL' };
  }
  const signal = AbortSignal.timeout(timeoutMs);
  try {
    const response = await fetchImpl(url, { method: 'GET', redirect: 'manual', signal });
    if (response.status !== 200) {
      await discard(response);
      return { ok: false, reason: `HTTP ${response.status}` };
    }
    const text = await response.text();
    try {
      JSON.parse(text);
    } catch {
      return { ok: false, reason: 'response was not valid JSON' };
    }
    return { ok: true, reason: '' };
  } catch (err) {
    if (isTimeout(err, signal)) {
      return { ok: false, reason: 'timed out after 10 seconds' };
    }
    return { ok: false, reason: `request failed (${errorName(err)})` };
  }
}

export function manifestKeyFor(objectKey: string): string | null {
  if (!objectKey.endsWith(SNAPSHOT_SUFFIX)) return null;
  return objectKey.slice(0, -SNAPSHOT_SUFFIX.length) + '.manifest.json';
}

export function evaluateBackups(
  objects: readonly ListedObject[],
  manifests: ReadonlyMap<string, string>,
  nowMs: number,
  prefixes: readonly string[] = DEFAULT_PREFIXES,
): CheckResult {
  const reasons: string[] = [];
  const watched = objects.filter((obj) => matchesPrefix(obj.key, prefixes) && !isWatchState(obj.key));
  const snapshots = watched.filter((obj) => obj.key.endsWith(SNAPSHOT_SUFFIX));
  const newest = newestOf(snapshots);

  if (!newest) {
    reasons.push(`no backup snapshot in ${prefixes.join(' or ')}`);
  } else {
    const age = nowMs - newest.uploadedMs;
    if (age > FRESHNESS_MS) {
      reasons.push(`newest snapshot ${newest.key} is ${formatDuration(age)} old, past 26 hours`);
    }
    const manifestKey = manifestKeyFor(newest.key);
    if (!manifestKey || !manifests.has(manifestKey)) {
      reasons.push(`missing manifest for ${newest.key}`);
    } else {
      const parsed = parseManifest(manifests.get(manifestKey) ?? '');
      if (!parsed.valid) {
        reasons.push(`manifest for ${newest.key} is not valid JSON`);
      } else {
        if (parsed.integrity !== 'ok') {
          const shown = typeof parsed.integrity === 'string' ? JSON.stringify(parsed.integrity) : 'missing';
          reasons.push(`manifest integrity is ${shown} for ${newest.key}`);
        }
        if (parsed.ciphertextSize === undefined || parsed.ciphertextSize !== newest.size) {
          const shown = parsed.ciphertextSize === undefined ? 'missing' : String(parsed.ciphertextSize);
          reasons.push(
            `manifest ciphertext size ${shown} does not match object size ${newest.size} for ${newest.key}`,
          );
        }
      }
    }
    const prefix = prefixOf(newest.key, prefixes);
    const previous = newestOf(
      snapshots.filter((obj) => obj.key !== newest.key && prefixOf(obj.key, prefixes) === prefix),
    );
    // Integer bytes: size * 2 < previous is the exact 50% line (equal passes).
    if (previous && newest.size * 2 < previous.size) {
      reasons.push(
        `newest snapshot ${newest.key} is ${newest.size} bytes, under half of previous ${previous.key} (${previous.size} bytes)`,
      );
    }
  }

  for (const obj of watched) {
    if (nowMs - obj.uploadedMs > OVER_AGE_MS) {
      reasons.push(`object ${obj.key} is older than 15 days (uploaded ${formatInstant(obj.uploadedMs)})`);
    }
  }

  return { ok: reasons.length === 0, reason: reasons.join('; ') };
}

export async function checkBackups(
  store: ObjectStore,
  nowMs: number,
  prefixes: readonly string[] = DEFAULT_PREFIXES,
): Promise<CheckResult> {
  let objects: ListedObject[];
  try {
    const pages = await Promise.all(prefixes.map((prefix) => store.list(prefix)));
    objects = pages.flat();
  } catch (err) {
    console.error(JSON.stringify({ message: 'could not list backup objects', error: errorName(err) }));
    return { ok: false, reason: 'could not list backup objects' };
  }

  const snapshots = objects.filter(
    (obj) => matchesPrefix(obj.key, prefixes) && !isWatchState(obj.key) && obj.key.endsWith(SNAPSHOT_SUFFIX),
  );
  const newest = newestOf(snapshots);
  const manifests = new Map<string, string>();
  if (newest) {
    const manifestKey = manifestKeyFor(newest.key);
    if (manifestKey) {
      try {
        const text = await store.getText(manifestKey);
        if (text !== null) manifests.set(manifestKey, text);
      } catch (err) {
        console.error(JSON.stringify({ message: 'could not read backup manifest', error: errorName(err) }));
        return { ok: false, reason: `could not read manifest for ${newest.key}` };
      }
    }
  }
  return evaluateBackups(objects, manifests, nowMs, prefixes);
}

export function r2Store(bucket: R2BucketLike): ObjectStore {
  return {
    async list(prefix: string): Promise<ListedObject[]> {
      const out: ListedObject[] = [];
      let cursor: string | undefined;
      for (;;) {
        const page = await bucket.list(cursor ? { prefix, cursor } : { prefix });
        for (const obj of page.objects) {
          out.push({ key: obj.key, size: obj.size, uploadedMs: obj.uploaded.getTime() });
        }
        if (!page.truncated || !page.cursor || page.cursor === cursor) return out;
        cursor = page.cursor;
      }
    },
    async getText(key: string): Promise<string | null> {
      const obj = await bucket.get(key);
      if (!obj) return null;
      return obj.text();
    },
    async putText(key: string, body: string): Promise<void> {
      await bucket.put(key, body, { httpMetadata: { contentType: 'application/json' } });
    },
  };
}

export async function runWatch(env: WatchBindings, options: RunOptions): Promise<RunReport> {
  const fetchImpl = options.fetchImpl ?? fetch;
  const pingImpl = options.pingImpl ?? fetchImpl;
  const ctor = options.emailCtor ?? null;
  const state = await readWatchState(env.BACKUPS);

  const uptime = await checkUptime(env.TARGET_URL, fetchImpl);
  const uptimeDecision = transition(state.uptime, uptime, options.nowMs);
  const uptimeCommit = await commitCheck(
    env,
    ctor,
    'uptime',
    state.uptime,
    uptime,
    uptimeDecision,
    options.nowMs,
  );
  state.uptime = uptimeCommit.state;
  state.updatedAt = new Date(options.nowMs).toISOString();
  logCheck('uptime', uptime.ok, uptimeCommit.report.emailError ? null : uptimeCommit.report.email);
  await writeWatchState(env.BACKUPS, state);
  await pingHealthcheck(env.HC_UPTIME_URL, uptime.ok, pingImpl);

  if (!options.runBackup) {
    return { uptime: uptimeCommit.report, backup: { skipped: true } };
  }

  const backup = await checkBackups(env.BACKUPS, options.nowMs, backupPrefixes(env.BACKUP_DRILL_PREFIX));
  const backupDecision = transition(state.backup, backup, options.nowMs);
  const backupCommit = await commitCheck(
    env,
    ctor,
    'backup',
    state.backup,
    backup,
    backupDecision,
    options.nowMs,
  );
  state.backup = backupCommit.state;
  state.updatedAt = new Date(options.nowMs).toISOString();
  logCheck('backup', backup.ok, backupCommit.report.emailError ? null : backupCommit.report.email);
  await writeWatchState(env.BACKUPS, state);
  await pingHealthcheck(env.HC_BACKUP_URL, backup.ok, pingImpl);

  return { uptime: uptimeCommit.report, backup: backupCommit.report };
}

async function commitCheck(
  env: WatchBindings,
  ctor: EmailMessageCtor | null,
  check: 'uptime' | 'backup',
  prior: CheckState,
  result: CheckResult,
  decision: Transition,
  nowMs: number,
): Promise<{ state: CheckState; report: CheckReport }> {
  const report: CheckReport = { ok: result.ok, reason: result.reason, email: decision.email };
  if (!decision.email) return { state: decision.next, report };
  try {
    await sendNotice(env, ctor, check, decision, result, nowMs, prior.failingSince);
    return { state: decision.next, report };
  } catch (err) {
    const emailError = errorName(err);
    console.error(JSON.stringify({ message: 'could not send watch email', check, error: emailError }));
    report.emailError = emailError;
    return { state: prior, report };
  }
}

function sinceFor(kind: AlertKind, priorSince: string | null, nextSince: string | null): string | null {
  return kind === 'resolved' ? priorSince : nextSince;
}

async function sendNotice(
  env: WatchBindings,
  ctor: EmailMessageCtor | null,
  check: 'uptime' | 'backup',
  decision: Transition,
  result: CheckResult,
  nowMs: number,
  priorSince: string | null,
): Promise<void> {
  if (!decision.email) return;
  const since = sinceFor(decision.email, priorSince, decision.next.failingSince);
  await deliverEmail(env.send, composeEmail(decision.email, check, result, nowMs, env, since), ctor);
}

function composeEmail(
  kind: AlertKind,
  check: 'uptime' | 'backup',
  result: CheckResult,
  nowMs: number,
  env: WatchBindings,
  failingSince: string | null,
): OutboundEmail {
  const tag = kind === 'resolved' ? '[RESOLVED]' : '[ALERT]';
  const reminder = kind === 'reminder' ? ' reminder' : '';
  const lines = [
    `check: ${check}`,
    `status: ${result.ok ? 'ok' : 'failing'}`,
    `notice: ${kind}`,
    `at: ${formatInstant(nowMs)}`,
  ];
  if (check === 'uptime') {
    try {
      lines.push(`target: ${buildUrl(env.TARGET_URL)}`);
    } catch {
      // The reason already says the URL is invalid.
    }
  }
  if (failingSince) {
    const parsed = Date.parse(failingSince);
    if (Number.isFinite(parsed)) lines.push(`failing since: ${formatInstant(parsed)}`);
  }
  if (result.reason) lines.push(`reason: ${result.reason}`);
  return {
    from: env.ALERT_FROM,
    to: env.ALERT_TO,
    subject: `${tag} pantry-pi-watch ${check}${reminder}`,
    text: lines.join('\n'),
    sentAtMs: nowMs,
  };
}

async function readWatchState(store: ObjectStore): Promise<WatchState> {
  try {
    const text = await store.getText(STATE_KEY);
    if (text === null) return emptyWatchState();
    return parseWatchState(text);
  } catch (err) {
    console.error(JSON.stringify({ message: 'could not read watch state', error: errorName(err) }));
    return emptyWatchState();
  }
}

export function parseWatchState(text: string): WatchState {
  try {
    const raw: unknown = JSON.parse(text);
    if (!raw || typeof raw !== 'object' || Array.isArray(raw)) return emptyWatchState();
    const rec = raw as Record<string, unknown>;
    return {
      version: 1,
      updatedAt: typeof rec.updatedAt === 'string' ? rec.updatedAt : null,
      uptime: normalizeCheck(rec.uptime),
      backup: normalizeCheck(rec.backup),
    };
  } catch {
    return emptyWatchState();
  }
}

async function writeWatchState(store: ObjectStore, state: WatchState): Promise<void> {
  await store.putText(STATE_KEY, JSON.stringify(state));
}

function normalizeCheck(value: unknown): CheckState {
  if (!value || typeof value !== 'object' || Array.isArray(value)) return emptyCheckState();
  const rec = value as Record<string, unknown>;
  const status = rec.status === 'failing' ? 'failing' : 'ok';
  return {
    status,
    failingSince: typeof rec.failingSince === 'string' ? rec.failingSince : null,
    lastAlertAt: typeof rec.lastAlertAt === 'string' ? rec.lastAlertAt : null,
    reason: typeof rec.reason === 'string' ? rec.reason : '',
  };
}

async function pingHealthcheck(raw: string | undefined, ok: boolean, fetchImpl: typeof fetch): Promise<void> {
  const target = healthcheckTarget(raw, ok);
  if (!target) return;
  try {
    const response = await fetchImpl(target, { method: 'POST', signal: AbortSignal.timeout(UPTIME_TIMEOUT_MS) });
    if (!response.ok) {
      await discard(response);
      console.error(JSON.stringify({ message: 'healthcheck ping failed', status: response.status }));
      return;
    }
    await discard(response);
  } catch (err) {
    console.error(JSON.stringify({ message: 'healthcheck ping failed', error: errorName(err) }));
  }
}

function logCheck(check: string, ok: boolean, email: AlertKind | null): void {
  const line = JSON.stringify({ message: 'watch check', check, ok, email });
  if (ok) console.log(line);
  else console.error(line);
}

function newestOf(objects: readonly ListedObject[]): ListedObject | null {
  let best: ListedObject | null = null;
  for (const obj of objects) {
    if (!best || obj.uploadedMs > best.uploadedMs || (obj.uploadedMs === best.uploadedMs && obj.key > best.key)) {
      best = obj;
    }
  }
  return best;
}

function matchesPrefix(key: string, prefixes: readonly string[]): boolean {
  return prefixes.some((prefix) => key.startsWith(prefix));
}

function prefixOf(key: string, prefixes: readonly string[]): string | null {
  return prefixes.find((prefix) => key.startsWith(prefix)) ?? null;
}

function isWatchState(key: string): boolean {
  return key === STATE_KEY || key.endsWith(`/${STATE_KEY}`);
}

interface ParsedManifest {
  valid: boolean;
  integrity?: unknown;
  ciphertextSize?: number;
}

function parseManifest(raw: string): ParsedManifest {
  try {
    const value: unknown = JSON.parse(raw);
    if (!value || typeof value !== 'object' || Array.isArray(value)) return { valid: false };
    const rec = value as Record<string, unknown>;
    const size = rec.ciphertext_size;
    return {
      valid: true,
      integrity: rec.integrity,
      ciphertextSize: typeof size === 'number' && Number.isFinite(size) ? size : undefined,
    };
  } catch {
    return { valid: false };
  }
}

function messageDomain(from: string): string {
  const at = from.lastIndexOf('@');
  const domain = at >= 0 ? from.slice(at + 1).trim() : '';
  return domain || 'rhionin.com';
}

function oneLine(value: string): string {
  return value.replace(/[\r\n]+/g, ' ').trim();
}

function isTimeout(err: unknown, signal: AbortSignal): boolean {
  if (signal.aborted) return true;
  return err instanceof Error && (err.name === 'TimeoutError' || err.name === 'AbortError');
}

function errorName(err: unknown): string {
  return err instanceof Error && err.name ? err.name : 'Error';
}

async function discard(response: Response): Promise<void> {
  try {
    await response.body?.cancel();
  } catch {
    // A fake response may not have a cancellable body.
  }
}
