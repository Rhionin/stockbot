import { readFileSync } from 'node:fs';
import { dirname, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { describe, expect, it } from 'vitest';
import {
  classifyImportDocument,
  formatPairingCode,
  isPairingCode,
  isProductBarcode,
  pairingCodeCollidesWithBarcode,
  type HostedErrorCode,
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
  device_revoked: 'This scanner was disconnected.',
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
      'tenant-rule: Nothing in this contract accepts a client-supplied tenant other than the X-Household-Id header, and that header is valid only when it names a household the signed-in user belongs to, or, for device calls, the household bound to the device token; nothing else selects a tenant.',
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
    expect(source).toContain('householdPassword: \'on\' | \'off\' | null');
    expect(source).toContain('minClientVersion: { expo: string }');
    expect(source).toContain("type: 'devices'");
    expect(doc).toContain('__Host-stockbot_session');
    expect(doc).toContain('POST /api/auth/login');
    expect(doc).toContain('POST /api/auth/reset');
    expect(doc).toContain('POST /api/household/household-password/retire');
    expect(doc).toContain('householdPassword: null');
    expect(doc).toContain('not_found');
  });

  it('rejects a pairing code as a product barcode', () => {
    expect(doc).toContain(
      'pairing-collision: A pairing code cannot collide with a product barcode.',
    );
    const barcodes = ['000111222333', '036000291452', '12345670', '1234567890123'];
    for (const barcode of barcodes) {
      expect(isProductBarcode(barcode)).toBe(true);
      expect(isPairingCode(barcode)).toBe(false);
      expect(pairingCodeCollidesWithBarcode(barcode)).toBe(false);
    }
    const code = formatPairingCode('0123ABCD');
    expect(code).toMatch(/^SBP-[0-9A-HJKMNP-TV-Z]{4}-[0-9A-HJKMNP-TV-Z]{4}-[0-9A-HJKMNP-TV-Z*~$=U]$/);
    expect(isPairingCode(code)).toBe(true);
    expect(isProductBarcode(code)).toBe(false);
    expect(pairingCodeCollidesWithBarcode(code)).toBe(false);
    const broken = `${code.slice(0, -1)}${code.endsWith('0') ? '1' : '0'}`;
    expect(isPairingCode(broken)).toBe(false);
  });
});
