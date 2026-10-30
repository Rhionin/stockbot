import { readFileSync } from 'node:fs';
import { dirname, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { describe, expect, it } from 'vitest';
import {
  classifyImportDocument,
  CONFIG_PATH,
  EVENTS_PATH,
  EXPO_CLIENT_PREFIX,
  HOSTED_ERROR_SENTENCES,
  LIVE_HINT_EVENT,
  UPGRADE_REQUIRED_SENTENCE,
  expoClientHeader,
  type AcceptInviteRequest,
  type HostedErrorCode,
  type HouseholdSettings,
  type InviteCreateRequest,
  type LiveHint,
} from '@stockbot/shared/contract';

// Pins the union to a real member so a deleted code fails typecheck.
const sampleCode: HostedErrorCode = 'busy';
const sampleHint: LiveHint = { type: 'scans', cursor: 'cursor' };

const root = resolve(dirname(fileURLToPath(import.meta.url)), '../../..');
const doc = readFileSync(resolve(root, 'docs/design/api-contract.md'), 'utf8');
const source = readFileSync(resolve(root, 'shared/contract.ts'), 'utf8');

// Sentences already shipped, locked by PR #320, or approved by PM on PR #364.
const existingSentences: Record<string, string> = {
  busy: 'Stockbot is busy right now. Try again in a moment.',
  no_space: "Stockbot is out of storage space, so this wasn't saved.",
  unauthorized: 'Sign in to continue.',
  forbidden_role: 'Only an owner can do that.',
  forbidden_owner: 'Only an owner can do that.',
  forbidden_operator: 'Only an admin can do this.',
  household_not_found: "Stockbot couldn't find that. It may have been removed.",
  invalid_input: 'Check what you entered and try again.',
  not_found: "Stockbot couldn't find that. It may have been removed.",
  conflict: 'Someone else just changed this. Refresh and try again.',
  invite_expired: 'This invite has expired or was already used. Ask for a new one.',
  validation: 'Check what you entered and try again.',
  not_pending: 'This scan was already handled.',
  product_unresolved: 'Choose which item this is first.',
  none_on_hand: 'Nothing on hand to take out.',
  instance_gone: 'That one was already taken out. Pick another.',
  no_item: 'Which item is this?',
  link_target_gone: "That item isn't available anymore. Pick another.",
  scan_not_found: 'This scan is no longer in the queue.',
  direction_unset: 'Choose stock in or stock out first.',
  internal: 'Something went wrong. Try again.',
  last_owner: "You're the only owner. Make someone else an owner first, or delete the household.",
  household_not_empty: 'You can only import into an empty household. This one already has items.',
  confirmation_mismatch: "That doesn't match. Check the spelling and try again.",
  rate_limited: 'Too many tries. Wait a moment and try again.',
  payload_too_large: 'That file is too large to import.',
  invite_required: 'You need an invite to start a household.',
  reset_expired: 'This reset link has expired or was already used. Ask for a new one.',
  account_exists: 'That email already has an account. Sign in, then open the invite again.',
  device_revoked: 'This device was disconnected. Pair it again to keep using it.',
  upgrade_required: 'Update Stockbot to keep using it.',
};

interface RegistryRow {
  code: string;
  sentence: string;
  notes: string;
}

function registry(markdown: string): RegistryRow[] {
  const start = markdown.indexOf('### Error code registry');
  const end = markdown.indexOf('## Open questions');
  const section = start >= 0 && end > start ? markdown.slice(start, end) : '';
  const rows: RegistryRow[] = [];
  for (const line of section.split('\n')) {
    if (!line.startsWith('| `')) continue;
    const parts = line.split('|');
    if (parts.length < 5) continue;
    rows.push({
      code: parts[1].replaceAll('`', '').trim(),
      sentence: parts[3].trim(),
      notes: parts[4].trim(),
    });
  }
  return rows;
}

function errorCodes(ts: string): string[] {
  const match = ts.match(/export type HostedErrorCode =\s*([^;]+);/s);
  if (match === null) return [];
  return [...match[1].matchAll(/'([a-z0-9_]+)'/g)].map((found) => found[1]);
}

function endpointIndex(markdown: string): string[] {
  const lines = markdown.split('\n');
  const start = lines.indexOf('endpoint-index');
  if (start < 0) return [];
  const paths: string[] = [];
  for (const line of lines.slice(start + 1)) {
    if (line.startsWith('```')) break;
    if (line.trim() !== '') paths.push(line.trim());
  }
  return paths;
}

describe('hosted API contract', () => {
  it('uses the sample members so the types stay in the typecheck', () => {
    expect(sampleCode).toBe('busy');
    expect(sampleHint.type).toBe('scans');
  });

  it('lists the same error codes in the doc and the TypeScript union', () => {
    const rows = registry(doc);
    const codes = rows.map((row) => row.code);
    expect(new Set(codes).size).toBe(codes.length);
    expect(errorCodes(source).sort()).toEqual([...codes].sort());
  });

  it('keeps the approved sentences and does not leave them for PM to confirm', () => {
    expect(doc).not.toContain('proposed, PM to confirm');
    for (const row of registry(doc)) {
      expect(existingSentences[row.code]).toBe(row.sentence);
      expect(HOSTED_ERROR_SENTENCES[row.code as HostedErrorCode]).toBe(row.sentence);
    }
    expect(doc).toContain('generic-sentence: Something went wrong. Try again.');
    expect(doc).toContain('stock target');
    expect(doc).not.toContain('TODO(CJ)');
    expect(doc).toContain('Decided by CJ, via Network Engineer, on 2026-10-22');
    expect(doc).toContain('<APP_BASE_URL>/invite/<code>');
    expect(doc).toContain('https://app.stockbot.rhionin.com');
    expect(doc).toContain('mail.stockbot.rhionin.com');
  });

  it('states the tenant rule and the endpoint index count', () => {
    expect(doc).toContain(
      'tenant-rule: Nothing in this contract accepts a client-supplied tenant other than the X-Household-Id header, and that header is valid only when it names a household the signed-in user belongs to, or, for device calls, the household bound to the device token, except the householdId body on POST /api/auth/session/household, which sets the session\'s active household after a membership check; nothing else selects a tenant.',
    );
    expect(doc).toContain('userId-removed:');
    const paths = endpointIndex(doc);
    expect(paths.length).toBeGreaterThan(0);
    for (const path of paths) {
      expect(path).toMatch(/^(GET|POST|PUT|PATCH|DELETE) \/api\//);
    }
    const count = doc.match(/^endpoint-count: (\d+)$/m);
    expect(count).not.toBeNull();
    expect(Number(count?.[1])).toBe(paths.length);
  });

  it('accepts a synthetic Pi sync snapshot, with legacyUserId optional', () => {
    const fixture = JSON.parse(readFileSync(resolve(root, 'frontend/src/api/fixtures/pi-sync-snapshot.json'), 'utf8')) as Record<string, unknown>;
    expect(fixture).not.toHaveProperty('legacyUserId');
    expect(fixture).not.toHaveProperty('schemaVersion');
    const accepted = classifyImportDocument(fixture);
    expect(accepted).toEqual({ ok: true, kind: 'pi', warnings: [] });
    expect((fixture.products as { name: string; barcodes: string[] }[])[0]).toMatchObject({
      name: 'Chickpeas',
      barcodes: ['000111222333'],
    });

    const withUser = classifyImportDocument({ ...fixture, legacyUserId: 'user-1' });
    expect(withUser).toEqual({ ok: true, kind: 'pi', warnings: [] });

    const withUnknown = classifyImportDocument({ ...fixture, pushSubscriptions: [] });
    expect(withUnknown.ok).toBe(true);
    expect(withUnknown.warnings).toEqual(['pushSubscriptions']);

    expect(classifyImportDocument({ ...fixture, schemaVersion: 2 }).ok).toBe(false);
  });

  it('keeps the #364 device-scan and invite fields and adds the stage-1 auth routes', () => {
    expect(source).toContain('heartbeat?: boolean');
    expect(source).toContain("status: 'accepted' | 'duplicate'");
    expect(source).toContain("status: 'open' | 'revoked' | 'used' | 'expired'");
    expect(source).toContain('minClientVersion: { expo: string }');
    expect(source).toContain("type: 'devices'");
    expect(doc).toContain('__Host-stockbot_session');
    expect(doc).toContain('POST /api/auth/login');
    expect(doc).toContain('POST /api/auth/reset');
    expect(doc).toContain('Clients label `revoked` as Cancelled.');
    expect(doc).toContain('A missing `X-Stockbot-Client` header means web, so there is no version gate.');
    expect(doc).toContain('Any `X-Stockbot-Client` value that does not start with `expo/` is web too, the same as a missing header, so there is no version gate.');
    expect(doc).toContain('The same `426` applies when that Basic request carries a value that does not start with `expo/`.');
    expect(doc).not.toContain('On the LAN it stays open, with no session.');
    expect(doc).not.toContain('Superseded by PM');
    expect(doc).toContain('S6 (#379) must run the production classifier in `internal/scan/qr.go` against `testdata/pairing-codes.json`.');
    expect(doc).toContain('`POST /api/devices/pair/complete` | No session');
    expect(doc).toContain('`Authorization: Basic` and no `X-Stockbot-Client` returns `426` `upgrade_required`, not `401`');
    expect(doc).toContain('activeHouseholdId order');
    expect(source).toContain('export interface LoginResponse');
    expect(doc).toContain('"inviteCode"?: string');
    expect(source).toContain('inviteCode?: string');
    expect(doc).toContain('not_found');
    expect(doc).toContain('BEGIN IMMEDIATE');
    expect(doc).toContain('UUIDv7 text ids preserved; no sequences');
    expect(doc).toContain('Stage 3; Pi returns `not_found`');
    expect(doc).toContain('GET /api/events');
    expect(doc).toContain('looksLikePairingCode');
    expect(doc).toContain('pairing-collision: A pairing code cannot collide with a product barcode.');
    expect(doc).toContain('Invite codes are 48 bits.');
    expect(doc).toContain('Pairing codes are 40 bits.');
    expect(doc).toContain('The setup code is 40 bits.');
    expect(doc).toContain('O, I, and L are aliases: O maps to 0, and I or L maps to 1.');
    expect(doc).toContain('testdata/pairing-codes.json');
    expect(doc).toContain('When two requests use the same invite, the loser gets `invite_expired`.');
    expect(doc).toContain('The `426` `upgrade_required` gate runs before auth, rate limits, and busy handling');
    expect(doc).toContain('On the LAN, after the first account exists, GET sends the user to sign-in at `APP_BASE_URL` and POST returns `not_found`, except in recover mode, where GET serves the page and a successful POST sends the user to sign-in at `APP_BASE_URL`.');
    expect(doc).not.toContain('On success, and on a GET after the first account exists');
    expect(doc).toContain('No session on the LAN. A session on the public entry');
    expect(doc).toContain('The saved-origin fallback applies only when `GET /api/config` omits `entry` (an older server). If `appBaseUrl` is present, a saved origin is LAN when it is http: or its host is not `appBaseUrl`\'s host. If `appBaseUrl` is also absent, an http: origin is LAN and an https: origin is public.');
    expect(doc).toContain('set by the listener that served the request');
    expect(doc).toContain('The pantry server opens two listeners. `:8080` is the LAN entry, published to the host as today. `:8081` is the public entry. It is not published to the host and is reachable only on the Docker network, where the reverse proxy forwards to `pantry:8081`. Everything on `:8081` is public. Everything on `:8080` is LAN. Stage 1 removes `X-Pantry-Entry`: it is never honored anywhere. `X-Pantry-Client-IP` is read only on `:8081`, as the rate-limit key. On `:8080` the key is always `RemoteAddr`. `X-Forwarded-For` is never trusted. If `APP_BASE_URL` is set, the server refuses to start unless the public listener (`:8081`) is enabled. There is no fail-open path. A server without `APP_BASE_URL` needs no `:8081` listener, so dev and test servers can run `:8080` only, and the refusal to start applies only when `APP_BASE_URL` is set. On a hairpin or userland-proxy path, the LAN `RemoteAddr` can be the Docker bridge gateway, which would make the 10-streams-per-IP LAN cap effectively household-wide. That is accepted for stage 1.');
    expect(doc).not.toContain('from this classification');
    expect(doc).not.toContain('trusted proxy');
    expect(doc).not.toContain('trusted-proxy');
    expect(doc).not.toContain('Docker bridge subnet');
    expect(doc).not.toContain('acceptable alternative');
    expect(doc).not.toContain('separate upstream port');
    expect(doc).not.toContain('Today LAN and public share `:8080`');
    expect(doc).not.toContain('one matching no public hostname');
    expect(doc).not.toContain('differs from `appBaseUrl`');
    expect(doc).toContain('on the LAN after retirement, unauthorized even with no cookie or bearer; Bearer sbs_ on the LAN is always unauthorized.');
    expect(doc).toContain('`unauthorized`, `validation`, `rate_limited`, `busy`. On the LAN, `404` `not_found`');
    expect(doc).toContain('`validation`, `reset_expired`, `not_found`, `rate_limited`, `busy`. On the LAN, `404` `not_found`');
    expect(doc).toContain('The first setup creates the operator as owner of household 1 and sets no session.');
    expect(doc).toContain('Recover resets the existing operator\'s password. It never creates an account, never changes the email, and never changes roles.');
    expect(doc).toContain('The POST body is `{ "setupCode": string, "email": string, "newPassword": string }`.');
    expect(doc).toContain('An unknown or non-operator email gets the same `404` `not_found` as a bad code, so the endpoint never reveals which emails exist.');
    expect(doc).toContain('set the new argon2id hash; revoke all of that account\'s sessions (web and Expo `sbs_`); delete the push subscriptions tied to those sessions and close their SSE streams; invalidate any outstanding reset tokens for that account; set the code row\'s `used` to true.');
    expect(doc).toContain('Device tokens (`sbd_`) and the household\'s other members are untouched.');
    expect(doc).toContain('No session is set. A successful POST sends the user to sign-in at `APP_BASE_URL`.');
    expect(doc).toContain('`POST /setup`, in both first-setup and recover mode, uses the same per-IP limit as `POST /api/auth/login`, keyed on `RemoteAddr` on `:8080`, sharing that route\'s per-IP bucket of 5 failures per 15 minutes. The 6th failing `POST /setup` from one `RemoteAddr` on `:8080` within 15 minutes gets `429` `rate_limited` with `Retry-After`. The rate limit is checked before the code, so a limited request gets `429` and doesn\'t count again. A failed attempt is any `POST /setup` with a well-formed body that doesn\'t succeed: a bad or expired code, or a right code with an unknown or non-operator email. All return the same `404` `not_found` and count toward the limit. A malformed body gets `400` `validation` and doesn\'t count. The code is used only on success. The recover code also expires after 24 h. Recover mode and GET /setup behave exactly as the `/setup` sentence says. `POST /setup` failures count only toward the per-IP bucket, not login\'s per-email bucket.');
    expect(doc).toContain('The Pi user can read it without sudo, and only the server writes or deletes it.');
    expect(doc).toContain('`/opt/pantry/setup/`');
    expect(doc).toContain('mode `2750` (setgid)');
    expect(doc).toContain('bind-mounted read-write at `/run/setup`');
    expect(doc).toContain('The issue order is: write the temp file inside `/run/setup` and chmod it to `0640` explicitly, then `BEGIN IMMEDIATE`, insert the row (marking any row it replaces as used), rename the temp file to `code`, and `COMMIT`.');
    expect(doc).toContain('The rename happens inside the transaction, before `COMMIT`, so a concurrent check can\'t see a committed row before its file exists');
    expect(doc).toContain('If the temp-file write fails, no row is committed.');
    expect(doc).toContain('deletes the temp file');
    expect(doc).toContain('Only an unused, unexpired code whose hash matches is accepted.');
    expect(doc).toContain('Deleting the file is best-effort after that commit and can never make a code reusable.');
    expect(doc).toContain('At startup, the server deletes the file only if no unused, unexpired row exists.');
    expect(doc).toContain('`issued_at` is RFC 3339 UTC.');
    expect(doc).toContain('A successful `POST /setup` sets `used` to true in the same transaction as the account or password write.');
    expect(doc).toContain('Only automatic issuing reuses an unused, unexpired code whose display file is present and matches.');
    expect(doc).toContain('An `issued_at` more than 24 h in the future counts as expired.');
    expect(doc).toContain('A code is expired when `now - issued_at >= 24 h`.');
    expect(doc).toContain('If the clock reads earlier than `issued_at` by less than 24 h (the Pi booted before NTP synced), the code counts as valid and is not reissued.');
    expect(doc).toContain('A clock jump forward can at worst expire it early, which just means a new code is issued.');
    expect(doc).toContain('The "no account exists" check runs inside the account-creating transaction, so racing first-setup POSTs cannot create two operators.');
    expect(doc).toContain('That `404` counts toward the per-IP failure bucket, the same as any `/setup` failure.');
    expect(doc).toContain('`docker exec pantry stockbot-server admin setup-code` with no flag issues a fresh setup-mode code when no account exists, marking any unused code as used.');
    expect(doc).toContain('`docker exec pantry stockbot-server admin setup-code --recover` requires an existing operator and issues a recover-mode code the same way.');
    expect(doc).toContain('reused and never replaced');
    expect(doc).toContain('While no account exists, a missing `code` file or one that doesn\'t match the unused, unexpired row makes the server mark that row used and issue a fresh code by that issue order.');
    expect(doc).toContain('Recover-mode rows never reissue automatically, so a lost recover file needs another `--recover`.');
    expect(doc).toContain('`code_hash` is argon2id with the account-password parameters, not SHA-256 and not HMAC.');
    expect(doc).toContain('The no-flag `admin setup-code` with an existing account exits nonzero and changes nothing.');
    expect(doc).toContain('On `SQLITE_BUSY` it exits nonzero without touching the file.');
    expect(doc).toContain('A full disk shows `setupCode` `"no_space"`, not `"unwritable"`.');
    expect(doc).toContain('If `/setup` hits `busy` or `no_space`, it returns the existing `busy` or `no_space` codes, which don\'t count toward the per-IP bucket.');
    expect(doc).toContain('If `COMMIT` fails after the rename, setup mode self-heals, and the recover-mode admin command exits nonzero so the operator reruns it.');
    expect(doc).toContain('Serialization is in the database (`BEGIN IMMEDIATE`), so it also covers the `docker exec` process.');
    expect(doc).toContain('https://github.com/Rhionin/stockbot/pull/397#issuecomment-6375160842');
    expect(doc).toContain('https://github.com/Rhionin/stockbot/pull/397#issuecomment-6375169537');
    expect(doc).toContain('https://github.com/Rhionin/stockbot/pull/397#issuecomment-6374601821');
    expect(doc).toContain('Recover mode is the stored mode.');
    expect(doc).toContain('This pair is the manual reissue path.');
    expect(doc).toContain('`/health` carries a warning field `setupCode` `"unwritable"` only while no account exists.');
    expect(doc).toContain('`docker exec pantry stockbot-server admin setup-code --recover`');
    expect(doc).toContain('https://github.com/Rhionin/stockbot/pull/394#issuecomment-6374556040');
    expect(doc).not.toContain('updater writes');
    expect(doc).not.toContain('each server start writes');
    expect(doc).not.toContain('treated as consumed');
    expect(doc).not.toContain('a valid code always exists');
    expect(doc).not.toContain('mutex');
    expect(doc).not.toContain('the only way to reissue');
    expect(doc).not.toContain('The code file is consumed only on success');
    expect(doc).not.toContain('Nothing else changes it');
    expect(doc).not.toContain('The hash is SHA-256 of the random code');
    expect(doc).not.toContain('Anyone who can delete files in `/opt/pantry/setup` already controls the Pi');
    expect(doc).not.toContain('If the commit fails, the server deletes the temp file');
    expect(doc).not.toContain('by 24 h or less');
    expect(doc).not.toContain('now minus `issued_at` over 24 h');
    expect(doc).not.toContain('commit the database row, then rename');
    expect(doc).not.toContain('under 24 h old whose hash matches');
    expect(doc).toContain('Limits: 5 failures per 15 minutes per client IP, and 10 failures per 15 minutes per normalised email. Over the limit is `429` `rate_limited` with `Retry-After`. The body is still the registry sentence and does not say which field was wrong. `POST /setup` failures count only toward the per-IP bucket, not login\'s per-email bucket.');
    expect(doc).toContain('When `APP_BASE_URL` is unset, a successful setup or recover POST returns success with no redirect, and the page says the account is ready but sign-in isn\'t set up on this server (placeholder copy owned by PM), and on the LAN after the first account exists, outside recover mode, GET /setup is `not_found`.');
    expect(doc).toContain('When `appBaseUrl` is absent (an older server), Expo only signs in against an https: saved origin and never sends credentials to an http: origin.');
    expect(doc).toContain('Every `rate_limited` response sends `Retry-After` in seconds, `POST /setup` included.');
    expect(doc).toContain('The code is single-use with a 24 h expiry, and the setup rate limits defined above apply. An expired code returns `not_found`.');
    expect(doc).toContain('pairing complete, stage-1 `POST /api/households`, and `POST /setup`.');
    expect(doc).toContain('`POST /setup` (first setup and recover) uses the same per-IP limit as login, keyed on `RemoteAddr` on `:8080`.');
    expect(doc).not.toContain('the setup rate limits apply');
    expect(doc).toContain('(f) The 6th failing `POST /setup` from one `RemoteAddr` on `:8080` within 15 minutes gets `429` `rate_limited` with `Retry-After`, sharing `POST /api/auth/login`\'s per-IP bucket (5 failures per 15 minutes). The rate limit is checked before the code, so a limited request gets `429` and doesn\'t count again.');
    expect(doc).toContain('The server\'s listener change and both Caddyfile changes (every handle to `pantry:8081`, `X-Pantry-Entry` dropped) must ship in the same release. If the server stops reading `X-Pantry-Entry` while Caddy still targets `:8080`, all internet traffic is LAN, and the startup check can\'t catch it.');
    expect(doc).toContain('That same release updates every current `X-Pantry-Entry` user: `deploy/Caddyfile`, `deploy/Caddyfile.tunnel`, `deploy/setup.sh` (it probes with the header, around lines 546 and 554), `check-publish-modes.sh` (around lines 256–257), `setup_apply_test.sh` (around lines 313 and 766), `deploy/README.md` (around line 16), `cmd/server/README.md` (around line 172), `cmd/server/public_compose_test.go` (around line 111 it asserts `deploy/Caddyfile` contains `X-Pantry-Entry public` and `reverse_proxy pantry:8080`, and around line 321 the same header in the tunnel file; this test must change in the same release as the Caddyfile change, or CI fails), `cmd/server/main.go` (around line 386), `internal/server/server.go` (around line 295), `internal/server/handler_sync_test.go` (around line 77), `internal/server/handler_push_test.go` (around lines 293, 301, 316, and 331), `internal/server/sentry_test.go` (around line 720), `internal/server/sentry_tunnel_test.go` (around lines 671, 681, 691, 701, and 743), `docs/design/multi-household-staged.md`, and `docs/design/multi-tenant-saas.md`.');
    expect(doc).toContain('During rollout, a `:8080` request carrying `X-Pantry-Entry` is ignored, never refused, and the server logs it at info.');
    expect(doc).not.toContain('may refuse');
    expect(doc).toContain('(g) Both `deploy/Caddyfile` and `deploy/Caddyfile.tunnel` point every handle at `pantry:8081`, and neither contains `pantry:8080`.');
    expect(doc).not.toContain('invalidated');
    expect(doc).not.toContain('fresh code from the updater');
    expect(doc).not.toContain('fresh one is issued');
    expect(doc).not.toContain('removed entirely');
    expect(doc).not.toContain('public is only the `X-Pantry-Entry` header');
    expect(doc).not.toContain('The port binding is owned by infra.');
    expect(doc).toContain('It is an infra deploy step owned by the network engineer, paired with S15/S5.');
    expect(doc).toContain('(a) On `:8080`, `X-Pantry-Entry: public` on `POST /api/auth/login` is ignored, so `404` `not_found`, and `GET /api/config` returns `entry` `"lan"`.');
    expect(doc).toContain('(b) On `:8080`, 11 `POST /api/devices/pair/complete` attempts, each with a different `X-Pantry-Client-IP` and `X-Forwarded-For`, are keyed on `RemoteAddr`, so the attempt over the limit gets `429` `rate_limited`.');
    expect(doc).toContain('(c) On `:8081`, `X-Pantry-Client-IP` sets the key and `X-Forwarded-For` is ignored.');
    expect(doc).toContain('(d) On `:8081`, a request with no `X-Pantry-Entry` (`GET /api/telemetry`, `POST /api/telemetry/client`, and `GET /api/build`) is public.');
    expect(doc).toContain('(e) Startup with `APP_BASE_URL` set and the public listener disabled fails.');
    expect(doc).toContain('On routers without NAT hairpin, the certificate (non-tunnel) path can hang from home Wi-Fi (see the comments in `deploy/Caddyfile`).');
    expect(doc).toContain('The client-facing type marks `entry` and `appBaseUrl` optional, because an older server omits them and the client falls back. The Go server always sends `entry`. It sends `appBaseUrl` only when `APP_BASE_URL` is set and omits the field otherwise (never an empty string), so a LAN-only server follows the absent-`appBaseUrl` rule.');
    expect(doc).toContain('`appBaseUrl` is omitted when `APP_BASE_URL` is unset.');
    expect(doc).not.toContain('The Go server always sends both fields.');
    expect(doc).toContain('The hosted push arm types `entry` as `\'public\'`.');
    expect(doc).toContain('Hosted `LiveConfig` sends `entry` `"public"` only.');
    expect(doc).not.toContain('Setup sets it when the person has exactly one household');
    expect(doc).not.toContain('signs them in, and sets `activeHouseholdId`');
    expect(doc).toContain('clientmsg.Generic');
    expect(doc).not.toContain('clientmsg.ServerError');
    expect(doc).toContain('Required in stage 3 only. The Pi takes the household from the job row.');
    expect(doc).toContain('Trim only the ASCII whitespace characters space, tab, LF, VT, FF, and CR.');
    expect(doc).toContain('typed `*`, `~`, `$`, and `=` are allowed');
    expect(doc).toContain('The LAN listener never offers personal sign-in, whether or not the household password is on.');
    expect(doc).toContain('invite accept with `account`, and `POST /api/households` return `404` `not_found`');
    expect(doc).toContain('`Authorization: Bearer sbs_…` on `:8080` returns `401` `unauthorized` and is never honored.');
    expect(doc).toContain('Session cookies are never set and never honored on the LAN.');
    expect(doc).toContain('lan-client: Expo must use `appBaseUrl` for personal sign-in. LAN URLs are unsupported for people after retirement. Clients never show Sign in on a LAN URL.');
    expect(doc).toContain('"appBaseUrl"?: string');
    expect(doc).toContain('Both entries omit it when `APP_BASE_URL` is unset.');
    expect(doc).toContain('omitting `appBaseUrl` when `APP_BASE_URL` is unset.');
    expect(doc).not.toContain('"appBaseUrl": string');
    expect(doc).not.toContain('Both entries return it.');
    expect(doc).not.toContain('no longer read');
    expect(doc).toContain('The 11th stream is `429` `rate_limited`, with `Retry-After` in seconds.');
    expect(doc).toContain('When the stream errors, the client refetches `GET /api/auth/session`.');
    expect(doc).toContain('only that person\'s sessions whose active household was the removed one have `activeHouseholdId` set to null');
    expect(doc).toContain('Accept returns `token` only when `account` is sent.');
    expect(doc).toContain('household-start: A person who is already signed in starts another household here, with `inviteCode`.');
    expect(doc).toContain('A missing part compares as 0, so `1.0` equals `1.0.0`.');
    expect(doc).toContain('A new Expo build sends `X-Stockbot-Client` on every request, including `GET /api/events`.');
    expect(doc).toContain('expo-401: Expo clears its stored `sbs_` token on any `401`, except the sign-in routes `POST /api/auth/login`, `POST /api/auth/reset`, and `POST /api/invites/accept`.');
    expect(doc).not.toContain('`POST /api/invites/accept`, and `POST /setup`');
    expect(doc).toContain('Sign in at `appBaseUrl`.');
    expect(doc).not.toContain('Sign-in is required');
    expect(doc).toContain('per source IP: 10 per IP, and the 11th from that IP is `429` `rate_limited`');
    expect(doc).toContain('includes `Retry-After` in seconds, like the other `rate_limited` responses.');
    expect(doc).toContain('Keeps working on the LAN, where hubs pair against `http://pi:8080`.');
    expect(doc).toContain('Clients use `entry` as the exact signal.');
    expect(doc).toContain('Dev builds that sign in must set `APP_BASE_URL`, and servers without it run LAN only.');
    expect(doc).not.toContain('Dev and test servers must set `APP_BASE_URL`, or dev builds would point at prod.');
    expect(doc).toContain('"entry": "lan" | "public"');
    expect(source).toContain("export const CLIENT_HEADER = 'X-Stockbot-Client'");
    expect(EXPO_CLIENT_PREFIX).toBe('expo/');
    expect(expoClientHeader('1.0.0')).toBe('expo/1.0.0');
    expect(UPGRADE_REQUIRED_SENTENCE).toBe(existingSentences.upgrade_required);
    expect(EVENTS_PATH).toBe('/api/events');
    expect(LIVE_HINT_EVENT).toBe('hint');
    expect(CONFIG_PATH).toBe('/api/config');
    expect(source).toContain('appBaseUrl?: string');
    expect(source).not.toContain('appBaseUrl: string');
    expect(source).toContain("entry?: 'lan' | 'public'");
    expect(source).toContain("entry?: 'public'");
    expect(source).toContain("export const AUTH_SESSION_PATH = '/api/auth/session'");
    expect(source).toContain("export const SESSION_COOKIE_NAME = '__Host-stockbot_session'");
    expect(source).toContain("kind: SessionKind");
    expect(source).toContain('field?: string');
    expect(doc).toContain('https://stockbot.rhionin.com');
    expect(doc).toContain('LAN_PEOPLE=signin');
    expect(doc).toContain('households` is the same list');
    expect(doc).toContain('rekey` never appears on the Pi stream');
    expect(source).toContain("status: 'pending' | 'ready'");
    expect(source).toContain("status: 'created' | 'duplicate' | 'conflict' | 'rejected'");
    expect(source.match(/export interface CreateHouseholdRequest/g)).toHaveLength(1);
    expect(source).toContain('barcodeValue: string');
    expect(source).not.toContain('pairingCodeCollidesWithBarcode');
  });

  it('sets invite role at create and keeps household settings to restock mode', () => {
    type AcceptHasNoRole = 'role' extends keyof AcceptInviteRequest ? false : true;
    type SettingsDroppedSecret = 'householdPassword' extends keyof HouseholdSettings ? false : true;
    type CreateSetsRole = NonNullable<InviteCreateRequest['role']> extends 'member' | 'owner' ? true : false;
    const typePins: [AcceptHasNoRole, SettingsDroppedSecret, CreateSetsRole] = [true, true, true];
    expect(typePins).toEqual([true, true, true]);

    const accept: AcceptInviteRequest = { code: 'invite-code' };
    expect(accept).not.toHaveProperty('role');

    const settings: HouseholdSettings = { restockMode: 'auto' };
    expect(Object.keys(settings)).toEqual(['restockMode']);

    expect(doc).toContain(
      'invite-role: The role is set when the invite is created. Only an owner can create an owner invite. A member who sends `role` `owner` gets `forbidden_role`. Omitting `role` stores `member`. Accept echoes that role and does not accept a role in the body. The person accepting cannot choose or raise it.',
    );

    const settingsType = source.slice(source.indexOf('export interface HouseholdSettings'), source.indexOf('export interface ExportProductV1'));
    expect(settingsType).toContain('restockMode:');
    expect(settingsType).not.toContain('householdPassword');

    const acceptType = source.slice(source.indexOf('export interface AcceptInviteRequest'), source.indexOf('export interface AcceptInviteResponse'));
    expect(acceptType).not.toContain('role');
    const createType = source.slice(source.indexOf('export interface InviteCreateRequest'), source.indexOf('export interface InviteCreated'));
    expect(createType).toContain("role?: 'member' | 'owner'");
  });
});
