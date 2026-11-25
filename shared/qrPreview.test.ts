import { describe, expect, it } from 'vitest';
import { qrPreview } from './qrPreview';

const wifi = 'WIFI:T:WPA;S:SyntheticNet;P:synthetic-pass-123;;';
const vcard = 'BEGIN:VCARD\nFN:Synthetic Person\nTEL:555-0199\nEND:VCARD';
const otpauth = 'otpauth://totp/Synthetic:user?secret=SYNTHETICSECRET123&issuer=SyntheticApp';
const plain = 'buy milk';

describe('qrPreview', () => {
  it('shows a URL host and a fixed label for every other payload', () => {
    expect(qrPreview('https://world.openfoodfacts.org/product/3017620422003')).toBe('world.openfoodfacts.org');
    expect(qrPreview('http://example.com/a')).toBe('example.com');
    expect(qrPreview('www.kroger.com/p/milk')).toBe('www.kroger.com');
    expect(qrPreview('HTTPS://Shop.Example.com/x')).toBe('shop.example.com');
    expect(qrPreview('https://user:synthetic-pass-123@example.com/reset?token=synthetic-token-456')).toBe('example.com');
    expect(qrPreview('  https://example.com/secret-path \r\n')).toBe('example.com');
    expect(qrPreview(wifi)).toBe('Wi-Fi code');
    expect(qrPreview('\tWifi:T:WPA;S:SyntheticNet;P:synthetic-pass-123;;\n')).toBe('Wi-Fi code');
    expect(qrPreview(vcard)).toBe('Contact card');
    expect(qrPreview('begin:vcard\nFN:Synthetic Person')).toBe('Contact card');
    expect(qrPreview(otpauth)).toBe('Authenticator code');
    expect(qrPreview('OTPAuth://totp/Synthetic?secret=SYNTHETICSECRET123')).toBe('Authenticator code');
    expect(qrPreview('mailto:synthetic@example.com')).toBe('QR code');
    expect(qrPreview('ftp://files.example.com/synthetic-file')).toBe('QR code');
    expect(qrPreview('hello')).toBe('QR code');
    expect(qrPreview(`  ${plain}  `)).toBe('QR code');
    expect(qrPreview('123456789012345')).toBe('QR code');
    expect(qrPreview('')).toBe('');
    expect(qrPreview('   ')).toBe('');
  });

  it('never echoes payload text', () => {
    const hidden = [
      wifi,
      'synthetic-pass-123',
      'SyntheticNet',
      vcard,
      'Synthetic Person',
      '555-0199',
      otpauth,
      'SYNTHETICSECRET123',
      'SyntheticApp',
      plain,
      'hello',
      '123456789012345',
      '你',
      'synthetic-token-456',
      'synthetic@example.com',
      'synthetic-file',
    ];
    const previews = hidden.map((payload) => qrPreview(payload));
    for (const preview of previews) {
      for (const secret of hidden) {
        expect(preview).not.toContain(secret);
      }
    }
  });

  it('labels a long plain-text payload instead of cutting it', () => {
    const preview = qrPreview('你'.repeat(60));
    expect(preview).toBe('QR code');
    expect(preview.includes('你')).toBe(false);
  });

  it('cuts a long URL host on a character boundary', () => {
    const host = `${'a'.repeat(60)}.example.com`;
    const preview = qrPreview(`https://${host}/path`);
    expect(Array.from(preview)).toHaveLength(48);
    expect(preview.endsWith('…')).toBe(true);
    expect(preview.startsWith('a')).toBe(true);
    expect(preview.includes(host)).toBe(false);
    expect(preview.includes('/path')).toBe(false);
  });

  it('leaves a no-break space in place, so a padded URL stays unlabeled', () => {
    expect(qrPreview('\u00a0https://example.com/secret-path')).toBe('QR code');
    expect(qrPreview('\u00a0https://example.com/secret-path')).not.toContain('example.com');
  });
});
