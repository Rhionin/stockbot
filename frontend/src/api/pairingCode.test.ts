import { describe, expect, it } from 'vitest';
import {
  formatPairingCode,
  isPairingCode,
  isProductBarcode,
  looksLikePairingCode,
  normalizePairingCode,
} from './contract';

// Fixed known answers. None of the valid codes are built by formatPairingCode.
const vectors: { input: string; looksLike: boolean; pairing: boolean; gtin: boolean }[] = [
  { input: 'SBP-0123-ABCD-G', looksLike: true, pairing: true, gtin: false },
  { input: 'SBP-0000-0014-U', looksLike: true, pairing: true, gtin: false },
  { input: 'SBP-ZZZZ-ZZZZ-F', looksLike: true, pairing: true, gtin: false },
  { input: 'sbp-0123-abcd-g', looksLike: true, pairing: true, gtin: false },
  { input: 'SBP-0123-ABCD-G\r\n', looksLike: true, pairing: true, gtin: false },
  { input: '  SBP-0123-ABCD-G  ', looksLike: true, pairing: true, gtin: false },
  { input: 'SBP-0123-ABCD-0', looksLike: true, pairing: false, gtin: false },
  { input: 'SBP-0213-ABCD-G', looksLike: true, pairing: false, gtin: false },
  { input: 'SBP-0123-ABCO-G', looksLike: false, pairing: false, gtin: false },
  { input: 'SBP-0123-ABCI-G', looksLike: false, pairing: false, gtin: false },
  { input: 'SBP-0123-ABCL-G', looksLike: false, pairing: false, gtin: false },
  { input: 'XBP-0123-ABCD-G', looksLike: false, pairing: false, gtin: false },
  { input: 'SBP-0123-ABCD-GEXTRA', looksLike: false, pairing: false, gtin: false },
  { input: 'xxSBP-0123-ABCD-G', looksLike: false, pairing: false, gtin: false },
  { input: 'SBP-0123-ABCD-G\u017f', looksLike: false, pairing: false, gtin: false },
  { input: '\u017fBP-0123-ABCD-G', looksLike: false, pairing: false, gtin: false },
  { input: 'SBP-0000-0000-0', looksLike: true, pairing: true, gtin: false },
  { input: 'SBP-0000-0000-1', looksLike: true, pairing: false, gtin: false },
  { input: 'SBP-0000-0010-*', looksLike: true, pairing: true, gtin: false },
  { input: 'SBP-0000-0011-~', looksLike: true, pairing: true, gtin: false },
  { input: 'SBP-0000-0012-$', looksLike: true, pairing: true, gtin: false },
  { input: 'SBP-0000-0013-=', looksLike: true, pairing: true, gtin: false },
  { input: 'SBP-0123-ABCD-I', looksLike: false, pairing: false, gtin: false },
  { input: '000111222333', looksLike: false, pairing: false, gtin: true },
  { input: '036000291452', looksLike: false, pairing: false, gtin: true },
  { input: '123456', looksLike: false, pairing: false, gtin: true },
  { input: '1234567', looksLike: false, pairing: false, gtin: true },
  { input: '12345670', looksLike: false, pairing: false, gtin: true },
  { input: '1234567890123', looksLike: false, pairing: false, gtin: true },
  { input: '12345678901231', looksLike: false, pairing: false, gtin: true },
  { input: '123456789012345', looksLike: false, pairing: false, gtin: false },
];

describe('pairing codes', () => {
  it('matches fixed known answers, including grammar negatives', () => {
    for (const vector of vectors) {
      expect(looksLikePairingCode(vector.input), vector.input).toBe(vector.looksLike);
      expect(isPairingCode(vector.input), vector.input).toBe(vector.pairing);
      expect(isProductBarcode(vector.input), vector.input).toBe(vector.gtin);
      expect(isPairingCode(vector.input) && isProductBarcode(vector.input), vector.input).toBe(false);
      expect(looksLikePairingCode(vector.input) && isProductBarcode(vector.input), vector.input).toBe(false);
    }
  });

  it('uppercases ASCII only and trims the whitespace a scan can carry', () => {
    expect(normalizePairingCode('sbp-0123-abcd-g\r\n')).toBe('SBP-0123-ABCD-G');
    expect(normalizePairingCode(' \tSBP-0123-ABCD-G\r')).toBe('SBP-0123-ABCD-G');
    expect(normalizePairingCode('\u017fBP-0123-ABCD-G')).toBe('\u017fBP-0123-ABCD-G');
    expect(normalizePairingCode('SBP-0123-ABCO-G')).toBe('SBP-0123-ABCO-G');
  });

  it('formats a payload to the known answer SBP-0123-ABCD-G', () => {
    expect(formatPairingCode('0123ABCD')).toBe('SBP-0123-ABCD-G');
  });
});
