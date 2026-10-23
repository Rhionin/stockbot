import { readFileSync } from 'node:fs';
import { dirname, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { describe, expect, it } from 'vitest';
import type { HostedErrorCode, LiveHint } from './contract';

// Pins the union to a real member so a deleted code fails typecheck.
const sampleCode: HostedErrorCode = 'busy';
const sampleHint: LiveHint = { type: 'scans', cursor: 'cursor' };

const root = resolve(dirname(fileURLToPath(import.meta.url)), '../../..');
const doc = readFileSync(resolve(root, 'docs/design/api-contract.md'), 'utf8');
const source = readFileSync(resolve(root, 'frontend/src/api/contract.ts'), 'utf8');

// Sentences already shipped, or locked by PR #320. Anything else in the
// registry is new copy and has to say so.
const existingSentences: Record<string, string> = {
  busy: 'Stockbot is busy right now. Try again in a moment.',
  no_space: "Stockbot is out of storage space, so this wasn't saved.",
  not_pending: 'This scan was already handled.',
  product_unresolved: 'Choose which item this is first.',
  none_on_hand: 'Nothing on hand to take out.',
  instance_gone: 'That one was already taken out. Pick another.',
  no_item: 'Which item is this?',
  link_target_gone: "That item isn't available anymore. Pick another.",
  scan_not_found: 'This scan is no longer in the queue.',
  direction_unset: 'Choose stock in or stock out first.',
  internal: 'Something went wrong. Try again.',
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

  it('keeps existing sentences and marks every new sentence for PM', () => {
    for (const row of registry(doc)) {
      const existing = existingSentences[row.code];
      if (existing !== undefined) {
        expect(row.sentence).toBe(existing);
      } else {
        expect(row.notes).toContain('proposed, PM to confirm');
      }
    }
    expect(doc).toContain('generic-sentence: Something went wrong. Try again.');
  });

  it('states the tenant rule and the endpoint index count', () => {
    expect(doc).toContain(
      'tenant-rule: Nothing in this contract accepts a client-supplied tenant other than the X-Household-Id header, and that header is valid only when it names a household the signed-in user belongs to.',
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
});
