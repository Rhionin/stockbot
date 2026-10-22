import { DEFAULT_SERVER_URL, normalizeServerUrl } from './credentials';

describe('normalizeServerUrl', () => {
  it('keeps the default public origin and drops a trailing slash or path', () => {
    expect(DEFAULT_SERVER_URL).toBe('https://stockbot.rhionin.com');
    expect(normalizeServerUrl(' https://stockbot.rhionin.com/ ')).toBe('https://stockbot.rhionin.com');
    expect(normalizeServerUrl('https://stockbot.rhionin.com/api')).toBe('https://stockbot.rhionin.com');
  });

  it('keeps a home server port', () => {
    expect(normalizeServerUrl('http://192.168.1.20:8080')).toBe('http://192.168.1.20:8080');
  });

  it('asks for a full address and refuses a password buried in the URL', () => {
    expect(() => normalizeServerUrl('stockbot.rhionin.com')).toThrow(/https:\/\//);
    expect(() => normalizeServerUrl('')).toThrow(/server address/);
    expect(() => normalizeServerUrl('ftp://stockbot.rhionin.com')).toThrow(/https:\/\/ or http:\/\//);
    expect(() => normalizeServerUrl('https://pantry:secret@stockbot.rhionin.com')).toThrow(/password field/);
  });
});
