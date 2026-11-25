import { PAGE_ORIGIN } from './sessionMode';
import { deleteStored, readStored, writeStored } from './secureStore';

export const DEFAULT_SERVER_URL = 'https://stockbot.rhionin.com';

export const SERVER_URL_KEY = 'stockbot.server';
export const SESSION_TOKEN_KEY = 'stockbot.sessionToken';

export interface SavedCredentials {
  serverUrl: string;
  token: string;
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
    throw new Error('Put your email and password in their own fields.');
  }
  return url.origin;
}

export interface SavedSession {
  serverUrl: string | null;
  token: string | null;
}

export async function loadSavedSession(): Promise<SavedSession> {
  if (PAGE_ORIGIN !== null) return { serverUrl: PAGE_ORIGIN, token: null };
  const [serverUrl, token] = await Promise.all([
    readStored(SERVER_URL_KEY),
    readStored(SESSION_TOKEN_KEY),
  ]);
  return { serverUrl, token };
}

export async function saveCredentials(credentials: SavedCredentials): Promise<void> {
  await Promise.all([
    writeStored(SERVER_URL_KEY, credentials.serverUrl),
    writeStored(SESSION_TOKEN_KEY, credentials.token),
  ]);
}

export async function clearToken(): Promise<void> {
  await deleteStored(SESSION_TOKEN_KEY);
}
