import { Platform } from 'react-native';
import * as SecureStore from 'expo-secure-store';

export const DEFAULT_SERVER_URL = 'https://stockbot.rhionin.com';

export const SERVER_URL_KEY = 'stockbot.serverUrl';
export const PASSWORD_KEY = 'stockbot.householdPassword';

export interface SavedCredentials {
  serverUrl: string;
  password: string;
}

export function normalizeServerUrl(input: string): string {
  const trimmed = input.trim();
  if (trimmed === '') {
    throw new Error('Enter the server address.');
  }
  let url: URL;
  try {
    url = new URL(trimmed);
  } catch {
    throw new Error('Enter a full address, including https://.');
  }
  if (url.protocol !== 'https:' && url.protocol !== 'http:') {
    throw new Error('The address must start with https:// or http://.');
  }
  if (url.username !== '' || url.password !== '') {
    throw new Error('Put the household password in the password field.');
  }
  return url.origin;
}

// Expo web has no secure-store native module. The household sign-in for that
// build stays in localStorage. Phones keep using the secure store.
function browserStore(): Storage | null {
  if (Platform.OS !== 'web' || typeof localStorage === 'undefined') return null;
  return localStorage;
}

export async function loadCredentials(): Promise<SavedCredentials | null> {
  const store = browserStore();
  const [serverUrl, password] = store === null
    ? await Promise.all([
      SecureStore.getItemAsync(SERVER_URL_KEY),
      SecureStore.getItemAsync(PASSWORD_KEY),
    ])
    : [store.getItem(SERVER_URL_KEY), store.getItem(PASSWORD_KEY)];
  if (serverUrl === null || password === null) return null;
  return { serverUrl, password };
}

export async function saveCredentials(credentials: SavedCredentials): Promise<void> {
  const store = browserStore();
  if (store !== null) {
    store.setItem(SERVER_URL_KEY, credentials.serverUrl);
    store.setItem(PASSWORD_KEY, credentials.password);
    return;
  }
  await Promise.all([
    SecureStore.setItemAsync(SERVER_URL_KEY, credentials.serverUrl),
    SecureStore.setItemAsync(PASSWORD_KEY, credentials.password),
  ]);
}

export async function clearCredentials(): Promise<void> {
  const store = browserStore();
  if (store !== null) {
    store.removeItem(SERVER_URL_KEY);
    store.removeItem(PASSWORD_KEY);
    return;
  }
  await Promise.all([
    SecureStore.deleteItemAsync(SERVER_URL_KEY),
    SecureStore.deleteItemAsync(PASSWORD_KEY),
  ]);
}
