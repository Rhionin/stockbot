import * as SecureStore from 'expo-secure-store';
import {
  DEFAULT_SERVER_URL,
  SERVER_URL_KEY,
  SESSION_TOKEN_KEY,
  clearToken,
  loadSavedSession,
  normalizeServerUrl,
  saveCredentials,
} from './credentials';

const mockMemory = new Map<string, string>();

jest.mock('expo-secure-store', () => ({
  getItemAsync: jest.fn(async (key: string) => mockMemory.get(key) ?? null),
  setItemAsync: jest.fn(async (key: string, value: string) => {
    mockMemory.set(key, value);
  }),
  deleteItemAsync: jest.fn(async (key: string) => {
    mockMemory.delete(key);
  }),
}));

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
    expect(() => normalizeServerUrl('https://ada:secret@stockbot.rhionin.com')).toThrow(/own fields/);
  });
});

describe('saved session', () => {
  beforeEach(() => mockMemory.clear());

  it('stores the server and the session token, never a password', async () => {
    await saveCredentials({ serverUrl: 'https://stockbot.example', token: 'sbs_abc' });

    expect([...mockMemory.entries()]).toEqual([
      [SERVER_URL_KEY, 'https://stockbot.example'],
      [SESSION_TOKEN_KEY, 'sbs_abc'],
    ]);
    expect(jest.mocked(SecureStore.setItemAsync).mock.calls.flat().join(' ')).not.toMatch(/password/i);
    await expect(loadSavedSession()).resolves.toEqual({ serverUrl: 'https://stockbot.example', token: 'sbs_abc' });
  });

  it('clears only the token so sign-in keeps the server address', async () => {
    await saveCredentials({ serverUrl: 'https://stockbot.example', token: 'sbs_abc' });

    await clearToken();

    await expect(loadSavedSession()).resolves.toEqual({ serverUrl: 'https://stockbot.example', token: null });
  });
});
