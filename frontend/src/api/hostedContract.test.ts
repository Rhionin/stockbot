import { readFileSync } from 'node:fs';
import { dirname, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { describe, expect, it } from 'vitest';
import {
  classifyImportDocument,
  CONFIG_PATH,
  EVENTS_PATH,
  EXPO_CLIENT_PREFIX,
  LIVE_HINT_EVENT,
  UPGRADE_REQUIRED_SENTENCE,
  expoClientHeader,
  type AcceptInviteRequest,
  type HostedErrorCode,
  type HouseholdSettings,
  type InviteCreateRequest,
  type LiveHint,
} from './contract';

// Pins the union to a real member so a deleted code fails typecheck.
const sampleCode: HostedErrorCode = 'busy';
const sampleHint: LiveHint = { type: 'scans', cursor: 'cursor' };

const root = resolve(dirname(fileURLToPath(import.meta.url)), '../../..');
const doc = readFileSync(resolve(root, 'docs/design/api-contract.md'), 'utf8');
const source = readFileSync(resolve(root, 'frontend/src/api/contract.ts'), 'utf8');

// Sentences already shipped, locked by PR #320, or approved by PM on PR #364.
const existingSentences: Record<string, string> = {
  busy: 'Stockbot is busy right now. Try again in a moment.',
  no_space: "Stockbot is out of storage space, so this wasn't saved.",
  unauthorized: 'Sign in to continue.',
  forbidden_role: 'Only an owner can do that.',
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
    expect(source).toContain('householdPasswordEnabled: boolean');
    expect(source).toContain('minClientVersion: { expo: string }');
    expect(source).toContain("type: 'devices'");
    expect(doc).toContain('__Host-stockbot_session');
    expect(doc).toContain('POST /api/auth/login');
    expect(doc).toContain('POST /api/auth/reset');
    expect(doc).toContain('POST /api/household/household-password/retire');
    expect(doc).toContain('Clients label `revoked` as Cancelled.');
    expect(doc).toContain('A missing `X-Stockbot-Client` header means web, so there is no version gate.');
    expect(doc).toContain('Any `X-Stockbot-Client` value that does not start with `expo/` is web too, the same as a missing header, so there is no version gate.');
    expect(doc).toContain('The same `426` applies when that Basic request carries a value that does not start with `expo/`.');
    expect(doc).toContain('On the LAN, `GET /api/telemetry` is open only while the household password is on.');
    expect(doc).toContain('After retirement it follows the LAN `401` rule.');
    expect(doc).toContain('The LAN response never includes the changeover counts.');
    expect(doc).not.toContain('On the LAN it stays open, with no session.');
    expect(doc).not.toContain('Superseded by PM');
    expect(doc).toContain('S6 (#379) must run the production classifier in `internal/scan/qr.go` against `testdata/pairing-codes.json`.');
    expect(doc).toContain('`POST /api/devices/pair/complete` | No session');
    expect(doc).toContain('`Authorization: Basic` and no `X-Stockbot-Client` returns `426` `upgrade_required`, not `401`');
    expect(doc).toContain('activeHouseholdId order');
    expect(source).toContain('export interface LoginResponse');
    expect(doc).toContain('"inviteCode"?: string');
    expect(source).toContain('inviteCode?: string');
    expect(doc).toContain('householdPasswordEnabled: false');
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
    expect(doc).toContain('The pantry server opens two listeners. `:8080` is the LAN entry, published to the host as today. `:8081` is the public entry. It is not published to the host and is reachable only on the Docker network, where the reverse proxy forwards to `pantry:8081`. Everything on `:8081` is public. Everything on `:8080` is LAN. `X-Pantry-Entry` is removed entirely: it is no longer read or honored anywhere. `X-Pantry-Client-IP` is read only on `:8081`, as the rate-limit key. On `:8080` the key is always `RemoteAddr`. `X-Forwarded-For` is never trusted. If `APP_BASE_URL` is set, the server refuses to start unless the public listener (`:8081`) is enabled. There is no fail-open path. On a hairpin or userland-proxy path, the LAN `RemoteAddr` can be the Docker bridge gateway, which would make the 10-streams-per-IP LAN cap effectively household-wide. That is accepted for stage 1.');
    expect(doc).not.toContain('from this classification');
    expect(doc).not.toContain('trusted proxy');
    expect(doc).not.toContain('trusted-proxy');
    expect(doc).not.toContain('Docker bridge subnet');
    expect(doc).not.toContain('acceptable alternative');
    expect(doc).not.toContain('separate upstream port');
    expect(doc).not.toContain('Today LAN and public share `:8080`');
    expect(doc).not.toContain('one matching no public hostname');
    expect(doc).not.toContain('differs from `appBaseUrl`');
    expect(doc).toContain('On the LAN: the LAN principal while the household password is on.');
    expect(doc).toContain('on the LAN after retirement, unauthorized even with no cookie or bearer; Bearer sbs_ on the LAN is always unauthorized.');
    expect(doc).toContain('`unauthorized`, `validation`, `rate_limited`, `busy`. On the LAN, `404` `not_found`');
    expect(doc).toContain('`validation`, `reset_expired`, `not_found`, `rate_limited`, `busy`. On the LAN, `404` `not_found`');
    expect(doc).toContain('The first setup creates the operator as owner of household 1 and sets no session.');
    expect(doc).toContain('Recover resets the existing operator\'s password. It never creates an account, never changes the email, and never changes roles.');
    expect(doc).toContain('The POST body is `{ "setupCode": string, "email": string, "newPassword": string }`.');
    expect(doc).toContain('An unknown or non-operator email gets the same `404` `not_found` as a bad code, so the endpoint never reveals which emails exist.');
    expect(doc).toContain('set the new argon2id hash; revoke all of that account\'s sessions (web and Expo `sbs_`); delete the push subscriptions tied to those sessions and close their SSE streams; invalidate any outstanding reset tokens for that account; consume the code (delete the file and leave recover mode).');
    expect(doc).toContain('Device tokens (`sbd_`) and the household\'s other members are untouched.');
    expect(doc).toContain('No session is set. A successful POST sends the user to sign-in at `APP_BASE_URL`.');
    expect(doc).toContain('`POST /setup`, in both first-setup and recover mode, uses the same per-IP limit as `POST /api/auth/login`, keyed on `RemoteAddr` on `:8080`. After 10 failed setup-code attempts the current code is invalidated, and the operator needs a fresh code from the updater.');
    expect(doc).toContain('The code is single-use with a 24 h expiry, and the setup rate limits defined above apply. An expired code returns `not_found`.');
    expect(doc).toContain('pairing complete, stage-1 `POST /api/households`, and `POST /setup`.');
    expect(doc).toContain('`POST /setup` (first setup and recover) uses the same per-IP limit as login, keyed on `RemoteAddr` on `:8080`.');
    expect(doc).not.toContain('the setup rate limits apply');
    expect(doc).toContain('(f) The 11th failed setup-code attempt finds the code invalidated, so even the correct code then returns `not_found` until a fresh one is issued.');
    expect(doc).not.toContain('public is only the `X-Pantry-Entry` header');
    expect(doc).not.toContain('The port binding is owned by infra.');
    expect(doc).toContain('The proxy configs (`deploy/Caddyfile` and `deploy/Caddyfile.tunnel`) must send every handle to `pantry:8081` and stop sending `X-Pantry-Entry`. That is an infra deploy step owned by the network engineer, paired with S15/S5.');
    expect(doc).toContain('(a) On `:8080`, `X-Pantry-Entry: public` on `POST /api/auth/login` is ignored, so `404` `not_found`, and `GET /api/config` returns `entry` `"lan"`.');
    expect(doc).toContain('(b) On `:8080`, 11 `POST /api/devices/pair/complete` attempts, each with a different `X-Pantry-Client-IP` and `X-Forwarded-For`, are keyed on `RemoteAddr`, so the attempt over the limit gets `429` `rate_limited`.');
    expect(doc).toContain('(c) On `:8081`, `X-Pantry-Client-IP` sets the key and `X-Forwarded-For` is ignored.');
    expect(doc).toContain('(d) On `:8081`, a request with no `X-Pantry-Entry` (`GET /api/telemetry`, `POST /api/telemetry/client`, and `GET /api/build`) is public.');
    expect(doc).toContain('(e) Startup with `APP_BASE_URL` set and the public listener disabled fails.');
    expect(doc).toContain('On routers without NAT hairpin, the certificate (non-tunnel) path can hang from home Wi-Fi (see the comments in `deploy/Caddyfile`).');
    expect(doc).toContain('The client-facing type marks `entry` and `appBaseUrl` optional, because an older server omits them and the client falls back. The Go server always sends both fields.');
    expect(doc).toContain('The hosted push arm types `entry` as `\'public\'`.');
    expect(doc).not.toContain('The Go server always sends `entry`.');
    expect(doc).toContain('Hosted `LiveConfig` sends `entry` `"public"` only.');
    expect(doc).not.toContain('Setup sets it when the person has exactly one household');
    expect(doc).not.toContain('signs them in, and sets `activeHouseholdId`');
    expect(doc).toContain('clientmsg.Generic');
    expect(doc).not.toContain('clientmsg.ServerError');
    expect(doc).toContain('"householdPasswordSignIn": boolean');
    expect(doc).toContain('Required in stage 3 only. The Pi takes the household from the job row.');
    expect(doc).toContain('Trim only the ASCII whitespace characters space, tab, LF, VT, FF, and CR.');
    expect(doc).toContain('typed `*`, `~`, `$`, and `=` are allowed');
    expect(doc).toContain('The LAN listener never offers personal sign-in, whether or not the household password is on.');
    expect(doc).toContain('invite accept with `account`, and `POST /api/households` return `404` `not_found`');
    expect(doc).toContain('`Authorization: Bearer sbs_…` on `:8080` returns `401` `unauthorized` and is never honored.');
    expect(doc).toContain('Session cookies are never set and never honored on the LAN.');
    expect(doc).toContain('lan-client: Expo must use `appBaseUrl` for personal sign-in. LAN URLs are unsupported for people after retirement. Clients never show Sign in on a LAN URL.');
    expect(doc).toContain('"appBaseUrl": string');
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
    expect(doc).toContain('Dev and test servers must set `APP_BASE_URL`, or dev builds would point at prod.');
    expect(doc).toContain('While the household password is on, Expo on a LAN URL may keep using the shared household-1 access, and should offer to move to `appBaseUrl`.');
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
    expect(source).toContain('householdPasswordSignIn: boolean');
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

  it('sets invite role at create and exposes only the household-password state', () => {
    type AcceptHasNoRole = 'role' extends keyof AcceptInviteRequest ? false : true;
    type SettingsHasEnabled = HouseholdSettings['householdPasswordEnabled'] extends boolean ? true : false;
    type SettingsDroppedSecret = 'householdPassword' extends keyof HouseholdSettings ? false : true;
    type CreateSetsRole = NonNullable<InviteCreateRequest['role']> extends 'member' | 'owner' ? true : false;
    const typePins: [AcceptHasNoRole, SettingsHasEnabled, SettingsDroppedSecret, CreateSetsRole] = [
      true,
      true,
      true,
      true,
    ];
    expect(typePins).toEqual([true, true, true, true]);

    const accept: AcceptInviteRequest = { code: 'invite-code' };
    expect(accept).not.toHaveProperty('role');

    const enabled: HouseholdSettings = { restockMode: 'auto', householdPasswordEnabled: true };
    const retired: HouseholdSettings = {
      restockMode: 'auto',
      householdPasswordEnabled: false,
      householdPasswordRestoreUntil: '2026-10-30T00:00:00Z',
    };
    for (const settings of [enabled, retired]) {
      const keys = Object.keys(settings);
      expect(keys).not.toContain('householdPassword');
      expect(keys).not.toContain('password');
      expect(keys.join(' ').toLowerCase()).not.toContain('hash');
      expect(Object.values(settings).join(' ')).not.toMatch(/\$2[aby]\$/);
    }

    expect(doc).toContain(
      'invite-role: The role is set when the invite is created. Only an owner can create an owner invite. A member who sends `role` `owner` gets `forbidden_role`. Omitting `role` stores `member`. Accept echoes that role and does not accept a role in the body. The person accepting cannot choose or raise it.',
    );
    expect(doc).toContain(
      'password-switch: The household password switch returns only `householdPasswordEnabled` and, while the password can still be restored, `householdPasswordRestoreUntil`. It never returns the password or its hash. Only an owner can change the switch. A member gets `forbidden_role`.',
    );

    const settingsType = source.slice(source.indexOf('export interface HouseholdSettings'), source.indexOf('export interface ExportProductV1'));
    expect(settingsType).toContain('householdPasswordEnabled: boolean');
    expect(settingsType).toContain('householdPasswordRestoreUntil?: string');
    expect(settingsType).not.toContain('householdPassword:');
    expect(settingsType.toLowerCase()).not.toContain('hash');

    const acceptType = source.slice(source.indexOf('export interface AcceptInviteRequest'), source.indexOf('export interface AcceptInviteResponse'));
    expect(acceptType).not.toContain('role');
    const createType = source.slice(source.indexOf('export interface InviteCreateRequest'), source.indexOf('export interface InviteCreated'));
    expect(createType).toContain("role?: 'member' | 'owner'");
  });
});
