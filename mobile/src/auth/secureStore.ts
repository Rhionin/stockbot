import * as SecureStore from 'expo-secure-store';

export function readStored(key: string): Promise<string | null> {
  return SecureStore.getItemAsync(key);
}

export function writeStored(key: string, value: string): Promise<void> {
  return SecureStore.setItemAsync(key, value);
}

export function deleteStored(key: string): Promise<void> {
  return SecureStore.deleteItemAsync(key);
}
