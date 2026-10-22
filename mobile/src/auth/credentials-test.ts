import { DEFAULT_SERVER_URL, normalizeServerUrl } from './credentials';

describe('normalizeServerUrl', () => {
  it('keeps the default public origin and drops a trailing slash or path', () => {
    expect(DEFAULT_SERVER_URL).toBe('https://stockbot.rhionin.com');
    expect(normalizeServerUrl(' https://stockbot.rhionin.com/ ')).toBe('https://stockbot.rhionin.com');
    expect(normalizeServerUrl('https://stockbot.rhionin.com/api')).toBe('https://stockbot.rhionin.com');
  });

  it('keeps an https port and rejects cleartext', () => {
    expect(normalizeServerUrl('https://192.168.1.20:8443')).toBe('https://192.168.1.20:8443');
    expect(() => normalizeServerUrl('http://192.168.1.20:8080')).toThrow(/https:\/\//);
    expect(() => normalizeServerUrl('http://stockbot.rhionin.com')).toThrow(/https:\/\//);
  });

  it('asks for a full address and refuses a password buried in the URL', () => {
    expect(() => normalizeServerUrl('stockbot.rhionin.com')).toThrow(/https:\/\//);
    expect(() => normalizeServerUrl('')).toThrow(/server address/);
    expect(() => normalizeServerUrl('ftp://stockbot.rhionin.com')).toThrow(/https:\/\//);
    expect(() => normalizeServerUrl('https://pantry:secret@stockbot.rhionin.com')).toThrow(/password field/);
  });
});
