import { readFileSync } from 'node:fs';
import { dirname, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { describe, expect, it } from 'vitest';
import {
  formatPairingCode,
  isPairingCode,
  isProductBarcode,
  looksLikePairingCode,
  normalizePairingCode,
} from './contract';

interface PairingVector {
  input: string;
  normalized: string | null;
  looksLike: boolean;
  pairing: boolean;
  gtin: boolean;
}

const root = resolve(dirname(fileURLToPath(import.meta.url)), '../../..');
const vectors = JSON.parse(readFileSync(resolve(root, 'testdata/pairing-codes.json'), 'utf8')) as PairingVector[];

describe('pairing codes', () => {
  it('matches the shared known answers, including alias and grammar negatives', () => {
    expect(vectors.length).toBeGreaterThan(0);
    for (const vector of vectors) {
      expect(normalizePairingCode(vector.input), vector.input).toBe(vector.normalized);
      expect(looksLikePairingCode(vector.input), vector.input).toBe(vector.looksLike);
      expect(isPairingCode(vector.input), vector.input).toBe(vector.pairing);
      expect(isProductBarcode(vector.input), vector.input).toBe(vector.gtin);
      expect(isPairingCode(vector.input) && isProductBarcode(vector.input), vector.input).toBe(false);
      expect(looksLikePairingCode(vector.input) && isProductBarcode(vector.input), vector.input).toBe(false);
    }
  });

  it('formats a payload to the known answer SBP-0123-ABCD-G', () => {
    expect(formatPairingCode('0123ABCD')).toBe('SBP-0123-ABCD-G');
  });
});
