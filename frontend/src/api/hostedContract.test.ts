import { readFileSync } from 'node:fs';
import { dirname, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { describe, expect, it } from 'vitest';
import {
  classifyImportDocument,
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
    expect(doc).toContain('O, I, and L are rejected on purpose.');
    expect(source).toContain("export const CLIENT_HEADER = 'X-Stockbot-Client'");
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
