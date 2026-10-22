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
  if (url.protocol !== 'https:') {
    throw new Error('The address must start with https://.');
  }
  if (url.username !== '' || url.password !== '') {
    throw new Error('Put the household password in the password field.');
  }
  return url.origin;
}

export async function loadCredentials(): Promise<SavedCredentials | null> {
  const [serverUrl, password] = await Promise.all([
    SecureStore.getItemAsync(SERVER_URL_KEY),
    SecureStore.getItemAsync(PASSWORD_KEY),
  ]);
  if (serverUrl === null || password === null) return null;
  return { serverUrl, password };
}

export async function saveCredentials(credentials: SavedCredentials): Promise<void> {
  await Promise.all([
    SecureStore.setItemAsync(SERVER_URL_KEY, credentials.serverUrl),
    SecureStore.setItemAsync(PASSWORD_KEY, credentials.password),
  ]);
}

export async function clearCredentials(): Promise<void> {
  await Promise.all([
    SecureStore.deleteItemAsync(SERVER_URL_KEY),
    SecureStore.deleteItemAsync(PASSWORD_KEY),
  ]);
}
