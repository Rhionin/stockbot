import { Platform } from 'react-native';
import { DEFAULT_SERVER_URL, clearCredentials, loadCredentials, normalizeServerUrl, saveCredentials } from './credentials';

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

describe('web sign-in storage', () => {
  const store = new Map<string, string>();
  const original = Object.getOwnPropertyDescriptor(Platform, 'OS');

  beforeEach(() => {
    Object.defineProperty(Platform, 'OS', { configurable: true, get: () => 'web' });
    Object.defineProperty(globalThis, 'localStorage', {
      configurable: true,
      value: {
        getItem: (key: string) => store.get(key) ?? null,
        setItem: (key: string, value: string) => { store.set(key, value); },
        removeItem: (key: string) => { store.delete(key); },
      },
    });
  });

  afterEach(() => {
    store.clear();
    if (original) Object.defineProperty(Platform, 'OS', original);
    delete (globalThis as { localStorage?: unknown }).localStorage;
  });

  it('keeps the household address in localStorage', async () => {
    await saveCredentials({ serverUrl: 'http://127.0.0.1:18081', password: 'pantry' });
    await expect(loadCredentials()).resolves.toEqual({
      serverUrl: 'http://127.0.0.1:18081',
      password: 'pantry',
    });
    await clearCredentials();
    await expect(loadCredentials()).resolves.toBeNull();
  });
});
